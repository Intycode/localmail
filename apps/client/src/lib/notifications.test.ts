import { test, expect } from "bun:test"
import { senderLabel, NOTIFICATIONS_KEY } from "./notifications"
import { getSetting } from "./settings"

test("senderLabel prefers the personal name, then the email", () => {
  expect(senderLabel({ from: { name: "Luca Rossi", email: "luca@x.dev" } })).toBe("Luca Rossi")
  expect(senderLabel({ from: { email: "luca@x.dev" } })).toBe("luca@x.dev")
  expect(senderLabel({})).toBe("Nuova email")
})

test("NOTIFICATIONS_KEY defaults to enabled", () => {
  // key naming contract with the Settings toggle group
  expect(NOTIFICATIONS_KEY).toBe("notify-enabled")
  expect(getSetting(NOTIFICATIONS_KEY, true)).toBe(true)
})
