// oauth — Google (Gmail) OAuth2 for the mail engine (M4).
//
// Flow: the UI invokes the oauth_google_start command; we open the system
// browser on Google's consent screen (authorization-code + PKCE), receive
// the redirect on a loopback HTTP listener (127.0.0.1:<port>/callback),
// exchange the code for tokens and hand { email, refreshToken } back to the
// UI, which creates the account. The REFRESH token is stored in the keychain
// as the account secret (account/<id>); an oauth/<id> keychain entry marks
// OAuth accounts and caches the short-lived ACCESS token ({access, expiry}).
//
// Client credentials come from oauth.json in the app-data dir (the user
// registers their own Google Cloud project — see docs/oauth.md); they are
// never embedded in the binary and never leave this process except to Google.

use serde_json::{json, Value};
use std::io::{BufRead, BufReader, Write};
use std::net::TcpListener;
use std::time::{SystemTime, UNIX_EPOCH};
use tauri::Manager;

const AUTH_URL: &str = "https://accounts.google.com/o/oauth2/v2/auth";
const TOKEN_URL: &str = "https://oauth2.googleapis.com/token";
const SCOPE: &str = "https://mail.google.com/ email";
const DEFAULT_PORT: u16 = 8734;

struct GoogleConfig {
    client_id: String,
    client_secret: String,
    port: u16,
}

fn load_config(app: &tauri::AppHandle) -> Result<GoogleConfig, String> {
    let path = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("cartella app non disponibile: {}", e))?
        .join("oauth.json");
    let raw = std::fs::read_to_string(&path).map_err(|_| {
        format!(
            "oauth.json non trovato (atteso: {}) — segui docs/oauth.md per configurare Google OAuth",
            path.display()
        )
    })?;
    let v: Value =
        serde_json::from_str(&raw).map_err(|e| format!("oauth.json non valido: {}", e))?;
    let g = v
        .get("google")
        .ok_or_else(|| "oauth.json: manca la sezione \"google\"".to_string())?;
    Ok(GoogleConfig {
        client_id: g
            .get("client_id")
            .and_then(|x| x.as_str())
            .ok_or_else(|| "oauth.json: client_id mancante".to_string())?
            .to_string(),
        client_secret: g
            .get("client_secret")
            .and_then(|x| x.as_str())
            .ok_or_else(|| "oauth.json: client_secret mancante".to_string())?
            .to_string(),
        port: g
            .get("port")
            .and_then(|x| x.as_u64())
            .map(|p| p as u16)
            .unwrap_or(DEFAULT_PORT),
    })
}

// --- helpers ---

/// b64url encodes without padding using the base64url alphabet.
fn b64url(data: &[u8]) -> String {
    const T: &[u8] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
    let mut out = String::new();
    for chunk in data.chunks(3) {
        let b0 = chunk[0] as u32;
        let b1 = *chunk.get(1).unwrap_or(&0) as u32;
        let b2 = *chunk.get(2).unwrap_or(&0) as u32;
        let n = (b0 << 16) | (b1 << 8) | b2;
        out.push(T[(n >> 18) as usize & 63] as char);
        out.push(T[(n >> 12) as usize & 63] as char);
        if chunk.len() > 1 {
            out.push(T[(n >> 6) as usize & 63] as char);
        }
        if chunk.len() > 2 {
            out.push(T[n as usize & 63] as char);
        }
    }
    out
}

fn b64url_decode(s: &str) -> Option<Vec<u8>> {
    fn val(c: u8) -> Option<u32> {
        match c {
            b'A'..=b'Z' => Some((c - b'A') as u32),
            b'a'..=b'z' => Some((c - b'a' + 26) as u32),
            b'0'..=b'9' => Some((c - b'0' + 52) as u32),
            b'-' => Some(62),
            b'_' => Some(63),
            _ => None,
        }
    }
    let bytes: Vec<u8> = s.bytes().filter(|b| !b"\r\n ".contains(b)).collect();
    let mut out = Vec::with_capacity(bytes.len() * 3 / 4);
    for chunk in bytes.chunks(4) {
        if chunk.len() < 2 {
            return None;
        }
        let mut n: u32 = 0;
        for (i, c) in chunk.iter().enumerate() {
            n |= val(*c)? << (18 - 6 * i);
        }
        out.push((n >> 16) as u8);
        if chunk.len() > 2 {
            out.push((n >> 8) as u8);
        }
        if chunk.len() > 3 {
            out.push(n as u8);
        }
    }
    Some(out)
}

