import { afterEach, beforeEach, expect, test } from "bun:test"

import { en } from "./dictionaries/en"
import { it } from "./dictionaries/it"
import {
  dateLocale,
  getLocale,
  resetLocaleCache,
  setLocale,
  subscribeLocale,
  t,
  type Locale,
} from "./index"

const hasWindow = typeof window !== "undefined"

/** Some test files stub a partial `window`: guard every access. */
function clearStorage(): void {
  try {
    window?.localStorage?.clear?.()
  } catch {
    // no real storage available
  }
}

beforeEach(() => {
  if (hasWindow) clearStorage()
  setLocale("it" as Locale)
})

afterEach(() => {
  if (hasWindow) clearStorage()
})

test("t() renders a missing key as the key itself", () => {
  setLocale("it" as Locale)
  expect(t("totally.missing.key")).toBe("totally.missing.key")
})

test("t() translates known keys for both locales (Italian is a real dict)", () => {
  setLocale("it" as Locale)
  expect(t("action.send")).toBe("action.send") // unmigrated key: no silent blank
  expect(t("folder.inbox")).toBe("Posta in arrivo")
  expect(t("nav.compose")).toBe("Nuovo messaggio")

  setLocale("en" as Locale)
  expect(t("folder.inbox")).toBe("Inbox")
  expect(t("nav.compose")).toBe("New message")
})

test("t() interpolates {param} placeholders", () => {
  setLocale("en" as Locale)
  expect(t("thread.messageCountMany", { n: 3 })).toBe("3 messages")
  expect(t("io.exported", { count: 12, failed: "" })).toBe("Exported 12 messages")
  setLocale("it" as Locale)
  expect(t("pgp.keySavedFor", { email: "a@b.dev" })).toBe("Chiave pubblica salvata per a@b.dev")
})

test("setLocale persists the choice and getLocale reads it back", () => {
  if (!hasWindow || typeof window?.localStorage?.getItem !== "function") return
  setLocale("en" as Locale)
  expect(getLocale()).toBe("en")
  expect(window.localStorage.getItem("mail.setting.locale")).toBe("en")
  setLocale("it" as Locale)
  expect(window.localStorage.getItem("mail.setting.locale")).toBe("it")
})

test("no saved choice falls back to the system language", () => {
  if (!hasWindow || typeof window?.localStorage?.getItem !== "function") return
  try {
    window.localStorage.removeItem("mail.setting.locale")
  } catch {
    // stub without removeItem
  }
  resetLocaleCache() // getLocale memoizes: force a fresh storage read
  const langs = navigator.languages
  Object.defineProperty(navigator, "languages", { value: ["en-US"], configurable: true })
  expect(getLocale()).toBe("en")
  Object.defineProperty(navigator, "languages", { value: ["it-IT", "en-US"], configurable: true })
  expect(getLocale()).toBe("it")
  Object.defineProperty(navigator, "languages", { value: langs, configurable: true })
  resetLocaleCache()
})

test("subscribeLocale notifies on change and supports unsubscribe", () => {
  let calls = 0
  const unsubscribe = subscribeLocale(() => calls++)
  setLocale("en" as Locale)
  setLocale("it" as Locale)
  expect(calls).toBe(2)
  unsubscribe()
  setLocale("en" as Locale)
  expect(calls).toBe(2)
  setLocale("it" as Locale)
})

test("dateLocale maps locales to BCP 47 tags", () => {
  setLocale("it" as Locale)
  expect(dateLocale()).toBe("it-IT")
  setLocale("en" as Locale)
  expect(dateLocale()).toBe("en-US")
})

test("the dictionaries cover exactly the same key set", () => {
  const itKeys = Object.keys(it).sort()
  const enKeys = Object.keys(en).sort()
  expect(enKeys).toEqual(itKeys)
  for (const key of itKeys) {
    expect(typeof en[key]).toBe("string")
    expect(en[key].length).toBeGreaterThan(0)
    expect(it[key].length).toBeGreaterThan(0)
  }
})
