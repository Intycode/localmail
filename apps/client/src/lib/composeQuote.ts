import type { Message } from "../types"
import { dateLocale, t } from "./i18n"
import type { Signature } from "./signature"

/**
 * Quote helpers for the composer (pure functions, unit-testable).
 *
 * The editor never hosts the quoted thread: a reply starts with an empty
 * body and the previous message is shown on demand (rendered like the thread
 * view) and included in the outgoing parts at send time:
 * - text part (buildTextPart): RFC-style "> " quote, one level per line;
 * - html part (composeHtmlBody): the original markup inside a blockquote.
 * In both cases the marks/markup exist only on the wire, never while writing.
 */

/** Quote context carried from the replied message (or a reopened draft):
 *  `marker` is the "… ha scritto:" header line used on the wire, `text` the
 *  clean plain text of the quote (for the text part) and `html` its
 *  blockquote twin with the original markup (for the html part). */
export interface QuoteContext {
  marker: string
  text: string
  html: string
}

/** "Il <date>, <who> ha scritto:" — shared by the text quote and its HTML twin.
 *  Translated with the active UI locale (the wire text goes out in the
 *  user's language). */
export function quoteHeader(m: Message): string {
  const when = new Date(m.date).toLocaleString(dateLocale())
  const who = m.from.name ? `${m.from.name} <${m.from.email}>` : m.from.email
  return t("compose.quoteHeader", { when, who })
}

/** Suffixes the quote header line ends with, per UI language. Detection must
 *  cover every language a draft may have been saved with — keep in sync with
 *  the `compose.quoteHeader` strings in the dictionaries. */
const QUOTE_MARKER_SUFFIXES = [" ha scritto:", " wrote:"]

/** Index of the quote header suffix in `text`, or -1. */
function quoteMarkerIndex(text: string): number {
  for (const suffix of QUOTE_MARKER_SUFFIXES) {
    const i = text.indexOf(suffix)
    if (i !== -1) return i
  }
  return -1
}

/** true when the message carries a real HTML part (body differs from text). */
export function hasHtmlPart(m: Message): boolean {
  const body = (m.body ?? "").trim()
  return body !== "" && body !== (m.bodyText ?? "").trim()
}

/** Removes every leading "> " quote level from each line (nested quotes must
 *  not pile up: the wire format re-adds exactly one level). */
export function stripQuoteMarks(text: string): string {
  return text
    .split("\n")
    .map((l) => l.replace(/^\s*(?:>\s?)+/, ""))
    .join("\n")
}

/** The HTML twin of the text quote: header line + blockquote. The original
 *  message keeps its own markup, so a reply never sends it as flat text. */
export function buildQuoteHtml(m: Message): string {
  const header = `<p>${escapeHtml(quoteHeader(m))}</p>`
  if (hasHtmlPart(m)) {
    return `${header}<blockquote type="cite">${(m.body ?? "").trim()}</blockquote>`
  }
  const text = (m.bodyText || m.body || "").trim()
  return `${header}<blockquote type="cite"><p>${escapeHtml(text).replace(/\n/g, "<br>")}</p></blockquote>`
}

/** Quote context for a reply: header line, clean text and HTML blockquote of
 *  the message being answered. */
export function buildQuoteCtx(m: Message): QuoteContext {
  return {
    marker: quoteHeader(m),
    text: stripQuoteMarks((m.bodyText || m.body || "").trim()),
    html: buildQuoteHtml(m),
  }
}

/** The quote header line inside a stored text body, if any. */
export function findQuoteMarkerLine(text: string): string | null {
  const line = text.split("\n").find((l) => quoteMarkerIndex(l) !== -1)
  return line ? line.trim() : null
}

/** Restores the quote context from a reopened draft: the stored HTML part
 *  ends with the quote block and the text one carries the matching header
 *  line. Old text-only drafts have no <p> markup and yield null. */
export function restoreQuoteCtx(m: Message): QuoteContext | null {
  const html = (m.body ?? "").trim()
  const i = quoteMarkerIndex(html)
  if (!html || i === -1) return null
  const pStart = html.lastIndexOf("<p>", i)
  if (pStart === -1) return null
  const marker = findQuoteMarkerLine(m.bodyText || m.body || "")
  if (!marker) return null
  const mi = (m.bodyText || m.body).indexOf(marker)
  const nl = (m.bodyText || m.body).indexOf("\n", mi)
  const text = nl >= 0 ? stripQuoteMarks((m.bodyText || m.body).slice(nl + 1)).trimEnd() : ""
  return { marker, text, html: html.slice(pStart) }
}

