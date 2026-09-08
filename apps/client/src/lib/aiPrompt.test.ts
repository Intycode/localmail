import { test, expect } from "bun:test"
import type { Message, Thread } from "../types"
import { buildThreadSummary, buildTranslation, TRANSLATION_LANGUAGES } from "./aiPrompt"

function msg(partial: Partial<Message>): Message {
  return {
    id: "m1",
    threadId: "t1",
    folderId: "f1",
    accountId: "a1",
    subject: "Ciao",
    from: { name: "Anna", email: "anna@x.dev" },
    to: [],
    cc: [],
    bcc: [],
    date: "2026-08-28T09:30:00Z",
    body: "",
    bodyText: "",
    remoteImages: 0,
    isEncrypted: false,
    isSigned: false,
    spamScore: 0,
    phishing: [],
    isUnread: false,
    isStarred: false,
    labels: [],
    attachments: [],
    ...partial,
  }
}

function thread(partial: Partial<Thread> = {}): Thread {
  return {
    id: "t1",
    folderId: "f1",
    accountId: "a1",
    subject: "Preventivo rinnovo",
    messageIds: ["m1"],
    from: { name: "Anna", email: "anna@x.dev" },
    to: [],
    snippet: "",
    date: "2026-08-28T09:30:00Z",
    isUnread: false,
    isStarred: false,
    labels: [],
    messageCount: 1,
    hasAttachments: false,
    ...partial,
  }
}

test("buildThreadSummary: system + user with subject, sender and body", () => {
  const msgs = [
    msg({ bodyText: "In allegato il preventivo." }),
    msg({ id: "m2", from: { name: "Bo", email: "bo@x.dev" }, bodyText: "Ricevuto, grazie." }),
  ]
  const out = buildThreadSummary(thread(), msgs)
  expect(out).toHaveLength(2)
  expect(out[0].role).toBe("system")
  expect(out[1].role).toBe("user")
  const user = out[1].content
  expect(user).toContain("Oggetto: Preventivo rinnovo")
  expect(user).toContain("Anna <anna@x.dev>")
  expect(user).toContain("In allegato il preventivo.")
  expect(user).toContain("Bo <bo@x.dev>")
  expect(user).toContain("Ricevuto, grazie.")
})

test("buildThreadSummary: clips huge bodies but keeps every message", () => {
  const big = "x".repeat(10_000)
  const msgs = [msg({ bodyText: big }), msg({ id: "m2", bodyText: "fine" })]
  const out = buildThreadSummary(thread(), msgs)
  expect(out[1].content).toContain("fine")
  expect(out[1].content.length).toBeLessThan(24_000)
})

test("buildThreadSummary: total budget trims the transcript", () => {
  const msgs = Array.from({ length: 40 }, (_, i) =>
    msg({ id: "m" + i, bodyText: "corpo ".repeat(500) + " msg" + i }),
  )
  const out = buildThreadSummary(thread({ messageCount: msgs.length }), msgs)
  expect(out[1].content.length).toBeLessThan(26_000)
  expect(out[1].content).toContain("omessi")
})

test("buildThreadSummary: includes PDF attachment texts under the header", () => {
  const out = buildThreadSummary(
    thread(),
    [msg({ bodyText: "In allegato il preventivo." })],
    [
      { messageId: "m1", name: "preventivo.pdf", text: "Preventivo n. 42 — totale 1.200 EUR" },
      { messageId: "m1", name: "scheda.pdf", text: "Specifiche tecniche" },
    ],
  )
  const user = out[1].content
  expect(user).toContain("Allegati PDF:")
  expect(user).toContain("--- Allegato: preventivo.pdf")
  expect(user).toContain("Preventivo n. 42 — totale 1.200 EUR")
  expect(user).toContain("--- Allegato: scheda.pdf")
})

test("buildThreadSummary: clips huge attachment texts and the total budget", () => {
  const big = "y".repeat(10_000)
  const out = buildThreadSummary(
    thread(),
    [msg({})],
    [
      { messageId: "m1", name: "grande.pdf", text: big },
      { messageId: "m1", name: "ancora.pdf", text: "z".repeat(10_000) },
    ],
  )
  const user = out[1].content
  expect(user).toContain("y".repeat(100) + " […]")
  expect(user).toContain("omessi")
  // transcript (24k) + attach budget (8k) + headers, never runaway
  expect(user.length).toBeLessThan(36_000)
})

test("buildThreadSummary: no attachments section when none supplied", () => {
  const out = buildThreadSummary(thread(), [msg({})])
  expect(out[1].content).not.toContain("Allegati PDF:")
})

test("buildThreadSummary: scanned pages ride the user message as images", () => {
  const page = (n: number) => `data:image/jpeg;base64,${"abcd".repeat(n + 1)}`
  const out = buildThreadSummary(
    thread(),
    [msg({})],
    [],
    [{ messageId: "m1", name: "scansione.pdf", pages: [page(0), page(1)], totalPages: 5 }],
  )
  expect(out[1].images).toEqual([page(0), page(1)])
  expect(out[1].content).toContain("Allegati scansionati")
  expect(out[1].content).toContain("scansione.pdf (2 pagine allegate come immagini, 3 omesse)")
  // the system prompt must tell the model what the images are
  expect(out[0].content).toContain("PDF scansionati")
})

test("buildThreadSummary: global cap on scanned pages across files", () => {
  const pages = ["data:image/jpeg;base64,AAA", "data:image/jpeg;base64,BBB"]
  const out = buildThreadSummary(
    thread(),
    [msg({})],
    [],
    [
      { messageId: "m1", name: "a.pdf", pages, totalPages: 2 },
      { messageId: "m1", name: "b.pdf", pages, totalPages: 2 },
      { messageId: "m1", name: "c.pdf", pages, totalPages: 2 },
      { messageId: "m1", name: "d.pdf", pages, totalPages: 2 },
    ],
  )
  // MAX_SCANNED_PAGES = 6: a(2) + b(2) + c(2) fit, d is cited but skipped
  expect(out[1].images).toHaveLength(6)
  expect(out[1].content).toContain("d.pdf (pagine non incluse per limite)")
})

test("buildTranslation: system + user carrying the content and target language", () => {
  const out = buildTranslation("- Punto chiave\n- Da fare: rispondere", "en")
  expect(out).toHaveLength(2)
  expect(out[0].role).toBe("system")
  // the instruction must preserve Markdown and forbid extra commentary
  expect(out[0].content).toContain("Markdown")
  expect(out[0].content).toContain("esclusivamente con la traduzione")
  expect(out[1].role).toBe("user")
  expect(out[1].content).toContain("English")
  expect(out[1].content).toContain("Da fare: rispondere")
})

test("buildTranslation: every offered language resolves to its native label", () => {
  for (const l of TRANSLATION_LANGUAGES) {
    const out = buildTranslation("testo", l.code)
    expect(out[1].content).toContain(l.label)
  }
})

test("buildTranslation: no images ride the translation request", () => {
  const out = buildTranslation("testo", "zh")
  expect(out.every((m) => m.images === undefined)).toBe(true)
})
