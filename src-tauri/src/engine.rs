// engine — spawns the Go mail engine and bridges the stdio JSON-lines IPC.
//
// Protocol (docs/ipc.md): requests {"id","op","params"} on the engine's
// stdin; responses {"id","ok","data"|"error"} and events {"kind","data"} on
// stdout. No TCP, no sockets: the only channel is the child's stdio pipes,
// so a local attacker cannot connect to it.
//
// Secrets never come back in responses or events: the engine redacts them
// structurally (backend/internal/auth).
use serde_json::{json, Value};
use std::collections::HashMap;
use std::io::{BufRead, BufReader, Write};
use std::path::PathBuf;
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::mpsc::{channel, Sender};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tauri::{AppHandle, Emitter, Manager};

pub const DEFAULT_TIMEOUT: Duration = Duration::from_secs(60);
const LONG_TIMEOUT: Duration = Duration::from_secs(600); // accounts.add
const SEND_TIMEOUT: Duration = Duration::from_secs(90); // SMTP session cap (60s) + overhead
                                                        // ai.chat: local model inference. MUST outlast the engine's own 120 s op cap,
                                                        // so a slow model surfaces the engine's timeout error instead of this bridge
                                                        // giving up first and cutting the UI's loading short.
const AI_TIMEOUT: Duration = Duration::from_secs(150);
const SECRET_OPS: [&str; 5] = [
    "accounts.add",
    "sync.start",
    "send",
    "attachment.save",
    "attachment.text",
];
// engine-internal ops: only Rust may call them (the webview is rejected)
const ENGINE_INTERNAL_OPS: [&str; 1] = ["secrets.restore"];

pub struct Engine {
    app: AppHandle,
    db_path: PathBuf,
    // interior mutability: requests run on the blocking pool CONCURRENTLY
    // (ai.chat can take minutes), so no outer mutex is held across the
    // response wait — the id-keyed protocol multiplexes overlapping calls.
    child: Mutex<Option<Child>>,
    stdin: Mutex<Option<ChildStdin>>,
    pending: Arc<Mutex<HashMap<String, Sender<Value>>>>,
    counter: std::sync::atomic::AtomicU64,
    dead: Arc<std::sync::atomic::AtomicBool>,
    // write-held while backup/restore touch the db file: blocks respawns
    // (any concurrent request would otherwise restart the engine mid-copy)
    gate: std::sync::RwLock<()>,
}

impl Engine {
    pub fn new(app: AppHandle, db_path: PathBuf) -> Self {
        Engine {
            app,
            db_path,
            child: Mutex::new(None),
            stdin: Mutex::new(None),
            pending: Arc::new(Mutex::new(HashMap::new())),
            counter: std::sync::atomic::AtomicU64::new(0),
            dead: Arc::new(std::sync::atomic::AtomicBool::new(false)),
            gate: std::sync::RwLock::new(()),
        }
    }

    /// stopped runs f with the engine stopped and no respawn possible until
    /// f returns (the next request restarts it).
    pub fn stopped<R>(&self, f: impl FnOnce() -> R) -> R {
        let _gate = self.gate.write().unwrap_or_else(|e| e.into_inner());
        self.stop();
        f()
    }

    fn resolve_binary() -> Result<PathBuf, String> {
        if let Ok(p) = std::env::var("MAIL_ENGINE_PATH") {
            let p = PathBuf::from(p);
            if p.exists() {
                return Ok(p);
            }
        }
        // packaged app: the sidecar (bundle.externalBin) sits next to the
        // main executable (Contents/MacOS on macOS, install dir elsewhere)
        if let Ok(exe) = std::env::current_exe() {
            if let Some(dir) = exe.parent() {
                for candidate in [dir.join("mail-engine"), dir.join("mail-engine.exe")] {
                    if candidate.exists() {
                        return Ok(candidate);
                    }
                }
            }
        }
        // development: bun run tauri:dev from the repo root or src-tauri
        let cwd = std::env::current_dir().unwrap_or_else(|_| PathBuf::from("."));
        for candidate in [
            cwd.join("binaries/mail-engine"),
            cwd.join("../src-tauri/binaries/mail-engine"),
        ] {
            if candidate.exists() {
                return Ok(candidate);
            }
        }
        Err(
            "motore non trovato: esegui 'bun run engine:build' (src-tauri/binaries/mail-engine)"
                .into(),
        )
    }