/// urlencode percent-encodes every byte outside the RFC 3986 unreserved set.
fn urlencode(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            _ => out.push_str(&format!("%{:02X}", b)),
        }
    }
    out
}

fn random_string(len: usize) -> String {
    use rand::RngCore;
    let mut bytes = vec![0u8; len];
    rand::thread_rng().fill_bytes(&mut bytes);
    b64url(&bytes)
}

fn open_url(url: &str) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    let mut cmd = {
        let mut c = std::process::Command::new("open");
        c.arg(url);
        c
    };
    #[cfg(target_os = "windows")]
    let mut cmd = {
        use std::os::windows::process::CommandExt;
        let mut c = std::process::Command::new("cmd");
        c.args(["/C", "start", "", url]).creation_flags(0x0800_0000); // niente flash di console
        c
    };
    #[cfg(target_os = "linux")]
    let mut cmd = {
        let mut c = std::process::Command::new("xdg-open");
        c.arg(url);
        c
    };
    cmd.spawn()
        .map(|_| ())
        .map_err(|e| format!("apertura browser non riuscita: {}", e))
}

fn now_unix() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

/// email_from_id_token decodes the JWT payload (no signature check needed:
/// the token arrived directly from Google over TLS in our own flow).
fn email_from_id_token(id_token: &str) -> Option<String> {
    let payload = id_token.split('.').nth(1)?;
    let raw = b64url_decode(payload)?;
    let v: Value = serde_json::from_slice(&raw).ok()?;
    v.get("email")
        .and_then(|e| e.as_str())
        .map(|s| s.to_string())
}

fn token_post(form: &[(&str, &str)]) -> Result<Value, String> {
    token_post_at(TOKEN_URL, form)
}

fn token_post_at(url: &str, form: &[(&str, &str)]) -> Result<Value, String> {
    let resp = ureq::post(url)
        .send_form(form)
        .map_err(|e| format!("scambio token non riuscito: {}", e))?;
    resp.into_json::<Value>()
        .map_err(|e| format!("risposta token non valida: {}", e))
}

// --- flow ---

