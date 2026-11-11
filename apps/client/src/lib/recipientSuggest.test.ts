import { expect, test } from "bun:test"
import { applySuggestion, lastEntry } from "./recipientSuggest"

test("lastEntry: only what follows the last comma", () => {
  expect(lastEntry("a@x.it, an")).toBe("an")
  expect(lastEntry("a@x.it, ")).toBe("")
  expect(lastEntry("an")).toBe("an")
})

test("applySuggestion: replaces the typed entry, keeps the previous ones", () => {
  expect(applySuggestion("an", { email: "anna@x.it", name: "Anna" })).toBe("Anna <anna@x.it>, ")
  expect(applySuggestion("a@x.it, an", { email: "anna@x.it" })).toBe("a@x.it, anna@x.it, ")
  // a name with a comma would split into two recipients: email only
  expect(applySuggestion("ro", { email: "r@x.it", name: "Rossi, Anna" })).toBe("r@x.it, ")
})