    /// is_running reports whether an engine child is alive right now. Never
    /// spawns one.
    pub(crate) fn is_running(&self) -> bool {
        if self.dead.load(std::sync::atomic::Ordering::SeqCst) {
            return false;
        }
        let mut child = self.child.lock().unwrap();
        child
            .as_mut()
            .map(|c| matches!(c.try_wait(), Ok(None)))
            .unwrap_or(false)
    }

    fn ensure_spawned(&self) -> Result<(), String> {
        {
            // dropped before restore_secrets, which re-enters ensure_spawned
            let _gate = self.gate.read().unwrap_or_else(|e| e.into_inner());
            let mut child = self.child.lock().unwrap();
            let exited = child
                .as_mut()
                .map(|c| c.try_wait().map(|s| s.is_some()).unwrap_or(true))
                .unwrap_or(true);
            if !exited && !self.dead.load(std::sync::atomic::Ordering::SeqCst) {
                return Ok(());
            }
            // (re)spawn — under the child lock so concurrent requests cannot
            // double-spawn; the guard drops before the vault re-delivery
            self.dead.store(false, std::sync::atomic::Ordering::SeqCst);
            if let Some(mut old) = child.take() {
                let _ = old.kill();
                let _ = old.wait();
            }
            *self.stdin.lock().unwrap() = None;

            let bin = Self::resolve_binary()?;
            if let Some(parent) = self.db_path.parent() {
                let _ = std::fs::create_dir_all(parent);
            }
            // Database encryption key (M3): generated on first run, kept in the
            // OS keychain, passed via env — never written next to the database.
            // Fail-hard: an encrypted database cannot open without its key.
            let db_key = crate::secrets::db_key()?;
            let mut command = Command::new(&bin);
            #[cfg(target_os = "windows")]
            {
                use std::os::windows::process::CommandExt;
                // CREATE_NO_WINDOW: il motore è un binario console-subsystem; senza
                // questo flag Windows apre una console vuota visibile accanto all'app.
                command.creation_flags(0x0800_0000);
            }
            let mut spawned = command
                .arg("--serve")
                .arg("--db")
                .arg(&self.db_path)
                .env("MAIL_DB_KEY", &db_key)
                .stdin(Stdio::piped())
                .stdout(Stdio::piped())
                .stderr(Stdio::piped())
                .spawn()
                .map_err(|e| format!("avvio motore ({}): {}", bin.display(), e))?;

            // engine stderr (panics, sync/SMTP errors) goes to a rotating log
            // file instead of /dev/null: app_data_dir/logs/engine.log[.old]
            if let Some(stderr) = spawned.stderr.take() {
                let logs_dir = self
                    .db_path
                    .parent()
                    .unwrap_or(&PathBuf::from("."))
                    .join("logs");
                std::thread::spawn(move || {
                    let _ = std::fs::create_dir_all(&logs_dir);
                    let log_path = logs_dir.join("engine.log");
                    rotate_if_big(&log_path, 1_048_576);
                    if let Ok(mut f) = std::fs::OpenOptions::new()
                        .create(true)
                        .append(true)
                        .open(&log_path)
                    {
                        use std::io::{BufRead, Write as _};
                        let _ = writeln!(f, "=== engine start {} ===", chrono_now());
                        let mut reader = BufReader::new(stderr);
                        loop {
                            let mut line = String::new();
                            match reader.read_line(&mut line) {
                                Ok(0) | Err(_) => break,
                                Ok(_) => {
                                    let _ = writeln!(f, "{}", line.trim_end_matches(['\n', '\r']));
                                }
                            }
                        }
                        let _ = writeln!(f, "=== engine exit {} ===", chrono_now());
                    }
                });
            }

            let stdout = spawned
                .stdout
                .take()
                .ok_or("stdout del motore non disponibile")?;
            *self.stdin.lock().unwrap() = spawned.stdin.take();
            *child = Some(spawned);

            let pending = Arc::clone(&self.pending);
            let app = self.app.clone();
            let dead = Arc::clone(&self.dead);
            std::thread::spawn(move || {
                let reader = BufReader::new(stdout);
                for line in reader.lines() {
                    let line = match line {
                        Ok(l) => l,
                        Err(_) => break,
                    };
                    let value: Value = match serde_json::from_str(&line) {
                        Ok(v) => v,
                        Err(_) => continue,
                    };
                    if let Some(id) = value.get("id").and_then(|v| v.as_str()) {
                        if let Some(tx) = pending.lock().unwrap().remove(id) {
                            let _ = tx.send(value);
                        }
                    } else if value.get("kind").is_some() {
                        let _ = app.emit("mail://event", value);
                    }
                }
                // engine died: fail every pending request
                dead.store(true, std::sync::atomic::Ordering::SeqCst);
                let mut map = pending.lock().unwrap();
                for (_, tx) in map.drain() {
                    let _ = tx.send(json!({"ok": false, "error": "il motore non risponde: è stato riavviato, riprova"}));
                }
            });
        } // child lock guard drops here

        // The engine's runtime vault starts empty after a (re)spawn: re-deliver
        // the keychain secrets so server round-trip ops (markRead, archive,
        // …) work without waiting for the next manual sync. Best-effort: a
        // failure only means those ops will error with "credenziale non
        // disponibile" until the next sync.start. Runs OUTSIDE the child
        // lock: restore_secrets re-enters invoke → ensure_spawned.
        if let Err(e) = self.restore_secrets() {
            eprintln!("engine: restore segreti post-spawn non riuscito: {}", e);
        }
        Ok(())
    }

