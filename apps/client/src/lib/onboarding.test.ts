import { beforeEach, describe, expect, test } from "bun:test"

import { shouldShowOnboarding } from "./onboarding"

// minimal localStorage shim: bun test has no DOM window
const store = new Map<string, string>()
;(globalThis as Record<string, unknown>).window = {
  localStorage: {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
    removeItem: (k: string) => void store.delete(k),
  },
}

beforeEach(() => store.clear())

const base = { isTauri: true, loaded: true, accountCount: 0 }

describe("shouldShowOnboarding", () => {
  test("primo avvio senza account: mostra il wizard", () => {
    expect(shouldShowOnboarding(base)).toBe(true)
  })

  test("browser (dev): mai", () => {
    expect(shouldShowOnboarding({ ...base, isTauri: false })).toBe(false)
  })

  test("account non ancora caricati: mai", () => {
    expect(shouldShowOnboarding({ ...base, loaded: false })).toBe(false)
  })

  test("con almeno un account: mai", () => {
    expect(shouldShowOnboarding({ ...base, accountCount: 1 })).toBe(false)
  })

  test("flag impostato in localStorage: mai (skip o secondo avvio)", () => {
    window.localStorage.setItem("mail.setting.onboarding-done", "true")
    expect(shouldShowOnboarding(base)).toBe(false)
  })
})
