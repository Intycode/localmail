// paths — which file paths the webview may hand to file-touching commands.
//
// A path counts only if the user picked it in a native dialog that Rust
// showed (pick_path). Otherwise script injected through a hostile email
// could make attachment_export write ~/.zshrc or a LaunchAgent, or attach
// ~/.ssh/id_rsa to an outgoing mail. Picked files are trusted for the
// session; picked folders (auto-backup target) persist across launches.
use serde::Deserialize;
use serde_json::{json, Value};
use std::collections::HashSet;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use tauri::{AppHandle, Manager};
use tauri_plugin_dialog::DialogExt;

static PICKED_FILES: Mutex<Option<HashSet<PathBuf>>> = Mutex::new(None);

fn dirs_file(app: &AppHandle) -> Option<PathBuf> {
    app.path()
        .app_data_dir()
        .ok()
        .map(|d| d.join("picked_dirs.json"))
}

fn picked_dirs(app: &AppHandle) -> Vec<PathBuf> {
    dirs_file(app)
        .and_then(|f| std::fs::read(f).ok())
        .and_then(|b| serde_json::from_slice(&b).ok())
        .unwrap_or_default()
}

fn remember(app: &AppHandle, picked: &[PathBuf], folder: bool) {
    if folder {
        let mut dirs = picked_dirs(app);
        dirs.extend(picked.iter().cloned());
        dirs.dedup();
        if let (Some(f), Ok(json)) = (dirs_file(app), serde_json::to_vec(&dirs)) {
            let _ = std::fs::write(f, json);
        }
    } else {
        let mut guard = PICKED_FILES.lock().unwrap_or_else(|e| e.into_inner());
        guard.get_or_insert_with(HashSet::new).extend(picked.iter().cloned());
    }
}

/// check accepts a path the user picked (or a file directly inside a picked
/// folder) and rejects anything else.
pub fn check(app: &AppHandle, path: &str) -> Result<(), String> {
    let files = PICKED_FILES.lock().unwrap_or_else(|e| e.into_inner());
    let empty = HashSet::new();
    if allowed(Path::new(path), files.as_ref().unwrap_or(&empty), &picked_dirs(app)) {
        Ok(())
    } else {
        Err("percorso non scelto dall'utente: selezionalo di nuovo dalla finestra di dialogo".into())
    }
}

fn allowed(p: &Path, files: &HashSet<PathBuf>, dirs: &[PathBuf]) -> bool {
    // ".." would escape a picked folder while keeping it as the textual parent
    if p.components().any(|c| c == std::path::Component::ParentDir) {
        return false;
    }
    files.contains(p) || p.parent().is_some_and(|parent| dirs.iter().any(|d| d == parent))
}

/// check_params validates the string params named in keys (when present).
pub fn check_params(app: &AppHandle, params: &Value, keys: &[&str]) -> Result<(), String> {
    for key in keys {
        if let Some(p) = params.get(*key).and_then(|v| v.as_str()) {
            check(app, p)?;
        }
    }
    Ok(())
}

#[derive(Deserialize)]
pub struct Filter {
    name: String,
    extensions: Vec<String>,
}

/// pick_path shows a native dialog and returns { paths: [...] } (empty when
/// cancelled). mode: "save" | "open" | "openMany" | "folder". default_path
/// is a directory, or for "save" a full path whose file name is suggested.
#[tauri::command]
pub async fn pick_path(
    app: AppHandle,
    mode: String,
    default_path: Option<String>,
    title: Option<String>,
    filters: Option<Vec<Filter>>,
) -> Result<Value, ()> {
    let result = tauri::async_runtime::spawn_blocking(move || {
        let mut dialog = app.dialog().file();
        if let Some(t) = title {
            dialog = dialog.set_title(t);
        }
        for f in filters.unwrap_or_default() {
            let exts: Vec<&str> = f.extensions.iter().map(String::as_str).collect();
            dialog = dialog.add_filter(f.name, &exts);
        }
        if let Some(d) = default_path {
            let d = PathBuf::from(d);
            if mode == "save" && !d.is_dir() {
                if let Some(dir) = d.parent().filter(|p| p.is_dir()) {
                    dialog = dialog.set_directory(dir);
                }
                if let Some(name) = d.file_name() {
                    dialog = dialog.set_file_name(name.to_string_lossy());
                }
            } else {
                dialog = dialog.set_directory(d);
            }
        }
        let picked: Vec<PathBuf> = match mode.as_str() {
            "save" => dialog.blocking_save_file().into_iter().collect(),
            "open" => dialog.blocking_pick_file().into_iter().collect(),
            "openMany" => dialog.blocking_pick_files().unwrap_or_default(),
            "folder" => dialog.blocking_pick_folder().into_iter().collect(),
            other => return Err(format!("modalità dialog sconosciuta: {}", other)),
        }
        .into_iter()
        .filter_map(|p| p.into_path().ok())
        .collect();
        remember(&app, &picked, mode == "folder");
        Ok(json!({ "paths": picked }))
    })
    .await
    .unwrap_or_else(|e| Err(format!("dialog non riuscito: {}", e)));
    Ok(crate::engine::envelope(result))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_picked_paths_pass() {
        let files: HashSet<PathBuf> = [PathBuf::from("/u/Downloads/a.pdf")].into();
        let dirs = vec![PathBuf::from("/u/Backups")];
        assert!(allowed(Path::new("/u/Downloads/a.pdf"), &files, &dirs));
        assert!(allowed(Path::new("/u/Backups/mail-backup-2026-10-30.db"), &files, &dirs));
        assert!(!allowed(Path::new("/u/.zshrc"), &files, &dirs));
        assert!(!allowed(Path::new("/u/Backups/sub/x.db"), &files, &dirs));
        assert!(!allowed(Path::new("/u/Backups/../.zshrc"), &files, &dirs));
    }
}