    /// restore_secrets asks the fresh engine which accounts exist, reads their
    /// credentials from the OS keychain and re-delivers them via the
    /// engine-internal "secrets.restore" op. Only this Rust path may call
    /// that op (engine_invoke rejects it).
    pub(crate) fn restore_secrets(&self) -> Result<usize, String> {
        let accounts = self.invoke("accounts.list", json!({}), DEFAULT_TIMEOUT)?;
        let mut secrets: Vec<Value> = Vec::new();
        if let Some(list) = accounts.as_array() {
            for acc in list {
                if let Some(id) = acc.get("id").and_then(|v| v.as_str()) {
                    if let Ok(secret) = self.account_secret(id) {
                        secrets.push(json!({"accountId": id, "secret": secret}));
                    }
                    // PGP private keyrings (M9) travel the same channel
                    if let Ok(key) = crate::secrets::pgp_get(id) {
                        secrets.push(json!({"accountId": format!("pgp/{}", id), "secret": key}));
                    }
                }
            }
        }
        if secrets.is_empty() {
            return Ok(0);
        }
        let restored = secrets.len();
        self.invoke(
            "secrets.restore",
            json!({ "secrets": secrets }),
            DEFAULT_TIMEOUT,
        )
        .map(|_| restored)
    }

    /// account_secret is the credential the engine needs right now: the
    /// keychain password, or a fresh access token for OAuth accounts (the
    /// keychain holds their refresh token, which IMAP/Graph reject).
    fn account_secret(&self, account_id: &str) -> Result<String, String> {
        let stored = crate::secrets::get_secret(account_id)?;
        crate::oauth::effective_secret(&self.app, account_id, &stored)
    }

    /// invoke sends one op and waits for the matching response. Locks are
    /// held only to spawn and to write the request line: the response wait
    /// is lock-free, so concurrent ops (a long ai.chat beside ordinary mail
    /// calls) multiplex over the id-keyed protocol instead of queueing.
    pub fn invoke(&self, op: &str, params: Value, timeout: Duration) -> Result<Value, String> {
        self.ensure_spawned()?;
        let id = format!(
            "req-{}",
            self.counter
                .fetch_add(1, std::sync::atomic::Ordering::SeqCst)
                + 1
        );
        let (tx, rx) = channel();
        self.pending.lock().unwrap().insert(id.clone(), tx);

        let request = json!({"id": id, "op": op, "params": params});
        let written = {
            let mut guard = self.stdin.lock().unwrap();
            match guard.as_mut() {
                Some(stdin) => stdin
                    .write_all((request.to_string() + "\n").as_bytes())
                    .and_then(|()| stdin.flush())
                    .map_err(|e| format!("scrittura verso il motore: {}", e)),
                None => Err("stdin del motore non disponibile".into()),
            }
        };
        if let Err(e) = written {
            self.pending.lock().unwrap().remove(&id);
            return Err(e);
        }

        match rx.recv_timeout(timeout) {
            Ok(resp) => {
                let ok = resp.get("ok").and_then(|v| v.as_bool()).unwrap_or(false);
                if ok {
                    Ok(resp.get("data").cloned().unwrap_or(Value::Null))
                } else {
                    Err(resp
                        .get("error")
                        .and_then(|v| v.as_str())
                        .unwrap_or("errore del motore")
                        .to_string())
                }
            }
            Err(_) => {
                self.pending.lock().unwrap().remove(&id);
                Err("timeout operazione verso il motore".into())
            }
        }
    }

