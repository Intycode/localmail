/**
 * lib/snooze — snooze presets shared by the thread view. The engine stores
 * an RFC 3339 moment and restores the thread at that time (local loop), so
 * the presets only need deterministic computations. `now` is injectable for
 * tests. Labels are translated at call time (the thread view re-renders on
 * locale change).
 */

import { t } from "./i18n"

export interface SnoozePreset {
  label: string
  /** RFC 3339 timestamp of the return moment. */
  until: string
}

/** Next 8:00 AM local time strictly after `now`. */
function nextMorningAt8(now: Date): Date {
  const d = new Date(now)
  d.setHours(8, 0, 0, 0)
  if (d.getTime() <= now.getTime()) d.setDate(d.getDate() + 1)
  return d
}

/** Next Monday 8:00 AM local time strictly after `now`. */
function nextMonday8(now: Date): Date {
  const d = nextMorningAt8(now)
  do {
    d.setDate(d.getDate() + 1)
  } while (d.getDay() !== 1)
  return d
}

/** The snooze presets offered in the thread view. */
export function snoozePresets(now = new Date()): SnoozePreset[] {
  const inAnHour = new Date(now.getTime() + 3_600_000)
  const weekend = new Date(now.getTime() + 2 * 86_400_000)
  return [
    { label: t("snooze.in1h"), until: inAnHour.toISOString() },
    { label: t("snooze.tomorrow8"), until: nextMorningAt8(now).toISOString() },
    { label: t("snooze.nextWeekend"), until: weekend.toISOString() },
    { label: t("snooze.nextWeek"), until: nextMonday8(now).toISOString() },
  ]
}
