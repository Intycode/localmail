/**
 * lib/settings — persisted UI settings (localStorage, dev only).
 * Real settings will flow through Tauri -> Go in the shipped app.
 * Toggles live under `mail.setting.<key>` as "true"/"false"; the theme
 * lives in lib/theme.ts under the same prefix.
 */

const PREFIX = "mail.setting."

export function getSetting(key: string, fallback: boolean): boolean {
  if (typeof window === "undefined") return fallback
  const saved = window.localStorage.getItem(PREFIX + key)
  return saved === null ? fallback : saved === "true"
}

export function setSetting(key: string, value: boolean): void {
  window.localStorage.setItem(PREFIX + key, String(value))
}

/** Keyboard shortcuts enabled? (Settings → Tastiera) */
export function keyboardEnabled(): boolean {
  return getSetting("keyboard", true)
}

/** Message body on a white sheet? HTML emails are designed for white. */
export function whiteMessageBackground(): boolean {
  return getSetting("message-white-bg", true)
}

export function setWhiteMessageBackground(value: boolean): void {
  setSetting("message-white-bg", value)
}

/** Undo-send window in seconds (0 = send immediately). */
export function sendUndoSeconds(): number {
  if (typeof window === "undefined") return 0
  const v = Number(window.localStorage.getItem(PREFIX + "send-undo-sec"))
  return Number.isFinite(v) && v > 0 ? v : 0
}

export function setSendUndoSeconds(seconds: number): void {
  window.localStorage.setItem(PREFIX + "send-undo-sec", String(seconds))
}

/** String-valued settings (the toggles above are booleans; the auto-backup
 *  needs a folder path and a last-run timestamp). */
export function getSettingString(key: string, fallback = ""): string {
  if (typeof window === "undefined") return fallback
  return window.localStorage.getItem(PREFIX + key) ?? fallback
}

export function setSettingString(key: string, value: string): void {
  window.localStorage.setItem(PREFIX + key, value)
}

/** Auto-sync interval in minutes (default 15); 0 = only at startup. */
export function syncIntervalMinutes(): number {
  return Number(getSettingString("sync-interval", "15")) || 0
}

export function setSyncIntervalMinutes(min: number): void {
  setSettingString("sync-interval", String(min))
}

/** Directory for the automatic daily backup (empty = not configured). */
export function autoBackupDir(): string {
  return getSettingString("autobackup-dir")
}

export function setAutoBackupDir(dir: string): void {
  setSettingString("autobackup-dir", dir)
}

/** ISO timestamp of the last automatic backup (empty = never). */
export function autoBackupLastRun(): string {
  return getSettingString("autobackup-last")
}

/** First-run onboarding wizard: shown once, when no account is connected. */
export function onboardingDone(): boolean {
  return getSetting("onboarding-done", false)
}

export function setOnboardingDone(): void {
  setSetting("onboarding-done", true)
}

export function setAutoBackupLastRun(iso: string): void {
  setSettingString("autobackup-last", iso)
}