    pub fn timeout_for(op: &str) -> Duration {
        if op == "accounts.add" {
            LONG_TIMEOUT
        } else if op == "send" {
            SEND_TIMEOUT
        } else if op == "ai.chat" {
            AI_TIMEOUT
        } else {
            // sync.start answers immediately (dial validation only); the
            // actual sync runs in the engine and reports via events
            DEFAULT_TIMEOUT
        }
    }
}

/// envelope builds the UI-facing result { ok, data?, error? }.
pub(crate) fn envelope(result: Result<Value, String>) -> Value {
    match result {
        Ok(data) => json!({"ok": true, "data": data}),
        Err(error) => json!({"ok": false, "error": error}),
    }
}

/// run_engine offloads one engine round-trip to the blocking pool. Tauri
/// runs SYNC commands on the main thread, so a multi-second recv (local AI
/// inference, IMAP/SMTP round-trips) would beach-ball the whole app. Async
/// commands + spawn_blocking keep the UI responsive; Engine carries its own
/// locks so overlapping requests multiplex over the id-keyed protocol.
pub(crate) async fn run_engine<F>(state: tauri::State<'_, Arc<Engine>>, f: F) -> Value
where
    F: FnOnce(&Engine) -> Result<Value, String> + Send + 'static,
{
    let engine = Arc::clone(state.inner());
    match tauri::async_runtime::spawn_blocking(move || f(&engine)).await {
        Ok(result) => envelope(result),
        Err(e) => envelope(Err(format!("engine task non riuscito: {}", e))),
    }
}

#[tauri::command]
pub async fn engine_invoke(
    state: tauri::State<'_, Arc<Engine>>,
    app: AppHandle,
    op: String,
    params: Value,
) -> Result<Value, ()> {
    // secret-bearing ops must go through the dedicated commands so the
    // webview never passes (or receives) credentials here.
    if SECRET_OPS.contains(&op.as_str()) {
        return Ok(envelope(Err(format!(
            "l'op {} richiede il keychain: usa i comandi dedicati",
            op
        ))));
    }
    if ENGINE_INTERNAL_OPS.contains(&op.as_str()) {
        return Ok(envelope(Err(format!(
            "l'op {} è interna al motore: gestita da Tauri, non dalla UI",
            op
        ))));
    }
    // ops that read/write a host file: only paths the user picked
    let path_keys: &[&str] = match op.as_str() {
        "export.message" | "export.mailbox" => &["dest"],
        "import.mails" => &["src"],
        _ => &[],
    };
    if let Err(e) = crate::paths::check_params(&app, &params, path_keys) {
        return Ok(envelope(Err(e)));
    }
    Ok(run_engine(state, move |engine| {
        engine.invoke(&op, params, Engine::timeout_for(&op))
    })
    .await)
}

#[tauri::command]
pub async fn account_add(
    state: tauri::State<'_, Arc<Engine>>,
    app: AppHandle,
    name: String,
    email: String,
    imap: Value,
    smtp: Option<Value>,
    secret: String,
    provider: Option<String>,
    allow_insecure: Option<bool>,
) -> Result<Value, ()> {
    let is_oauth = matches!(
        provider.as_deref(),
        Some("gmail-oauth") | Some("microsoft-oauth") | Some("microsoft")
    );
    Ok(run_engine(state, move |engine| {
        // the engine validates the account by logging in: OAuth accounts need a
        // fresh access token there, not the refresh token kept in the keychain
        let engine_secret = if provider.as_deref() == Some("gmail-oauth") {
            crate::oauth::google_refresh(&app, &secret)?.0
        } else {
            secret.clone()
        };
        let params = json!({
            "name": name,
            "email": email,
            "imap": imap,
            "smtp": smtp,
            "secret": engine_secret,
            "allowInsecure": allow_insecure.unwrap_or(false),
            // "microsoft-oauth" is the marker's name; the engine knows "microsoft"
            "provider": match provider.as_deref() {
                Some("microsoft-oauth") => Some("microsoft"),
                other => other,
            },
        });
        engine
            .invoke("accounts.add", params, Engine::timeout_for("accounts.add"))
            .and_then(|account| {
                // store the secret only after the engine validated + persisted the
                // account; on keychain failure roll the account back.
                let id = account
                    .get("id")
                    .and_then(|v| v.as_str())
                    .ok_or_else(|| "risposta del motore senza id account".to_string())?
                    .to_string();
                match crate::secrets::set_secret(&id, &secret).and_then(|()| {
                    // OAuth marker: oauth/<id> exists only for OAuth accounts and
                    // caches the short-lived access token (effective_secret).
                    if is_oauth {
                        crate::secrets::set(
                            &format!("oauth/{}", id),
                            &json!({
                                "access": "",
                                "expiry": 0,
                                "provider": provider.as_deref().unwrap_or("")
                            })
                            .to_string(),
                        )?;
                    }
                    Ok(())
                }) {
                    Ok(()) => Ok(account),
                    Err(e) => {
                        let _ = engine.invoke(
                            "accounts.remove",
                            json!({"accountId": id}),
                            DEFAULT_TIMEOUT,
                        );
                        Err(format!("{} (account rimosso: riprova)", e))
                    }
                }
            })
    })
    .await)
}

#[tauri::command]
pub async fn account_sync(
    state: tauri::State<'_, Arc<Engine>>,
    account_id: String,
) -> Result<Value, ()> {
    Ok(run_engine(state, move |engine| {
        engine.account_secret(&account_id).and_then(|secret| {
            engine.invoke(
                "sync.start",
                json!({"accountId": account_id, "secret": secret}),
                Engine::timeout_for("sync.start"),
            )
        })
    })
    .await)
}

#[tauri::command]
pub async fn account_send(
    state: tauri::State<'_, Arc<Engine>>,
    app: AppHandle,
    account_id: String,
    message: Value,
) -> Result<Value, ()> {
    // outgoing attachments are read from disk by the engine: picked files only
    if let Some(files) = message.get("files").and_then(|f| f.as_array()) {
        for f in files {
            if let Err(e) = crate::paths::check_params(&app, f, &["path"]) {
                return Ok(envelope(Err(e)));
            }
        }
    }
    Ok(run_engine(state, move |engine| {
        engine.account_secret(&account_id).and_then(|secret| {
            engine.invoke(
                "send",
                json!({"accountId": account_id, "secret": secret, "message": message}),
                Engine::timeout_for("send"),
            )
        })
    })
    .await)
}

#[tauri::command]
pub async fn attachment_export(
    state: tauri::State<'_, Arc<Engine>>,
    app: AppHandle,
    account_id: String,
    message_id: String,
    name: String,
    dest: String,
) -> Result<Value, ()> {
    if let Err(e) = crate::paths::check(&app, &dest) {
        return Ok(envelope(Err(e)));
    }
    Ok(run_engine(state, move |engine| {
        engine.account_secret(&account_id).and_then(|secret| {
            engine.invoke(
                "attachment.save",
                json!({"messageId": message_id, "name": name, "dest": dest, "secret": secret}),
                Engine::timeout_for("attachment.save"),
            )
        })
    })
    .await)
}

/// attachment_text extracts the plain text of a PDF attachment for the local
/// AI (M12): same secret path as attachment_export, but the engine returns
/// the extracted text instead of writing a file. The text goes to the
/// user-configured local model only.
#[tauri::command]
pub async fn attachment_text(
    state: tauri::State<'_, Arc<Engine>>,
    account_id: String,
    message_id: String,
    name: String,
) -> Result<Value, ()> {
    Ok(run_engine(state, move |engine| {
        engine.account_secret(&account_id).and_then(|secret| {
            engine.invoke(
                "attachment.text",
                json!({"messageId": message_id, "name": name, "secret": secret}),
                Engine::timeout_for("attachment.text"),
            )
        })
    })
    .await)
}

/// attachment_preview returns one IMAGE attachment's bytes for the inline
/// viewer: same keychain-only secret discipline as attachment_text.
#[tauri::command]
pub async fn attachment_preview(
    state: tauri::State<'_, Arc<Engine>>,
    account_id: String,
    message_id: String,
    name: String,
) -> Result<Value, ()> {
    Ok(run_engine(state, move |engine| {
        engine.account_secret(&account_id).and_then(|secret| {
            engine.invoke(
                "attachment.preview",
                json!({"messageId": message_id, "name": name, "secret": secret}),
                Engine::timeout_for("attachment.text"),
            )
        })
    })
    .await)
}

#[tauri::command]
pub async fn attachment_open(
    state: tauri::State<'_, Arc<Engine>>,
    app: AppHandle,
    account_id: String,
    message_id: String,
    name: String,
) -> Result<Value, ()> {
    Ok(run_engine(state, move |engine| {
        open_attachment(engine, &app, &account_id, &message_id, &name)
            .map(|path| json!({ "path": path }))
    })
    .await)
}

/// open_attachment downloads an attachment into the per-message cache dir
/// (app_data/attachments/<messageId>/) and opens it with the OS default
/// application. Re-downloads on every open so the file is never stale.
fn open_attachment(
    engine: &Engine,
    app: &AppHandle,
    account_id: &str,
    message_id: &str,
    name: &str,
) -> Result<String, String> {
    if message_id.trim().is_empty() || name.trim().is_empty() {
        return Err("messageId e name sono obbligatori".into());
    }
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("cartella dati non disponibile: {}", e))?
        .join("attachments")
        .join(sanitize_component(message_id));
    std::fs::create_dir_all(&dir).map_err(|e| format!("creazione cache allegati: {}", e))?;
    let dest = dir.join(sanitize_component(name));
    let secret = engine.account_secret(account_id)?;
    engine.invoke(
        "attachment.save",
        json!({
            "messageId": message_id,
            "name": name,
            "dest": dest.to_string_lossy(),
            "secret": secret,
        }),
        Engine::timeout_for("attachment.save"),
    )?;
    open_with_default(&dest)?;
    Ok(dest.to_string_lossy().to_string())
}

