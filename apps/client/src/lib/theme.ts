/**
 * lib/theme — theme mode (System / Light / Dark).
 *
 * Dark mode is class-based: `.dark` on <html> flips every shadcn token
 * (see styles/global.css). The mode is persisted in localStorage under
 * `mail.setting.theme` and applied:
 *  - pre-paint by the inline bootstrap script in BaseLayout (no FOUC),
 *  - immediately from the Settings page via setThemeMode().
 * In "system" mode the OS preference is followed live.
 *
 * The accent color is a second, independent choice: `data-accent` on <html>
 * swaps the --brand tokens in global.css (absent = "ocean", the app-icon blue).
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

export const ACCENTS = ["ocean", "cyan", "indigo", "violet", "rose", "graphite"] as const
export type Accent = (typeof ACCENTS)[number]

export const ACCENT_STORAGE_KEY = "mail.setting.accent"

export function getAccent(): Accent {
  if (typeof window === "undefined") return "ocean"
  const stored = window.localStorage.getItem(ACCENT_STORAGE_KEY)
  return (ACCENTS as readonly string[]).includes(stored ?? "") ? (stored as Accent) : "ocean"
}

export function applyAccent(accent: Accent): void {
  if (accent === "ocean") delete document.documentElement.dataset.accent
  else document.documentElement.dataset.accent = accent
}

/** Persist + apply an accent. */
export function setAccent(accent: Accent): Accent {
  window.localStorage.setItem(ACCENT_STORAGE_KEY, accent)
  applyAccent(accent)
  return accent
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
  // Another window changed a setting: re-apply both (applying is idempotent).
  const onStorage = () => {
    applyThemeMode(getThemeMode())
    applyAccent(getAccent())
  }
  media.addEventListener("change", onChange)
  window.addEventListener("storage", onStorage)
  return () => {
    media.removeEventListener("change", onChange)
    window.removeEventListener("storage", onStorage)
  }
}
