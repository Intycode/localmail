// Tauri is intentionally a THIN native layer.
//
// Rule: no email business logic here. Business logic lives in the Go mail
// engine (backend/) and the UI lives in Astro (apps/client). This crate only:
//  1. opens the app window
//  2. hosts the webview (Astro static build)
//  3. bridges IPC between the UI and the Go engine (future phases)

// windows-subsystem in release: l'exe non alloca una console all'avvio
// (doppio click = solo la finestra dell'app, nessun prompt dei comandi).
// In debug la console resta per i log.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    mail_lib::run();
}