/// sanitize_component reduces a path component to a safe single segment:
/// path separators, ".." and control characters become underscores.
fn sanitize_component(raw: &str) -> String {
    let cleaned: String = raw
        .chars()
        .map(|c| match c {
            '/' | '\\' | ':' | '*' | '?' | '"' | '<' | '>' | '|' | '\u{0}'..='\u{1f}' => '_',
            _ => c,
        })
        .collect();
    let trimmed = cleaned.trim_matches('.').trim();
    let safe = if trimmed.is_empty() {
        "allegato"
    } else {
        trimmed
    };
    safe.chars().take(120).collect()
}

/// open_with_default opens a file with the platform default application.
/// The per-platform `return`s are load-bearing on the targets where more
/// cfg blocks follow — clippy only sees one block per build.
#[allow(clippy::needless_return)]
fn open_with_default(path: &std::path::Path) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    {
        std::process::Command::new("open")
            .arg(path)
            .spawn()
            .map_err(|e| format!("apertura con l'app predefinita: {}", e))?;
        return Ok(());
    }
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        std::process::Command::new("cmd")
            .args(["/C", "start", ""])
            .arg(path)
            .creation_flags(0x0800_0000) // niente flash di console
            .spawn()
            .map_err(|e| format!("apertura con l'app predefinita: {}", e))?;
        return Ok(());
    }
    #[cfg(target_os = "linux")]
    {
        std::process::Command::new("xdg-open")
            .arg(path)
            .spawn()
            .map_err(|e| format!("apertura con l'app predefinita: {}", e))?;
        return Ok(());
    }
    #[cfg(not(any(target_os = "macos", target_os = "windows", target_os = "linux")))]
    {
        let _ = path;
        Err("apertura allegati non supportata su questa piattaforma".into())
    }
}

