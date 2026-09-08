import { beforeEach, test, expect } from "bun:test"
import { dayLabel, formatBytes, formatDate, formatProvider, initials, shortSender } from "./format"
import { setLocale } from "./i18n/index"

beforeEach(() => setLocale("it"))

test("formatBytes", () => {
  expect(formatBytes(500)).toBe("500 B")
  expect(formatBytes(2048)).toBe("2.0 KB")
  expect(formatBytes(5 * 1024 * 1024)).toBe("5.0 MB")
})

test("initials", () => {
  expect(initials("Luca Rossi")).toBe("LR")
  expect(initials("Anna")).toBe("A")
  expect(initials("")).toBe("?")
})

test("formatDate returns a readable string", () => {
  const s = formatDate("2026-08-25T09:30:00Z")
  expect(s.length).toBeGreaterThan(0)
})

test("dayLabel groups recent days", () => {
  const now = new Date("2026-08-28T12:00:00")
  expect(dayLabel(new Date("2026-08-28T08:00:00").toISOString(), now)).toBe("Oggi")
  expect(dayLabel(new Date("2026-08-27T08:00:00").toISOString(), now)).toBe("Ieri")
  expect(dayLabel(new Date("2026-08-24T08:00:00").toISOString(), now)).toBe("Lunedì")
  expect(dayLabel("not-a-date", now)).toBe("")
})

test("shortSender falls back to email local part", () => {
  expect(shortSender({ from: { name: "Anna Bianchi", email: "a@x.com" } })).toBe("Anna Bianchi")
  expect(shortSender({ from: { name: "", email: "elena@example.com" } })).toBe("elena")
})

test("formatProvider shows friendly labels", () => {
  expect(formatProvider("generic-imap")).toBe("Imap")
  expect(formatProvider("gmail")).toBe("Gmail")
  expect(formatProvider("microsoft")).toBe("Microsoft")
  expect(formatProvider("mock")).toBe("mock")
})