/// google_start runs the browser flow and returns (email, refresh_token).
pub fn google_start(app: &tauri::AppHandle) -> Result<(String, String), String> {
    let cfg = load_config(app)?;

    // PKCE S-256
    let verifier = random_string(64);
    let challenge = {
        use sha2::{Digest, Sha256};
        let mut h = Sha256::new();
        h.update(verifier.as_bytes());
        b64url(&h.finalize())
    };
    let state = random_string(16);

    let listener = TcpListener::bind(("127.0.0.1", cfg.port)).map_err(|e| {
        format!(
            "porta {} occupata: {} (chiudi l'altro flusso o cambia port in oauth.json)",
            cfg.port, e
        )
    })?;

    let redirect = format!("http://127.0.0.1:{}/callback", cfg.port);
    let auth_url = format!(
        "{}?client_id={}&redirect_uri={}&response_type=code&scope={}&access_type=offline&prompt=consent&state={}&code_challenge={}&code_challenge_method=S256",
        AUTH_URL,
        urlencode(&cfg.client_id),
        urlencode(&redirect),
        urlencode(SCOPE),
        state,
        challenge
    );
    open_url(&auth_url)?;

    // wait for exactly one redirect (the UI keeps the button in loading state)
    let (mut stream, _) = listener
        .accept()
        .map_err(|e| format!("redirect non ricevuto: {}", e))?;
    drop(listener);

    let mut reader = BufReader::new(&stream);
    let mut request_line = String::new();
    reader
        .read_line(&mut request_line)
        .map_err(|e| format!("richiesta di redirect illeggibile: {}", e))?;
    let target = request_line.split_whitespace().nth(1).unwrap_or("");
    let (path, query) = match target.split_once('?') {
        Some((p, q)) => (p, q),
        None => (target, ""),
    };
    if path != "/callback" {
        return Err("redirect inatteso (path sconosciuto)".into());
    }
    let mut code = String::new();
    let mut got_state = String::new();
    for kv in query.split('&') {
        let (k, v) = kv.split_once('=').unwrap_or((kv, ""));
        match k {
            "code" => code = v.to_string(),
            "state" => got_state = v.to_string(),
            _ => {}
        }
    }
    if code.is_empty() {
        return Err("autorizzazione negata o codice mancante".into());
    }
    if got_state != state {
        return Err("state non corrispondente (possibile CSRF): riprova".into());
    }

    let _ = stream.write_all(
        b"HTTP/1.1 200 OK
Content-Type: text/html; charset=utf-8
Connection: close

          <!doctype html><meta charset='utf-8'><title>Mail</title>          <body style='font-family:-apple-system,sans-serif;text-align:center;padding-top:3rem'>          <h2>&#10003; Autorizzazione completata</h2>          <p>Puoi chiudere questa finestra e tornare a Mail.</p></body>",
    );
    let _ = stream.flush();
    drop(stream);

    let tokens = token_post(&[
        ("code", &code),
        ("client_id", &cfg.client_id),
        ("client_secret", &cfg.client_secret),
        ("redirect_uri", &redirect),
        ("grant_type", "authorization_code"),
        ("code_verifier", &verifier),
    ])?;
    let refresh = tokens
        .get("refresh_token")
        .and_then(|x| x.as_str())
        .ok_or_else(|| "refresh_token mancante nella risposta di Google".to_string())?
        .to_string();
    let id_token = tokens
        .get("id_token")
        .and_then(|x| x.as_str())
        .unwrap_or("");
    let email = email_from_id_token(id_token)
        .ok_or_else(|| "email non ricavabile dal token di Google".to_string())?;
    Ok((email, refresh))
}

/// google_refresh exchanges a Google refresh token for (access token, ttl).
pub fn google_refresh(
    app: &tauri::AppHandle,
    refresh_token: &str,
) -> Result<(String, u64), String> {
    let cfg = load_config(app)?;
    let tokens = token_post(&[
        ("client_id", &cfg.client_id),
        ("client_secret", &cfg.client_secret),
        ("grant_type", "refresh_token"),
        ("refresh_token", refresh_token),
    ])?;
    let access = tokens
        .get("access_token")
        .and_then(|x| x.as_str())
        .ok_or_else(|| {
            "access_token mancante nel refresh (token revocato? riautorizza l'account)".to_string()
        })?
        .to_string();
    let ttl = tokens
        .get("expires_in")
        .and_then(|x| x.as_u64())
        .unwrap_or(3600);
    Ok((access, ttl))
}

// --- token lifecycle (used by account_sync / account_send) ---

/// effective_secret maps the stored account secret to the credential the
/// engine needs right now: for OAuth accounts (marker entry oauth/<id>)
/// it returns a fresh access token (refreshing + caching when expired);
/// for password accounts the secret is returned unchanged.
pub fn effective_secret(
    app: &tauri::AppHandle,
    account_id: &str,
    password_secret: &str,
) -> Result<String, String> {
    let marker = format!("oauth/{}", account_id);
    let cached: Value = match crate::secrets::get(&marker)? {
        Some(json) => serde_json::from_str(&json).unwrap_or_else(|_| json!({})),
        None => return Ok(password_secret.to_string()),
    };

    let expiry = cached.get("expiry").and_then(|x| x.as_u64()).unwrap_or(0);
    if now_unix() + 60 < expiry {
        if let Some(access) = cached.get("access").and_then(|x| x.as_str()) {
            if !access.is_empty() {
                return Ok(access.to_string());
            }
        }
    }

    // refresh needed: route by provider (the marker records which OAuth
    // identity the account uses; legacy markers without the field are Google)
    let (access, ttl) =
        if cached.get("provider").and_then(|x| x.as_str()) == Some("microsoft-oauth") {
            microsoft_refresh(app, password_secret)?
        } else {
            google_refresh(app, password_secret)?
        };

    // keep "provider": without it the next refresh of a Microsoft account
    // would be routed to Google and fail
    crate::secrets::set(
        &marker,
        &json!({"access": access, "expiry": now_unix() + ttl, "provider": cached.get("provider")})
            .to_string(),
    )?;
    Ok(access)
}