#[tauri::command]
pub async fn attachment_download(
    state: tauri::State<'_, Arc<Engine>>,
    app: AppHandle,
    account_id: String,
    message_id: String,
    name: String,
) -> Result<Value, ()> {
    Ok(run_engine(state, move |engine| {
        download_attachment(engine, &app, &account_id, &message_id, &name)
            .map(|path| json!({ "path": path }))
    })
    .await)
}

/// download_attachment fetches an attachment straight into the system
/// Downloads folder (one click, no dialog). When a file with the same name
/// exists, a numeric suffix is appended: report.pdf → report (1).pdf.
fn download_attachment(
    engine: &Engine,
    app: &AppHandle,
    account_id: &str,
    message_id: &str,
    name: &str,
) -> Result<String, String> {
    if message_id.trim().is_empty() || name.trim().is_empty() {
        return Err("messageId e name sono obbligatori".into());
    }
    let downloads = app
        .path()
        .download_dir()
        .map_err(|e| format!("cartella Scaricati non disponibile: {}", e))?;
    std::fs::create_dir_all(&downloads).map_err(|e| format!("creazione Scaricati: {}", e))?;
    let dest = unique_path(&downloads.join(sanitize_component(name)))?;
    let secret = engine.account_secret(account_id)?;
    engine.invoke(
        "attachment.save",
        json!({
            "messageId": message_id,
            "name": name,
            "dest": dest.to_string_lossy(),
            "secret": secret,
        }),
        Engine::timeout_for("attachment.save"),
    )?;
    Ok(dest.to_string_lossy().to_string())
}

