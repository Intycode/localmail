// secrets — OS keychain access for account passwords/app-passwords.
//
// Rule: secrets live ONLY in the keychain (service "dev.mail.client"). They
// are never logged, never sent to the webview, never persisted by the Go
// engine; Tauri injects them into engine requests at runtime.
//
// macOS asks "allow access?" once PER keychain item, so there every secret
// (db key, account passwords, OAuth tokens, PGP keys) lives in ONE item
// ("vault"): one prompt per launch instead of one per item. Windows caps a
// credential at 2.5 KB and Linux unlocks the whole collection at once, so
// there each name keeps its own entry. Either way each value is read from the
// OS at most once per process (CACHE).
use keyring::Entry;
use std::collections::HashMap;
use std::sync::{Mutex, MutexGuard};

const SERVICE: &str = "dev.mail.client";
const ONE_ITEM: bool = cfg!(target_os = "macos");
const VAULT: &str = "vault";

static CACHE: Mutex<Option<HashMap<String, String>>> = Mutex::new(None);

fn os_entry(name: &str) -> Result<Entry, String> {
    Entry::new(SERVICE, name).map_err(|e| format!("keychain non disponibile: {}", e))
}

fn os_get(name: &str) -> Result<Option<String>, String> {
    match os_entry(name)?.get_password() {
        Ok(v) => Ok(Some(v)),
        Err(keyring::Error::NoEntry) => Ok(None),
        Err(e) => Err(format!("lettura keychain non riuscita: {}", e)),
    }
}

fn os_delete(name: &str) -> Result<(), String> {
    match os_entry(name)?.delete_credential() {
        Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
        Err(e) => Err(format!("cancellazione keychain non riuscita: {}", e)),
    }
}

/// cache locks the in-process copy, loading the vault item on first use.
/// The lock is held across OS calls on purpose: concurrent first reads wait
/// for one prompt instead of raising several.
fn cache() -> Result<MutexGuard<'static, Option<HashMap<String, String>>>, String> {
    let mut guard = CACHE.lock().unwrap_or_else(|e| e.into_inner());
    if guard.is_none() {
        let map = match ONE_ITEM.then(|| os_get(VAULT)).transpose()?.flatten() {
            // a corrupt vault must fail loudly: rewriting it would drop the db key
            Some(json) => serde_json::from_str(&json)
                .map_err(|e| format!("vault keychain illeggibile: {}", e))?,
            None => HashMap::new(),
        };
        *guard = Some(map);
    }
    Ok(guard)
}

fn persist(map: &HashMap<String, String>) -> Result<(), String> {
    let json = serde_json::to_string(map).map_err(|e| e.to_string())?;
    os_entry(VAULT)?
        .set_password(&json)
        .map_err(|e| format!("scrittura keychain non riuscita: {}", e))
}

/// get reads one named secret. On macOS a name missing from the vault may
/// still be a pre-vault item: it is moved into the vault once.
pub(crate) fn get(name: &str) -> Result<Option<String>, String> {
    let mut guard = cache()?;
    let map = guard.as_mut().expect("cache loaded");
    if let Some(v) = map.get(name) {
        return Ok(Some(v.clone()));
    }
    let Some(v) = os_get(name)? else {
        return Ok(None);
    };
    map.insert(name.to_string(), v.clone());
    // migrate best effort: if the vault write fails the legacy item stays usable
    if ONE_ITEM && persist(map).is_ok() {
        let _ = os_delete(name); // the legacy item would prompt again
    }
    Ok(Some(v))
}

pub(crate) fn set(name: &str, value: &str) -> Result<(), String> {
    let mut guard = cache()?;
    let map = guard.as_mut().expect("cache loaded");
    let old = map.insert(name.to_string(), value.to_string());
    let saved = if ONE_ITEM {
        persist(map)
    } else {
        os_entry(name)?
            .set_password(value)
            .map_err(|e| format!("scrittura keychain non riuscita: {}", e))
    };
    if saved.is_err() {
        // keep the cache equal to what the OS holds
        match old {
            Some(o) => map.insert(name.to_string(), o),
            None => map.remove(name),
        };
    }
    saved
}

pub(crate) fn delete(name: &str) -> Result<(), String> {
    let mut guard = cache()?;
    let map = guard.as_mut().expect("cache loaded");
    if map.remove(name).is_some() && ONE_ITEM {
        persist(map)?;
    }
    os_delete(name) // per-item entry, or a never-migrated legacy one
}

pub fn set_secret(account_id: &str, secret: &str) -> Result<(), String> {
    set(&format!("account/{}", account_id), secret)
}

pub fn get_secret(account_id: &str) -> Result<String, String> {
    get(&format!("account/{}", account_id))?.ok_or_else(|| {
        format!(
            "credenziale non trovata nel keychain per l'account {}",
            account_id
        )
    })
}

pub fn delete_secret(account_id: &str) -> Result<(), String> {
    delete(&format!("account/{}", account_id))
}

/// db_key — the local database encryption key (32 random bytes, hex).
/// Generated on first run and stored in the keychain under "db/master";
/// Tauri hands it to the engine process via the MAIL_DB_KEY env var at
/// spawn. Losing this keychain entry means losing access to the local
/// database: it is encrypted at rest by design.
///
/// encrypted_db_exists: an encrypted database file is already on disk. A
/// missing key is then an error, never a reason to mint a new key: that
/// would leave the data unreadable (and the engine would archive the file).
/// The user restores the key from the recovery key file instead.
pub fn db_key(encrypted_db_exists: bool) -> Result<String, String> {
    match get("db/master")? {
        Some(k) if !k.is_empty() => Ok(k),
        _ if encrypted_db_exists => Err(
            "chiave del database non trovata nel portachiavi: importa la chiave di recupero (Impostazioni → Backup)"
                .into(),
        ),
        // missing (first run) or empty: generate and store
        _ => {
            use rand::RngCore;
            let mut bytes = [0u8; 32];
            rand::thread_rng().fill_bytes(&mut bytes);
            let hex: String = bytes.iter().map(|b| format!("{:02x}", b)).collect();
            set("db/master", &hex)?;
            Ok(hex)
        }
    }
}

/// pgp_get/pgp_set/pgp_delete — keychain entries for OpenPGP private keyrings
/// (pgp/<account_id>, M9). Same trust model as account passwords: the webview
/// never touches them directly, Rust mediates every access.
pub fn pgp_get(account_id: &str) -> Result<String, String> {
    get(&format!("pgp/{}", account_id))?
        .ok_or_else(|| "chiave PGP non trovata nel keychain".to_string())
}

pub fn pgp_set(account_id: &str, armored: &str) -> Result<(), String> {
    set(&format!("pgp/{}", account_id), armored)
}

pub fn pgp_delete(account_id: &str) -> Result<(), String> {
    delete(&format!("pgp/{}", account_id))
}