// --- Microsoft (Outlook) OAuth2: device-code flow ---
//
// No redirect listener needed: the user opens microsoft.com/devicelogin and
// types the code shown in the app. Client credentials come from oauth.json
// (section "microsoft", client_id required, client_secret optional for
// public clients). The REFRESH token is stored as the account secret; the
// oauth/<id> marker caches the short-lived ACCESS token.

const MS_DEVICECODE_URL: &str = "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode";
const MS_TOKEN_URL: &str = "https://login.microsoftonline.com/common/oauth2/v2.0/token";
const MS_SCOPE: &str = "https://graph.microsoft.com/Mail.ReadWrite https://graph.microsoft.com/Mail.Send offline_access openid";
const MS_GRAPH: &str = "https://graph.microsoft.com";

/// MicrosoftConfig carries the app registration credentials (oauth.json).
struct MicrosoftConfig {
    client_id: String,
    client_secret: String,
}

fn load_microsoft_config(app: &tauri::AppHandle) -> Result<MicrosoftConfig, String> {
    let path = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("cartella app non disponibile: {}", e))?
        .join("oauth.json");
    let raw = std::fs::read_to_string(&path).map_err(|_| {
        format!(
            "oauth.json non trovato (atteso: {}) — segui docs/oauth.md per configurare Microsoft OAuth",
            path.display()
        )
    })?;
    let v: Value =
        serde_json::from_str(&raw).map_err(|e| format!("oauth.json non valido: {}", e))?;
    let m = v
        .get("microsoft")
        .ok_or_else(|| "oauth.json: manca la sezione \"microsoft\"".to_string())?;
    Ok(MicrosoftConfig {
        client_id: m
            .get("client_id")
            .and_then(|x| x.as_str())
            .ok_or_else(|| "oauth.json: client_id mancante (sezione microsoft)".to_string())?
            .to_string(),
        client_secret: m
            .get("client_secret")
            .and_then(|x| x.as_str())
            .unwrap_or("")
            .to_string(),
    })
}

/// MicrosoftBegin is what the UI shows while the user authorizes.
pub struct MicrosoftBegin {
    pub device_code: String,
    pub user_code: String,
    pub verification_uri: String,
    pub interval_secs: u64,
}

/// microsoft_begin starts the device-code flow: it returns the code the user
/// must enter and does NOT block.
pub fn microsoft_begin(app: &tauri::AppHandle) -> Result<MicrosoftBegin, String> {
    let cfg = load_microsoft_config(app)?;
    let resp = ureq::post(MS_DEVICECODE_URL)
        .send_form(&[("client_id", &cfg.client_id), ("scope", MS_SCOPE)])
        .map_err(|e| format!("richiesta device code non riuscita: {}", e))?;
    let v = resp
        .into_json::<Value>()
        .map_err(|e| format!("risposta device code non valida: {}", e))?;
    let begin = MicrosoftBegin {
        device_code: v
            .get("device_code")
            .and_then(|x| x.as_str())
            .ok_or_else(|| "device_code mancante".to_string())?
            .to_string(),
        user_code: v
            .get("user_code")
            .and_then(|x| x.as_str())
            .unwrap_or("")
            .to_string(),
        verification_uri: v
            .get("verification_uri")
            .and_then(|x| x.as_str())
            .unwrap_or("https://microsoft.com/devicelogin")
            .to_string(),
        interval_secs: v
            .get("interval")
            .and_then(|x| x.as_u64())
            .unwrap_or(5)
            .max(1),
    };
    // the browser opens immediately: the user lands on devicelogin while
    // the app shows the code to type
    open_url(&begin.verification_uri)?;
    Ok(begin)
}