/// unique_path returns a non-existing path next to the requested one by
/// appending " (n)" before the extension (report.pdf → report (1).pdf).
fn unique_path(path: &std::path::Path) -> Result<std::path::PathBuf, String> {
    if !path.exists() {
        return Ok(path.to_path_buf());
    }
    let stem = path
        .file_stem()
        .map(|s| s.to_string_lossy().to_string())
        .unwrap_or_else(|| "file".into());
    let ext = path.extension().map(|s| s.to_string_lossy().to_string());
    for i in 1..1000 {
        let candidate = match &ext {
            Some(e) => path.with_file_name(format!("{} ({}).{}", stem, i, e)),
            None => path.with_file_name(format!("{} ({})", stem, i)),
        };
        if !candidate.exists() {
            return Ok(candidate);
        }
    }
    Err("troppi file con lo stesso nome nella cartella".into())
}

#[tauri::command]
pub async fn account_remove(
    state: tauri::State<'_, Arc<Engine>>,
    account_id: String,
) -> Result<Value, ()> {
    Ok(run_engine(state, move |engine| {
        let result = engine.invoke(
            "accounts.remove",
            json!({"accountId": account_id}),
            Engine::timeout_for("accounts.remove"),
        );
        // keychain cleanup regardless of the engine outcome
        if let Err(e) = crate::secrets::delete_secret(&account_id) {
            eprintln!("secrets: {}", e);
        }
        // OAuth marker + access-token cache (best effort)
        let _ = crate::secrets::delete(&format!("oauth/{}", account_id));
        result
    })
    .await)
}

/// secrets_restore re-delivers the keychain secrets of every known account
/// into the engine's runtime vault. Called by Tauri after each engine spawn
/// (see Engine::ensure_spawned); also exposed as a command so the UI can
/// force a re-delivery if a spawn-time restore failed. The secrets travel
/// Rust -> engine only; the webview never sees them.
#[tauri::command]
pub async fn secrets_restore(state: tauri::State<'_, Arc<Engine>>) -> Result<Value, ()> {
    Ok(run_engine(state, move |engine| {
        engine
            .restore_secrets()
            .map(|restored| json!({ "restored": restored }))
    })
    .await)
}

/// Rotates the engine log when it grows past max_bytes: engine.log -> engine.log.old.
fn rotate_if_big(log_path: &PathBuf, max_bytes: u64) {
    if let Ok(meta) = std::fs::metadata(log_path) {
        if meta.len() > max_bytes {
            let old = log_path.with_extension("log.old");
            let _ = std::fs::rename(log_path, old);
        }
    }
}

/// UTC timestamp for log markers (no chrono dependency).
fn chrono_now() -> String {
    let secs = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    // coarse civil-time conversion (UTC): days since epoch -> y/m/d
    let days = secs / 86_400;
    let rem = secs % 86_400;
    let (h, mi, s) = (rem / 3600, (rem % 3600) / 60, rem % 60);
    let z = days + 719_468;
    let era = z / 146_097;
    let doe = z % 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = if m <= 2 { y + 1 } else { y };
    format!("{:04}-{:02}-{:02}T{:02}:{:02}:{:02}Z", y, m, d, h, mi, s)
}

impl Engine {
    /// stop terminates the engine child (if any) so the local DB file is no
    /// longer held open. The next invoke respawns a fresh engine. Closing
    /// stdin first lets the engine checkpoint the WAL into the db file and
    /// exit cleanly (a killed engine leaves recent writes in mail.db-wal);
    /// kill only if it does not exit in time.
    pub fn stop(&self) {
        *self.stdin.lock().unwrap() = None;
        if let Some(mut old) = self.child.lock().unwrap().take() {
            let deadline = std::time::Instant::now() + Duration::from_secs(5);
            while std::time::Instant::now() < deadline {
                if let Ok(Some(_)) = old.try_wait() {
                    break;
                }
                std::thread::sleep(Duration::from_millis(50));
            }
            let _ = old.kill();
            let _ = old.wait();
        }
        self.dead.store(true, std::sync::atomic::Ordering::SeqCst);
    }
}

/// sqlite WAL sidecar path for a database file (the -shm index is
/// rebuildable and must never be copied along).
fn wal_path(db: &std::path::Path) -> std::path::PathBuf {
    let mut s = db.as_os_str().to_os_string();
    s.push("-wal");
    std::path::PathBuf::from(s)
}

/// Sync the WAL sidecar of a copied database: the backup carries the source
/// WAL frames (no committed write is lost); without one the stale target WAL
/// is removed.
fn copy_wal(src_db: &std::path::Path, dest_db: &std::path::Path) {
    let src_wal = wal_path(src_db);
    let dest_wal = wal_path(dest_db);
    if src_wal.exists() {
        let _ = std::fs::copy(&src_wal, &dest_wal);
    } else {
        let _ = std::fs::remove_file(&dest_wal);
    }
    let dest_shm = {
        let mut s = dest_db.as_os_str().to_os_string();
        s.push("-shm");
        std::path::PathBuf::from(s)
    };
    let _ = std::fs::remove_file(&dest_shm); // rebuilt on open, never copied
}

