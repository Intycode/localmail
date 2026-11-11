import { beforeEach, test, expect } from "bun:test"
import type { Message } from "../types"
import {
  buildQuoteCtx,
  buildTextPart,
  composeHtmlBody,
  displayBody,
  restoreQuoteCtx,
  signatureHtml,
  stripQuoteMarks,
  typedPart,
} from "./composeQuote"
import { setLocale } from "./i18n/index"

beforeEach(() => setLocale("it"))

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

test("stripQuoteMarks removes every nesting level", () => {
  expect(stripQuoteMarks("> > > profondo")).toBe("profondo")
  expect(stripQuoteMarks("> uno\n> > due")).toBe("uno\ndue")
  expect(stripQuoteMarks("senza segni")).toBe("senza segni")
})

test("buildQuoteCtx: clean text, header line and original html", () => {
  const m = msg({
    body: "<p>Ciao <b>tu</b></p>",
    bodyText: "Ciao tu!\n> > vecchia annidata",
  })
  const ctx = buildQuoteCtx(m)
  expect(ctx.marker).toContain(" ha scritto:")
  // the plain quote is clean: no marks pile up in the wire text part
  expect(ctx.text).toBe("Ciao tu!\nvecchia annidata")
  // the html quote keeps the original markup verbatim
  expect(ctx.html).toContain('<blockquote type="cite"><p>Ciao <b>tu</b></p></blockquote>')
})

test("buildQuoteCtx escapes text when the original has no html part", () => {
  const m = msg({ body: "a < b", bodyText: "a < b" })
  const ctx = buildQuoteCtx(m)
  expect(ctx.html).toContain("<p>a &lt; b</p>")
})

test("buildTextPart: typed text then RFC-style quote, one level per line", () => {
  const m = msg({ bodyText: "Ciao!\n> vecchia" })
  const ctx = buildQuoteCtx(m)
  const wire = buildTextPart("risposta", ctx)
  expect(wire.startsWith("risposta\n\n")).toBe(true)
  expect(wire).toContain("\n> Ciao!\n> vecchia\n")
  // header line never prefixed
  expect(wire).toContain(ctx.marker + "\n> Ciao!")
})

test("buildTextPart is a no-op without context", () => {
  expect(buildTextPart("testo", null)).toBe("testo")
})

test("round trip: typed -> wire -> reopen keeps only the typed part", () => {
  const m = msg({ bodyText: "Ciao!\n> vecchia" })
  const ctx = buildQuoteCtx(m)
  const wire = buildTextPart("risposta", ctx)
  // a stored draft carries the wire text in bodyText; displayBody cleans it
  const reopened = displayBody(msg({ bodyText: wire }))
  expect(typedPart(reopened, ctx).trimEnd()).toBe("risposta")
})

test("composeHtmlBody: typed paragraphs plus the quote, always", () => {
  const m = msg({ body: "<p>Ciao <b>tu</b></p>", bodyText: "Ciao tu!" })
  const ctx = buildQuoteCtx(m)
  const html = composeHtmlBody("risposta", ctx)
  expect(html).toContain("<p>risposta</p>")
  expect(html).toContain('<blockquote type="cite"><p>Ciao <b>tu</b></p></blockquote>')
  // the quote is included even when the typed body does not carry the marker:
  // the editor never hosts the quote, so it cannot be trimmed while writing
  expect(composeHtmlBody("", ctx)).toContain('<blockquote type="cite">')
})

test("composeHtmlBody: plain text only without context", () => {
  expect(composeHtmlBody("uno\n\ndue", null)).toBe("<p>uno</p>\n<p>due</p>")
  expect(composeHtmlBody("   ", null)).toBe("")
})

test("restoreQuoteCtx recovers marker, text and html from a stored draft", () => {
  const m = msg({ body: "<p>Ciao <b>tu</b></p>", bodyText: "Ciao tu!" })
  const ctx = buildQuoteCtx(m)
  const wireText = buildTextPart("risposta", ctx)
  const draft = msg({ body: composeHtmlBody("risposta", ctx), bodyText: wireText })
  const restored = restoreQuoteCtx(draft)
  if (!restored) throw new Error("quote context not restored")
  expect(restored.marker).toBe(ctx.marker)
  expect(restored.text).toBe(ctx.text)
  expect(restored.html).toBe(ctx.html)
})

test("restoreQuoteCtx yields null for old text-only drafts", () => {
  const legacy = msg({ body: "risposta\nIl tempo, Anna ha scritto:\n> citazione" })
  expect(restoreQuoteCtx(legacy)).toBeNull()
})

test("drafts saved in another UI language still restore their quote", () => {
  setLocale("en")
  const enCtx = buildQuoteCtx(msg({ bodyText: "Hi!" }))
  expect(enCtx.marker).toContain(" wrote:")
  const wireText = buildTextPart("reply", enCtx)
  const draft = msg({ body: composeHtmlBody("reply", enCtx), bodyText: wireText })
  // reopen with an Italian UI: the English marker is still detected
  setLocale("it")
  const restored = restoreQuoteCtx(draft)
  expect(restored?.marker).toBe(enCtx.marker)
  expect(restored?.text).toBe(enCtx.text)
})

test("signature: after the typed text, before the quote, in both parts", () => {
  const ctx = buildQuoteCtx(msg({ bodyText: "originale", body: "originale" }))
  const sig = { text: "Dario\n<Intycode>", image: "data:image/png;base64,AAAA" }
  const text = buildTextPart("risposta\n", ctx, sig)
  expect(text.startsWith("risposta\n\n-- \nDario\n<Intycode>\n\n")).toBe(true)
  expect(text.indexOf("Dario")).toBeLessThan(text.indexOf("> originale"))
  const html = composeHtmlBody("risposta", ctx, sig)
  expect(html).toContain('<p style="margin:0">Dario<br>&lt;Intycode&gt;</p>')
  expect(html).toContain('<img src="data:image/png;base64,AAAA" alt="">')
  expect(html.indexOf("Dario")).toBeLessThan(html.indexOf("<img")) // default: below
  expect(html.indexOf("signature")).toBeLessThan(html.indexOf("<blockquote"))
  // image-only signature: nothing in the text part
  expect(buildTextPart("x", null, { text: " ", image: "data:image/png;base64,AAAA" })).toBe("x")
})

test("signatureHtml: image position and width", () => {
  const base = { text: "Dario", image: "data:image/png;base64,AAAA", imageWidth: 120 }
  const above = signatureHtml({ ...base, imagePosition: "above" })
  expect(above.indexOf("<img")).toBeLessThan(above.indexOf("Dario"))
  expect(above).toContain('width="120" style="width:120px;max-width:100%;height:auto"')
  const left = signatureHtml({ ...base, imagePosition: "left" })
  expect(left).toContain("<table")
  expect(left.indexOf("<img")).toBeLessThan(left.indexOf("Dario"))
  const right = signatureHtml({ ...base, imagePosition: "right" })
  expect(right.indexOf("Dario")).toBeLessThan(right.indexOf("<img"))
  // a malformed image value never reaches the markup
  expect(signatureHtml({ text: "x", image: 'data:image/png;base64,A"><script>' })).not.toContain(
    "script",
  )
})
