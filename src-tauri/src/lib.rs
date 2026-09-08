// Thin native layer: window hosting + IPC bridge to the Go engine.
//
// This crate stays deliberately thin (no email business logic):
//   1. opens the app window and hosts the Astro static build,
//   2. spawns the Go engine process and bridges its JSON-lines IPC
//      (engine.rs), forwarding events to the webview as "mail://event",
//   3. stores account secrets in the OS keychain (secrets.rs).
use tauri::Manager;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_notification::init())
        .setup(|app| {
            let data_dir = app
                .path()
                .app_data_dir()
                .expect("app data dir non disponibile");
            std::fs::create_dir_all(&data_dir).expect("creazione app data dir");
            let engine = engine::Engine::new(app.handle().clone(), data_dir.join("mail.db"));
            app.manage(std::sync::Arc::new(engine));

            // The title bar is drawn by the webview (TitleBar.tsx): make sure
            // the native frame is gone at runtime too, not just via the
            // window config (which is embedded at compile time).
            let window = app
                .get_webview_window("main")
                .expect("main window non trovata");
            window
                .set_decorations(false)
                .expect("impossibile rimuovere le decorazioni");
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            engine::engine_invoke,
            engine::account_add,
            engine::account_sync,
            engine::account_send,
            engine::attachment_export,
            engine::attachment_open,
            engine::attachment_download,
            engine::attachment_text,
            engine::attachment_images,
            engine::attachment_preview,
            engine::account_remove,
            oauth_google_start,
            oauth_microsoft_begin,
            oauth_microsoft_wait,
            engine::secrets_restore,
            pgp::pgp_generate,
            pgp::pgp_import_private,
            pgp::pgp_delete,
            engine::backup_create,
            engine::backup_restore
        ])
        .build(tauri::generate_context!())
        .expect("failed to build Tauri app");

    app.run(|_app_handle, _event| {});
}

mod engine;
mod oauth;
mod pgp;
mod secrets;

#[tauri::command]
fn oauth_microsoft_begin(app: tauri::AppHandle) -> serde_json::Value {
    match oauth::microsoft_begin(&app) {
        Ok(b) => engine::envelope(Ok(serde_json::json!({
            "deviceCode": b.device_code,
            "userCode": b.user_code,
            "verificationUri": b.verification_uri,
            "intervalSecs": b.interval_secs
        }))),
        Err(e) => engine::envelope(Err(e)),
    }
}

#[tauri::command]
fn oauth_microsoft_wait(
    app: tauri::AppHandle,
    device_code: String,
    interval_secs: u64,
) -> serde_json::Value {
    match oauth::microsoft_wait(&app, &device_code, interval_secs) {
        Ok((email, refresh_token)) => engine::envelope(Ok(
            serde_json::json!({ "email": email, "refreshToken": refresh_token }),
        )),
        Err(e) => engine::envelope(Err(e)),
    }
}

#[tauri::command]
fn oauth_google_start(app: tauri::AppHandle) -> serde_json::Value {
    match oauth::google_start(&app) {
        Ok((email, refresh_token)) => engine::envelope(Ok(
            serde_json::json!({ "email": email, "refreshToken": refresh_token }),
        )),
        Err(e) => engine::envelope(Err(e)),
    }
}
