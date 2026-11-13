/** Small presentation helpers shared by the UI. Dates are formatted with
 *  the active UI locale (lib/i18n); labels come from the dictionaries. */

import { dateLocale, t } from "./i18n"

export function formatDate(iso: string): string {
  const d = new Date(iso)
  const now = new Date()
  const isToday = d.toDateString() === now.toDateString()
  if (isToday) return d.toLocaleTimeString(dateLocale(), { hour: "2-digit", minute: "2-digit" })
  const isYear = d.getFullYear() === now.getFullYear()
  return d.toLocaleDateString(
    dateLocale(),
    isYear
      ? { month: "short", day: "numeric" }
      : { year: "numeric", month: "short", day: "numeric" },
  )
}

/**
 * dayLabel — group label for the message list: "Oggi"/"Today", "Ieri"/
 * "Yesterday", the weekday for the last seven days, otherwise a full date.
 * Deterministic via `now`.
 */
export function dayLabel(iso: string, now = new Date()): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const sameDay = (a: Date, b: Date): boolean =>
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  if (sameDay(d, now)) return t("time.today")
  const yesterday = new Date(now)
  yesterday.setDate(yesterday.getDate() - 1)
  if (sameDay(d, yesterday)) return t("time.yesterday")
  const age = now.getTime() - d.getTime()
  if (age > 0 && age < 7 * 86_400_000) {
    const weekday = d.toLocaleDateString(dateLocale(), { weekday: "long" })
    return weekday.charAt(0).toUpperCase() + weekday.slice(1)
  }
  const sameYear = d.getFullYear() === now.getFullYear()
  return d.toLocaleDateString(
    dateLocale(),
    sameYear
      ? { day: "numeric", month: "long" }
      : { day: "numeric", month: "long", year: "numeric" },
  )
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

export function initials(name: string): string {
  const trimmed = name.trim()
  if (trimmed === "") return "?"
  const parts = trimmed.split(/\s+/)
  if (parts.length === 1) return parts[0].slice(0, 1).toUpperCase()
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase()
}

export function shortSender(thread: { from: { name: string; email: string } }): string {
  return thread.from.name || thread.from.email.split("@")[0]
}
/**
 * formatFolderName — display name for IMAP folders. IMAP's canonical inbox
 * name is all-caps "INBOX" and some servers use all-caps names ("DRAFTS").
 * Canonical folder names (inbox, sent, drafts, archive, trash, junk,
 * snoozed — case-insensitive) are translated via the dictionaries; the rest
 * fall back to natural casing without touching the underlying folder id.
 */
const CANONICAL_FOLDER_KEYS: Record<string, string> = {
  inbox: "folder.inbox",
  sent: "folder.sent",
  drafts: "folder.drafts",
  archive: "folder.archive",
  trash: "folder.trash",
  junk: "folder.junk",
  snoozed: "folder.snoozed",
}

export function formatFolderName(raw: string): string {
  // servers with an "INBOX." personal namespace (Courier, cPanel, …) nest
  // every folder under the inbox: "INBOX.Sent" shows as "Sent". Display only:
  // the full name stays in imap_name for the server.
  const name = raw.replace(/^INBOX[./](?=.)/i, "")
  const canonical = CANONICAL_FOLDER_KEYS[name.toLowerCase()]
  if (canonical) return t(canonical)
  if (name.length > 1 && name === name.toUpperCase() && /^[A-Z0-9 ]+$/.test(name)) {
    return name
      .toLowerCase()
      .replace(/(^|\s)([a-z])/g, (_m, sep: string, c: string) => sep + c.toUpperCase())
  }
  return name
}

/**
 * formatProvider — friendly label for an account's provider kind. The raw
 * internal id ("generic-imap") is technical jargon; present it as "Imap".
 * Unknown kinds fall back to the raw value.
 */
export function formatProvider(provider: string): string {
  if (provider === "generic-imap") return "Imap"
  if (provider === "gmail" || provider === "gmail-oauth") return "Gmail"
  if (provider === "microsoft" || provider === "microsoft-oauth") return "Microsoft"
  return provider
}
