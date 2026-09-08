/**
 * lib/notifications — native system notifications for new mail (privacy:
 * everything is rendered locally, nothing leaves the device). Gated by the
 * Settings toggle `notify-enabled` (default ON) and the OS permission; a
 * failing notification must never break the event stream.
 */
import { getSetting, setSetting } from "./settings"
import { isTauri } from "../services/tauriMailService"
import { t } from "./i18n"

export const NOTIFICATIONS_KEY = "notify-enabled"

export function notificationsEnabled(): boolean {
  return getSetting(NOTIFICATIONS_KEY, true)
}

export function setNotificationsEnabled(value: boolean): void {
  setSetting(NOTIFICATIONS_KEY, value)
}

/** Recipient as serialized by the engine inside message.created payloads. */
interface RecipientData {
  name?: string
  email?: string
}

/** Extracts a human sender label from the engine's message payload. */
export function senderLabel(data: Record<string, unknown>): string {
  const from = data.from as RecipientData | undefined
  if (from?.name) return from.name
  if (from?.email) return from.email
  return t("notify.newEmail")
}

/** Shows one system notification for an incoming message. Unread-only:
 *  messages already marked read (filters, rules) stay silent. */
export async function notifyNewMessage(data: Record<string, unknown>): Promise<void> {
  if (!isTauri() || !notificationsEnabled()) return
  if (data.isUnread === false) return
  try {
    const mod = await import("@tauri-apps/plugin-notification")
    let granted = await mod.isPermissionGranted()
    if (!granted) {
      const requested = await mod.requestPermission()
      granted = requested === "granted"
    }
    if (!granted) return
    const subject = String(data.subject ?? "").trim() || t("notify.noSubject")
    void mod.sendNotification({ title: senderLabel(data), body: subject })
  } catch {
    // best effort: a notification failure must not disturb the sync flow
  }
}
