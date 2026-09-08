/**
 * lib/i18n — minimal reactive i18n for the UI (Italian + English).
 *
 * Design:
 * - Two flat dictionaries (`dictionaries/it.ts`, `dictionaries/en.ts`) with
 *   dot-namespaced keys; Italian is the reference language and is treated
 *   like any other (no inline-Italian fallbacks anywhere).
 * - `t(key, params)` interpolates `{name}` placeholders; a missing key
 *   renders as the key itself (loud, never a blank UI).
 * - The locale is persisted under `mail.setting.locale`; with no saved
 *   choice the system language is used (`navigator.languages`).
 * - Reactivity: `setLocale` notifies subscribers; `useT()` is the render
 *   hook (useSyncExternalStore) so every mounted component re-renders on
 *   language change. Plain `t()` is fine for event-driven strings (toasts,
 *   validation, notifications) which need no reactivity.
 */

import * as React from "react"

import { en } from "./dictionaries/en"
import { it } from "./dictionaries/it"

export type Locale = "it" | "en"

export const LOCALES: readonly Locale[] = ["it", "en"]

const STORAGE_KEY = "mail.setting.locale"

const DICTS: Record<Locale, Record<string, string>> = { it, en }

export type TParams = Record<string, string | number>
export type TFn = (key: string, params?: TParams) => string

// --- locale state -----------------------------------------------------

const listeners = new Set<() => void>()
let cached: Locale | null = null

/** Best-effort system language: the first `it`/`en` in navigator.languages,
 *  defaulting to Italian (the app's reference language). */
function systemLocale(): Locale {
  if (typeof navigator === "undefined") return "it"
  for (const lang of navigator.languages ?? [navigator.language]) {
    const base = (lang ?? "").toLowerCase().split("-")[0]
    if (base === "en") return "en"
    if (base === "it") return "it"
  }
  return "it"
}

/** Persisted locale, falling back to the system language (SSR-safe: "it"). */
export function getLocale(): Locale {
  if (cached) return cached
  try {
    const saved = window?.localStorage.getItem(STORAGE_KEY)
    if (saved === "it" || saved === "en") {
      cached = saved
      return saved
    }
  } catch {
    // non-browser runtime: system default
  }
  return systemLocale()
}

/** Drops the memoized locale so the next getLocale() re-reads storage (tests). */
export function resetLocaleCache(): void {
  cached = null
}

/** Persist + apply + notify. */
export function setLocale(locale: Locale): void {
  cached = locale
  try {
    window?.localStorage.setItem(STORAGE_KEY, locale)
  } catch {
    // non-browser runtime: module state only
  }
  applyDocumentLang()
  for (const fn of listeners) fn()
}

function applyDocumentLang(): void {
  try {
    document.documentElement.lang = getLocale()
  } catch {
    // non-browser runtime: nothing to update
  }
}

/** Subscribe to locale changes (also fires from other tabs via storage). */
export function subscribeLocale(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/**
 * Keep the locale in sync across tabs/windows: a `storage` event invalidates
 * the cache and notifies subscribers. Returns a cleanup function (mirrors
 * lib/theme's watchThemeMode; AppShell calls it once at mount).
 */
export function watchLocale(): () => void {
  if (typeof window === "undefined") return () => {}
  const onStorage = (e: StorageEvent): void => {
    if (e.key !== STORAGE_KEY && e.key !== null) return
    const next = getLocale()
    if (next !== cached) {
      cached = next
      applyDocumentLang()
      for (const fn of listeners) fn()
    }
  }
  window.addEventListener("storage", onStorage)
  return () => window.removeEventListener("storage", onStorage)
}

// --- translation ------------------------------------------------------

/** Translate `key` for the active locale, interpolating `{param}` slots.
 *  A missing key renders as the key itself (never silently blank). */
export const t: TFn = (key, params) => {
  let s = DICTS[getLocale()][key] ?? key
  if (params) {
    for (const [name, value] of Object.entries(params)) {
      s = s.split("{" + name + "}").join(String(value))
    }
  }
  return s
}

/** BCP 47 tag for date/number formatting (`toLocaleString` et al.). */
export function dateLocale(locale = getLocale()): string {
  return locale === "en" ? "en-US" : "it-IT"
}

// --- React bindings ---------------------------------------------------

const CLIENT_SNAPSHOT = (): Locale => getLocale()
const SERVER_SNAPSHOT = (): Locale => "it"

/** Render hook: returns `t` bound to the active locale. Any mounted
 *  component using it re-renders when the language changes. */
export function useT(): TFn {
  React.useSyncExternalStore(subscribeLocale, CLIENT_SNAPSHOT, SERVER_SNAPSHOT)
  return t
}
