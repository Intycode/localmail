/**
 * lib/autoBackup — daily automatic backup of the encrypted database file.
 * Uses the existing backup_create engine command (the DB is already
 * encrypted at rest, so the snapshot is too). Triggered on app start: when
 * enabled, a directory is configured and the last backup is older than 24h,
 * a dated snapshot is written into the chosen folder. Best-effort and
 * silent: a failing backup must never disturb the session.
 */
import { isTauri, ioService } from "../services/tauriMailService"
import { autoBackupDir, autoBackupLastRun, getSetting, setAutoBackupLastRun } from "./settings"

export const AUTOBACKUP_KEY = "autobackup"
/** One backup per day, max. */
export const AUTOBACKUP_INTERVAL_MS = 24 * 3_600_000

export function autoBackupEnabled(): boolean {
  return getSetting(AUTOBACKUP_KEY, false)
}

/** True when a backup is due right now (enabled + dir + interval elapsed). */
export function autoBackupDue(now = new Date()): boolean {
  if (!isTauri() || !autoBackupEnabled()) return false
  const dir = autoBackupDir()
  if (!dir) return false
  const last = autoBackupLastRun()
  if (!last) return true
  const t = Date.parse(last)
  return !Number.isFinite(t) || now.getTime() - t >= AUTOBACKUP_INTERVAL_MS
}

/** Runs the automatic backup if due. Returns the created path or null. */
export async function maybeRunAutoBackup(now = new Date()): Promise<string | null> {
  if (!autoBackupDue(now)) return null
  try {
    const stamp = now.toISOString().slice(0, 10) // YYYY-MM-DD: one per day
    const dest = `${autoBackupDir().replace(/\/+$/, "")}/mail-backup-${stamp}.db`
    const res = await ioService.backupCreate(dest)
    setAutoBackupLastRun(now.toISOString())
    return res.path
  } catch {
    return null // silent: retried on the next start
  }
}