/** Editor text from a stored message (draft reopen): the quote region after
 *  the header line is shown without marks, however it was stored. */
export function displayBody(m: Message): string {
  const text = m.bodyText || m.body || ""
  const marker = findQuoteMarkerLine(text)
  if (!marker) return text
  const i = text.indexOf(marker)
  const nl = text.indexOf("\n", i)
  if (nl < 0) return text
  return text.slice(0, nl + 1) + stripQuoteMarks(text.slice(nl + 1))
}

/** The typed part of `body`: everything before the quote marker, or the
 *  whole body when no quote is present. */
export function typedPart(body: string, ctx: QuoteContext | null): string {
  if (!ctx) return body
  const i = body.indexOf(ctx.marker)
  return i >= 0 ? body.slice(0, i) : body
}

/** Text part for the wire: what the user typed, the signature after the
 *  conventional "-- " line, then the header line and the quote as RFC-style
 *  text ("> " per line, one level). Without context/signature the body is
 *  sent as-is. */
export function buildTextPart(
  body: string,
  ctx: QuoteContext | null,
  sig: Signature | null = null,
): string {
  const sigText = sig?.text.trim()
  const signed = sigText ? `${body.replace(/\s+$/, "")}\n\n-- \n${sigText}` : body
  if (!ctx) return signed
  const typed = signed.replace(/\s+$/, "")
  const quoted = ctx.text
    .split("\n")
    .map((l) => (l === "" ? ">" : `> ${l}`))
    .join("\n")
  return `${typed}\n\n${ctx.marker}\n${quoted}\n`
}

/** Signature block for the HTML part (also the composer/dialog preview);
 *  the image stays a data URL here, the engine converts it to an inline cid
 *  part at send time. Side-by-side uses a table: the only layout every mail
 *  client honours. */
export function signatureHtml(sig: Signature): string {
  const text = sig.text.trim()
  const p = text ? `<p style="margin:0">${escapeHtml(text).replace(/\n/g, "<br>")}</p>` : ""
  // strict shape: the value lands inside an attribute
  const src = /^data:image\/png;base64,[A-Za-z0-9+/=]+$/.test(sig.image ?? "") ? sig.image : ""
  const w = Math.round(sig.imageWidth ?? 0)
  const size = w > 0 ? ` width="${w}" style="width:${w}px;max-width:100%;height:auto"` : ""
  const img = src ? `<img src="${src}" alt=""${size}>` : ""
  if (!p || !img) return p || img ? `<div class="signature">${p}${img}</div>` : ""
  const pos = sig.imagePosition ?? "below"
  if (pos === "above") return `<div class="signature">${img}<br>${p}</div>`
  if (pos === "below") return `<div class="signature">${p}<br>${img}</div>`
  const cell = (html: string, pad: string) =>
    `<td style="vertical-align:middle;padding:0${pad}">${html}</td>`
  const cells =
    pos === "left" ? cell(img, " 12px 0 0") + cell(p, "") : cell(p, " 12px 0 0") + cell(img, "")
  return `<div class="signature"><table cellpadding="0" cellspacing="0" border="0"><tr>${cells}</tr></table></div>`
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
}

/** Plain text -> minimal semantic HTML (paragraphs, <br> within one). */
function textToHtml(text: string): string {
  const t = text.replace(/\r\n/g, "\n").trim()
  if (!t) return ""
  return t
    .split(/\n{2,}/)
    .map((p) => `<p>${escapeHtml(p).replace(/\n/g, "<br>")}</p>`)
    .join("\n")
}

/** HTML part for send/draft: the typed text as paragraphs, the signature,
 *  plus the original HTML quote (always, when a quote context exists: the
 *  editor does not host the quote, so it cannot be trimmed by editing the
 *  body). Empty when there is nothing beyond plain text — the engine then
 *  sends a simple text/plain. */
export function composeHtmlBody(
  body: string,
  ctx: QuoteContext | null,
  sig: Signature | null = null,
): string {
  return [textToHtml(body), sig ? signatureHtml(sig) : "", ctx?.html].filter(Boolean).join("\n")
}
