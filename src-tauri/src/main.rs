// Tauri is intentionally a THIN native layer.
//
// Rule: no email business logic here. Business logic lives in the Go mail
// engine (backend/) and the UI lives in Astro (apps/client). This crate only:
//  1. opens the app window
//  2. hosts the webview (Astro static build)
//  3. bridges IPC between the UI and the Go engine (future phases)
fn main() {
    mail_lib::run();
}
