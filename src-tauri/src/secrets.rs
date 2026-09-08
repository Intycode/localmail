// secrets — OS keychain access for account passwords/app-passwords.
//
// Rule: secrets live ONLY in the keychain (service "dev.mail.client"). They
// are never logged, never sent to the webview, never persisted by the Go
// engine; Tauri injects them into engine requests at runtime.
use keyring::Entry;

const SERVICE: &str = "dev.mail.client";

fn entry(account_id: &str) -> Result<Entry, String> {
    Entry::new(SERVICE, &format!("account/{}", account_id))
        .map_err(|e| format!("keychain non disponibile: {}", e))
}

/// entry_for exposes a named keychain entry (used by the OAuth module for
/// the oauth/<id> marker + access-token cache).
pub(crate) fn entry_for(name: &str) -> Result<Entry, String> {
    Entry::new(SERVICE, name).map_err(|e| format!("keychain non disponibile: {}", e))
}

pub fn set_secret(account_id: &str, secret: &str) -> Result<(), String> {
    entry(account_id)?
        .set_password(secret)
        .map_err(|e| format!("scrittura keychain non riuscita: {}", e))
}

pub fn get_secret(account_id: &str) -> Result<String, String> {
    entry(account_id)?.get_password().map_err(|_| {
        format!(
            "credenziale non trovata nel keychain per l'account {}",
            account_id
        )
    })
}

pub fn delete_secret(account_id: &str) -> Result<(), String> {
    match entry(account_id)?.delete_credential() {
        Ok(()) => Ok(()),
        Err(keyring::Error::NoEntry) => Ok(()), // already gone
        Err(e) => Err(format!("cancellazione keychain non riuscita: {}", e)),
    }
}

/// db_key — the local database encryption key (32 random bytes, hex).
/// Generated on first run and stored in the keychain under "db/master";
/// Tauri hands it to the engine process via the MAIL_DB_KEY env var at
/// spawn. Losing this keychain entry means losing access to the local
/// database: it is encrypted at rest by design.
pub fn db_key() -> Result<String, String> {
    let entry =
        Entry::new(SERVICE, "db/master").map_err(|e| format!("keychain non disponibile: {}", e))?;
    match entry.get_password() {
        Ok(k) if !k.is_empty() => Ok(k),
        // missing (first run) or empty: generate and store
        Ok(_) | Err(keyring::Error::NoEntry) => {
            use rand::RngCore;
            let mut bytes = [0u8; 32];
            rand::thread_rng().fill_bytes(&mut bytes);
            let hex: String = bytes.iter().map(|b| format!("{:02x}", b)).collect();
            entry
                .set_password(&hex)
                .map_err(|e| format!("scrittura keychain non riuscita: {}", e))?;
            Ok(hex)
        }
        Err(e) => Err(format!("lettura keychain non riuscita: {}", e)),
    }
}

/// pgp_get/pgp_set/pgp_delete — keychain entries for OpenPGP private keyrings
/// (pgp/<account_id>, M9). Same trust model as account passwords: the webview
/// never touches them directly, Rust mediates every access.
pub fn pgp_get(account_id: &str) -> Result<String, String> {
    Entry::new(SERVICE, &format!("pgp/{}", account_id))
        .map_err(|e| format!("keychain non disponibile: {}", e))?
        .get_password()
        .map_err(|e| format!("chiave PGP non trovata nel keychain: {}", e))
}

pub fn pgp_set(account_id: &str, armored: &str) -> Result<(), String> {
    Entry::new(SERVICE, &format!("pgp/{}", account_id))
        .map_err(|e| format!("keychain non disponibile: {}", e))?
        .set_password(armored)
        .map_err(|e| format!("scrittura keychain non riuscita: {}", e))
}

pub fn pgp_delete(account_id: &str) -> Result<(), String> {
    match Entry::new(SERVICE, &format!("pgp/{}", account_id))
        .map_err(|e| format!("keychain non disponibile: {}", e))?
        .delete_credential()
    {
        Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
        Err(e) => Err(format!("cancellazione keychain non riuscita: {}", e)),
    }
}
