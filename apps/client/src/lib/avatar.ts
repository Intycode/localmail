/**
 * lib/avatar — per-account avatar personalization (accent color + icon),
 * stored in localStorage under `mail.avatar-prefs` (device-local, like
 * lib/settings). The sidebar and the accounts page read through
 * useSyncExternalStore, so edits apply live everywhere.
 */

import type { ComponentType, CSSProperties } from "react"

import {
  Briefcase,
  Building2,
  Code,
  Coffee,
  Globe,
  Heart,
  Home,
  Mail,
  Music,
  Star,
  User,
  Zap,
} from "lucide-react"

export interface AvatarPref {
  /** Accent hex; omitted = theme primary. */
  color?: string
  /** Key from AVATAR_ICONS; omitted = initials. */
  icon?: string
}

/** Accent palette: mid tones that read on both light and dark surfaces. */
export const AVATAR_COLORS: { value: string; label: string }[] = [
  { value: "#2563eb", label: "avatar.color.blue" },
  { value: "#16a34a", label: "avatar.color.green" },
  { value: "#d97706", label: "avatar.color.amber" },
  { value: "#dc2626", label: "avatar.color.red" },
  { value: "#7c3aed", label: "avatar.color.violet" },
  { value: "#db2777", label: "avatar.color.pink" },
  { value: "#0d9488", label: "avatar.color.teal" },
  { value: "#4f46e5", label: "avatar.color.indigo" },
]

export const AVATAR_ICONS: {
  key: string
  label: string
  icon: ComponentType<{ className?: string }>
}[] = [
  { key: "mail", label: "avatar.icon.mail", icon: Mail },
  { key: "user", label: "avatar.icon.person", icon: User },
  { key: "briefcase", label: "avatar.icon.work", icon: Briefcase },
  { key: "home", label: "avatar.icon.home", icon: Home },
  { key: "building", label: "avatar.icon.office", icon: Building2 },
  { key: "star", label: "avatar.icon.star", icon: Star },
  { key: "heart", label: "avatar.icon.heart", icon: Heart },
  { key: "zap", label: "avatar.icon.zap", icon: Zap },
  { key: "globe", label: "avatar.icon.globe", icon: Globe },
  { key: "code", label: "avatar.icon.code", icon: Code },
  { key: "coffee", label: "avatar.icon.coffee", icon: Coffee },
  { key: "music", label: "avatar.icon.music", icon: Music },
]

const KEY = "mail.avatar-prefs"
const listeners = new Set<() => void>()
let cache: Record<string, AvatarPref> | null = null

function read(): Record<string, AvatarPref> {
  if (cache) return cache
  if (typeof window === "undefined") return {}
  try {
    cache = JSON.parse(window.localStorage.getItem(KEY) ?? "{}") as Record<string, AvatarPref>
  } catch {
    cache = {}
  }
  return cache
}

function write(next: Record<string, AvatarPref>): void {
  cache = next
  try {
    window.localStorage.setItem(KEY, JSON.stringify(next))
  } catch {
    // storage blocked/full: keep the in-memory value for this session
  }
  for (const notify of listeners) notify()
}

/** useSyncExternalStore plumbing: getSnapshot must return a cached value. */
export function subscribeAvatars(notify: () => void): () => void {
  listeners.add(notify)
  return () => listeners.delete(notify)
}

export function getAvatarSnapshot(): Record<string, AvatarPref> {
  return read()
}

export function getAvatarServerSnapshot(): Record<string, AvatarPref> {
  return {}
}

export function getAvatarPref(accountId: string): AvatarPref {
  return read()[accountId] ?? {}
}

export function setAvatarPref(accountId: string, pref: AvatarPref): void {
  write({ ...read(), [accountId]: pref })
}

export function resetAvatarPref(accountId: string): void {
  const next = { ...read() }
  delete next[accountId]
  write(next)
}

/** Tinted chip style for an accent color (16% wash + full-color content). */
export function avatarTint(color: string): CSSProperties {
  return {
    backgroundColor: `color-mix(in oklch, ${color} 16%, transparent)`,
    color,
  }
}