/// microsoft_wait polls the token endpoint until the user completes (or
/// abandons) the authorization, then returns (email, refresh_token).
pub fn microsoft_wait(
    app: &tauri::AppHandle,
    device_code: &str,
    interval_secs: u64,
) -> Result<(String, String), String> {
    let cfg = load_microsoft_config(app)?;
    let interval = std::time::Duration::from_secs(interval_secs.max(1));
    // hard cap ~10 minutes: after that the device code is stale anyway
    for _ in 0..120 {
        std::thread::sleep(interval);
        let form: Vec<(&str, &str)> = vec![
            ("client_id", &cfg.client_id),
            ("client_secret", &cfg.client_secret),
            ("grant_type", "urn:ietf:params:oauth:grant-type:device_code"),
            ("device_code", device_code),
        ];
        let tokens = match ureq::post(MS_TOKEN_URL).send_form(&form) {
            Ok(resp) => resp
                .into_json::<Value>()
                .map_err(|e| format!("risposta token non valida: {}", e))?,
            Err(ureq::Error::Status(code, resp)) if code == 400 || code == 401 => {
                let err = resp.into_json::<Value>().unwrap_or(json!({}));
                match err.get("error").and_then(|x| x.as_str()).unwrap_or("") {
                    "authorization_pending" => continue,
                    "slow_down" => {
                        std::thread::sleep(interval);
                        continue;
                    }
                    other => return Err(format!("autorizzazione non completata ({})", other)),
                }
            }
            Err(e) => return Err(format!("scambio token non riuscito: {}", e)),
        };
        let refresh = tokens
            .get("refresh_token")
            .and_then(|x| x.as_str())
            .ok_or_else(|| "refresh_token mancante nella risposta di Microsoft".to_string())?
            .to_string();
        let access = tokens
            .get("access_token")
            .and_then(|x| x.as_str())
            .unwrap_or("")
            .to_string();
        // resolve the email from the signed-in account (Graph /me)
        let body = ureq::get(&format!("{}/v1.0/me?$select=userPrincipalName", MS_GRAPH))
            .set("Authorization", &format!("Bearer {}", access))
            .call()
            .map_err(|e| format!("profilo Microsoft non leggibile: {}", e))?;
        let me = body
            .into_json::<Value>()
            .map_err(|e| format!("profilo Microsoft non valido: {}", e))?;
        let email = me
            .get("userPrincipalName")
            .and_then(|x| x.as_str())
            .unwrap_or("")
            .to_string();
        if email.is_empty() {
            return Err("email non ricavabile dal profilo Microsoft".into());
        }
        return Ok((email, refresh));
    }
    Err("autorizzazione non completata entro il tempo limite".into())
}

/// microsoft_refresh exchanges a refresh token for a fresh access token
/// (used by effective_secret when the cached one expired).
pub fn microsoft_refresh(
    app: &tauri::AppHandle,
    refresh_token: &str,
) -> Result<(String, u64), String> {
    let cfg = load_microsoft_config(app)?;
    let tokens = token_post_at(
        MS_TOKEN_URL,
        &[
            ("client_id", &cfg.client_id),
            ("client_secret", &cfg.client_secret),
            ("grant_type", "refresh_token"),
            ("refresh_token", refresh_token),
            ("scope", MS_SCOPE),
        ],
    )?;
    let access = tokens
        .get("access_token")
        .and_then(|x| x.as_str())
        .ok_or_else(|| {
            "access_token mancante nel refresh (token revocato? riautorizza l'account)".to_string()
        })?
        .to_string();
    let ttl = tokens
        .get("expires_in")
        .and_then(|x| x.as_u64())
        .unwrap_or(3600);
    Ok((access, ttl))
}
