/**
 * lib/updater — on app start, checks GitHub Releases (latest.json, see
 * tauri.conf.json → plugins.updater) for a newer signed build and, if the
 * user agrees, downloads, installs and relaunches. Silent on failure
 * (offline, endpoint down): retried on the next start.
 */
import { isTauri } from "../services/tauriMailService"
import { t } from "./i18n"

const CHECKED_KEY = "updater.checked"

export async function maybeCheckForUpdate(): Promise<void> {
  if (!isTauri()) return
  // AppShell mounts on every page: check once per app session
  try {
    if (sessionStorage.getItem(CHECKED_KEY)) return
    sessionStorage.setItem(CHECKED_KEY, "1")
  } catch {
    /* storage unavailable: just check */
  }
  try {
    const [{ check }, { ask }, { relaunch }] = await Promise.all([
      import("@tauri-apps/plugin-updater"),
      import("@tauri-apps/plugin-dialog"),
      import("@tauri-apps/plugin-process"),
    ])
    const update = await check()
    if (!update) return
    const go = await ask(t("updater.prompt", { version: update.version }), {
      title: t("updater.title"),
      okLabel: t("updater.install"),
      cancelLabel: t("updater.later"),
    })
    if (!go) return
    await update.downloadAndInstall()
    await relaunch()
  } catch {
    // silent: retried on the next start
  }
}
