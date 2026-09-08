/**
 * lib/theme — theme mode (System / Light / Dark).
 *
 * Dark mode is class-based: `.dark` on <html> flips every shadcn token
 * (see styles/global.css). The mode is persisted in localStorage under
 * `mail.setting.theme` and applied:
 *  - pre-paint by the inline bootstrap script in BaseLayout (no FOUC),
 *  - immediately from the Settings page via setThemeMode().
 * In "system" mode the OS preference is followed live.
 */

export type ThemeMode = "system" | "light" | "dark"

export const THEME_STORAGE_KEY = "mail.setting.theme"

export function isThemeMode(value: string | null): value is ThemeMode {
  return value === "system" || value === "light" || value === "dark"
}

export function getThemeMode(): ThemeMode {
  if (typeof window === "undefined") return "system"
  const stored = window.localStorage.getItem(THEME_STORAGE_KEY)
  return isThemeMode(stored) ? stored : "system"
}

function systemPrefersDark(): boolean {
  return window.matchMedia("(prefers-color-scheme: dark)").matches
}

/** Apply a mode to the document (adds/removes `.dark`). */
export function applyThemeMode(mode: ThemeMode): void {
  const dark = mode === "dark" || (mode === "system" && systemPrefersDark())
  document.documentElement.classList.toggle("dark", dark)
  document.documentElement.style.colorScheme = dark ? "dark" : "light"
}

/** Persist + apply a mode. Returns the applied mode. */
export function setThemeMode(mode: ThemeMode): ThemeMode {
  window.localStorage.setItem(THEME_STORAGE_KEY, mode)
  applyThemeMode(mode)
  return mode
}

/**
 * Keep the document in sync: in "system" mode follow live OS changes.
 * Returns a cleanup function.
 */
export function watchThemeMode(): () => void {
  const media = window.matchMedia("(prefers-color-scheme: dark)")
  const onChange = () => {
    if (getThemeMode() === "system") applyThemeMode("system")
  }
  media.addEventListener("change", onChange)
  window.addEventListener("storage", onChange)
  return () => {
    media.removeEventListener("change", onChange)
    window.removeEventListener("storage", onChange)
  }
}