/// backup_create snapshots the encrypted database FILE with the engine
/// stopped. File-level copy is the only VFS-safe way here: the adiantum
/// encrypting VFS (M3) cannot VACUUM INTO arbitrary host paths (SQLite
/// answers "unable to open"). The WAL sidecar travels along so no committed
/// frame is lost; an existing destination is overwritten.
#[tauri::command]
pub async fn backup_create(
    state: tauri::State<'_, Arc<Engine>>,
    app: AppHandle,
    dest: String,
) -> Result<Value, ()> {
    if let Err(e) = crate::paths::check(&app, &dest) {
        return Ok(envelope(Err(e)));
    }
    Ok(run_engine(state, move |engine| {
        let src = engine.db_path.clone();
        if src == std::path::Path::new(&dest) {
            return Err(String::from(
                "la destinazione coincide col database corrente",
            ));
        }
        // no open handles, no concurrent writes, no respawn mid-copy
        engine.stopped(|| match std::fs::copy(&src, &dest) {
            Ok(size) => {
                copy_wal(&src, std::path::Path::new(&dest));
                Ok(json!({"path": dest, "size": size}))
            }
            Err(e) => Err(format!("backup non riuscito: {}", e)),
        })
    })
    .await)
}

/// backup_restore replaces the local database with a previously saved backup
/// (made by backup_create). Destructive by definition: the UI asks for
/// confirmation. Order matters: the backup is READ and staged into the
/// app-data dir FIRST, while the engine is still alive — if the source is
/// unreadable (macOS TCC, iCloud placeholder, missing file) the error
/// surfaces immediately and the app keeps working. Only after a successful
/// staging does the engine stop for the atomic swap. Stale WAL/SHM sidecars
/// are dropped; the backup's WAL is restored with the file. The DB
/// encryption key is unchanged (same keychain).
#[tauri::command]
pub async fn backup_restore(
    state: tauri::State<'_, Arc<Engine>>,
    app: AppHandle,
    src: String,
) -> Result<Value, ()> {
    if let Err(e) = crate::paths::check(&app, &src) {
        return Ok(envelope(Err(e)));
    }
    Ok(run_engine(state, move |engine| {
        let dest = engine.db_path.clone();
        // 1) stage the backup next to the live db (same volume => rename works).
        //    A zero-byte staged file means an unreadable source (iCloud
        //    placeholder or race) — reject before touching anything.
        let tmp = dest.with_extension("db.restore.tmp");
        let _ = std::fs::remove_file(&tmp);
        match std::fs::copy(&src, &tmp) {
            Ok(size) if size > 0 => {}
            Ok(_) => {
                let _ = std::fs::remove_file(&tmp);
                return Err(String::from(
                    "il file di backup risulta vuoto o non scaricato (iCloud?): riprova dopo averlo aperto",
                ));
            }
            Err(e) => {
                let _ = std::fs::remove_file(&tmp);
                eprintln!("backup_restore: stage copy failed: src={} err={}", src, e);
                return Err(format!(
                    "lettura del backup non riuscita ({}): {}",
                    src, e
                ));
            }
        }
        if std::fs::metadata(&tmp)
            .map(|m| m.len() < 4096)
            .unwrap_or(true)
        {
            let _ = std::fs::remove_file(&tmp);
            return Err(String::from(
                "il file scelto non sembra un backup del database (troppo piccolo)",
            ));
        }
        // 2) swap: engine down, staged file over the live db, atomic rename.
        //    Before the swap keep a safety copy of the CURRENT db: a bad restore
        //    stays reversible (mail.db.pre-restore.bak, best effort).
        let safety = dest.with_extension("db.pre-restore.bak");
        let _ = std::fs::copy(&dest, &safety);
        engine.stopped(|| match std::fs::rename(&tmp, &dest) {
            Ok(_) => {
                copy_wal(std::path::Path::new(&src), &dest);
                Ok(json!({"ok": true}))
            }
            Err(e) => {
                let _ = std::fs::remove_file(&tmp);
                eprintln!(
                    "backup_restore: rename failed: dest={} err={}",
                    dest.display(),
                    e
                );
                Err(format!(
                    "sostituzione del database non riuscita: {}",
                    e
                ))
            }
        })
    })
    .await)
}
