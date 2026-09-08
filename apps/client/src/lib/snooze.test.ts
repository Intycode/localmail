import { beforeEach, test, expect } from "bun:test"
import { snoozePresets } from "./snooze"
import { setLocale } from "./i18n/index"

beforeEach(() => setLocale("it"))

test("snooze presets are in the future and ordered", () => {
  const now = new Date("2026-09-01T10:00:00")
  const presets = snoozePresets(now)
  const labels = presets.map((p) => p.label)
  expect(labels).toEqual([
    "Tra 1 ora",
    "Domani alle 8:00",
    "Prossimo weekend",
    "Prossima settimana",
  ])
  const times = presets.map((p) => Date.parse(p.until))
  for (const t of times) expect(t).toBeGreaterThan(now.getTime())
  expect([...times].sort((a, b) => a - b)).toEqual(times)
})

test("domani alle 8:00 lands at 08:00 the next day (or same day before 8)", () => {
  // 10:00 → next 8:00 is tomorrow
  const morning = new Date("2026-09-01T10:00:00")
  const domani = snoozePresets(morning)[1]
  const d = new Date(domani.until)
  expect(d.getHours()).toBe(8)
  expect(d.getDate()).toBe(2)

  // 06:00 → today at 8:00 is still in the future
  const early = new Date("2026-09-01T06:00:00")
  const oggi = snoozePresets(early)[1]
  expect(new Date(oggi.until).getDate()).toBe(1)
  expect(new Date(oggi.until).getHours()).toBe(8)
})

test("prossima settimana is a Monday at 8:00", () => {
  // 2026-09-01 is a Tuesday → next Monday is 2026-09-07
  const now = new Date("2026-09-01T10:00:00")
  const next = snoozePresets(now)[3]
  const d = new Date(next.until)
  expect(d.getDay()).toBe(1)
  expect(d.getHours()).toBe(8)
  expect(d.getDate()).toBe(7)
})
