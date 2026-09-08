// pgp — OpenPGP keychain bridge (M9).
//
// Private keyrings live ONLY in the OS keychain (service dev.mail.client,
// entries pgp/<account_id>). The engine generates them in its runtime vault;
// this module persists the armored private key and re-delivers it to the
// engine at spawn/restart, exactly like account passwords. The webview never
// sees private key material: Rust strips it from engine responses.
use serde_json::json;

use crate::engine::{envelope, run_engine, Engine, DEFAULT_TIMEOUT};

/// pgp_redeliver pushes every pgp/<id> keychain entry into the engine vault.
pub fn pgp_redeliver(engine: &Engine) -> usize {
    let accounts = match engine.invoke("accounts.list", json!({}), DEFAULT_TIMEOUT) {
        Ok(v) => v,
        Err(_) => return 0,
    };
    let mut secrets: Vec<serde_json::Value> = Vec::new();
    if let Some(list) = accounts.as_array() {
        for acc in list {
            if let Some(id) = acc.get("id").and_then(|v| v.as_str()) {
                if let Ok(key) = crate::secrets::pgp_get(id) {
                    secrets.push(json!({"accountId": format!("pgp/{}", id), "secret": key}));
                }
            }
        }
    }
    let restored = secrets.len();
    let _ = engine.invoke(
        "secrets.restore",
        json!({ "secrets": secrets }),
        DEFAULT_TIMEOUT,
    );
    restored
}

/// pgp_generate creates the account keypair via the engine (vault), stores
/// the private armor in the keychain and returns ONLY the public half +
/// fingerprint to the webview.
#[tauri::command]
pub async fn pgp_generate(
    state: tauri::State<'_, std::sync::Arc<Engine>>,
    account_id: String,
    name: String,
    email: String,
) -> Result<serde_json::Value, ()> {
    Ok(run_engine(state, move |engine| {
        let data = engine.invoke(
            "pgp.generate",
            json!({"accountId": account_id, "name": name, "email": email}),
            crate::engine::DEFAULT_TIMEOUT,
        )?;
        let public = data.get("publicKey").cloned().unwrap_or(json!(""));
        let fingerprint = data
            .get("fingerprint")
            .and_then(|v| v.as_str())
            .unwrap_or("")
            .to_string();
        let _ = pgp_redeliver(engine);
        Ok(json!({"publicKey": public, "fingerprint": fingerprint}))
    })
    .await)
}

/// pgp_import_private validates an armored private keyring pasted by the
/// user (the webview already holds it), stores it in the keychain and
/// re-delivers it to the engine vault. Returns the fingerprint.
#[tauri::command]
pub async fn pgp_import_private(
    state: tauri::State<'_, std::sync::Arc<Engine>>,
    account_id: String,
    armored: String,
) -> Result<serde_json::Value, ()> {
    if let Err(e) = crate::secrets::pgp_set(&account_id, &armored) {
        return Ok(envelope(Err(e)));
    }
    Ok(run_engine(state, move |engine| {
        engine.invoke(
            "pgp.importPrivate",
            json!({"accountId": account_id, "armored": armored}),
            crate::engine::DEFAULT_TIMEOUT,
        )?;
        let _ = pgp_redeliver(engine);
        Ok(json!({"ok": true}))
    })
    .await)
}

/// pgp_delete removes the keychain entry and wipes the engine vault.
#[tauri::command]
pub async fn pgp_delete(
    state: tauri::State<'_, std::sync::Arc<Engine>>,
    account_id: String,
) -> Result<serde_json::Value, ()> {
    let _ = crate::secrets::pgp_delete(&account_id);
    Ok(run_engine(state, move |engine| {
        engine.invoke(
            "pgp.forget",
            json!({"accountId": account_id}),
            crate::engine::DEFAULT_TIMEOUT,
        )
    })
    .await)
}
