/**
 * lib/aiPrompt — prompt builders for the user's local AI (M12). Pure
 * functions: the webview already holds the mail content (and, for PDF
 * attachments, the engine-extracted text or the rendered pages of scanned
 * documents), so the UI assembles the request and the engine only forwards
 * it to the configured local server (Ollama / LM Studio). Caps keep the
 * prompt inside the IPC line budget and small models effective.
 */

import type { AIChatMessage } from "@/services/tauriMailService"
import type { Message, Thread } from "@/types"
import { formatDate } from "@/lib/format"

/** Total character budget for the thread transcript sent to the model. */
const THREAD_BUDGET = 24_000

/** Per-message body cap: long threads lose depth, not messages. */
const BODY_BUDGET = 4_000

/** Total budget for the PDF attachment texts appended after the transcript. */
const ATTACH_BUDGET = 8_000

/** Per-attachment cap: one huge PDF must not starve the others. */
const ATTACH_PER_FILE = 4_000

/** Hard cap on total image pages per request (each ~100-300 KB base64). */
export const MAX_SCANNED_PAGES = 6

/** Target languages offered for on-demand summary translation (native
 *  labels: the user recognizes their own language written as its speakers
 *  do). `label` is what the model sees, too. */
export const TRANSLATION_LANGUAGES = [
  { code: "en", label: "English" },
  { code: "fr", label: "Français" },
  { code: "es", label: "Español" },
  { code: "de", label: "Deutsch" },
  { code: "pt", label: "Português" },
  { code: "zh", label: "中文" },
] as const

export type TranslationLanguage = (typeof TRANSLATION_LANGUAGES)[number]["code"]

/** System instruction: faithful Markdown-preserving translation. The base
 *  summary is Italian; translations are a separate, cacheable request so a
 *  language switch never regenerates the underlying summary. */
const TRANSLATION_SYSTEM =
  "Sei un assistente locale che traduce riassunti di posta elettronica. " +
  "Traduci il testo dell'utente nella lingua richiesta, in modo fedele e " +
  "conciso. Preserva il formato Markdown: punti elenco, grassetto, titoli " +
  "e la riga 'Da fare:' (che tradurrai anch'essa). Non commentare, non " +
  "aggiungere note o prefazioni: rispondi esclusivamente con la traduzione."

/** System instruction: consistent, local-language summaries. */
const SUMMARY_SYSTEM =
  "Sei un assistente locale che riassume thread di posta elettronica. " +
  "Rispondi in italiano, in modo conciso e fattuale. " +
  "Formato: prima 3-5 punti chiave (elenco con -), poi una riga " +
  "'Da fare:' con le azioni richieste all'utente, se presenti. " +
  "Non inventare informazioni: usa solo il testo dei messaggi e degli " +
  "allegati forniti; le immagini, se presenti, sono pagine di PDF " +
  "scansionati: leggile e integrale nel riassunto. Se un allegato è " +
  "citato ma il suo contenuto non c'è, non tirare conclusioni."

/** One PDF attachment's extracted text, keyed to its source message. */
export interface AttachmentText {
  messageId: string
  name: string
  text: string
}

/** One scanned PDF's rendered pages (data URLs) for vision models. */
export interface ScannedAttachment {
  messageId: string
  name: string
  /** data URLs ("data:image/jpeg;base64,..."), engine-rendered pages */
  pages: string[]
  /** pages present in the document beyond what was rendered */
  totalPages: number
}

/** buildThreadSummary composes the chat request that summarizes a thread,
 *  optionally including the engine-extracted text of the PDF attachments
 *  and the rendered pages of scanned ones (vision models). */
export function buildThreadSummary(
  thread: Thread,
  messages: Message[],
  attachments: AttachmentText[] = [],
  scanned: ScannedAttachment[] = [],
): AIChatMessage[] {
  const parts: string[] = []
  let budget = THREAD_BUDGET
  for (const m of messages) {
    const body = m.bodyText.trim() || "(corpo non disponibile)"
    const clipped = body.length > BODY_BUDGET ? body.slice(0, BODY_BUDGET) + " […]" : body
    const block = [
      "---",
      `Da: ${m.from.name || m.from.email} <${m.from.email}>`,
      `Data: ${formatDate(m.date)}`,
      "",
      clipped,
    ].join("\n")
    if (budget - block.length < 0) {
      parts.push("---\n(ulteriori messaggi omessi per lunghezza)")
      break
    }
    budget -= block.length
    parts.push(block)
  }
  const transcript = [`Oggetto: ${thread.subject}`, `Messaggi: ${messages.length}`, ...parts].join(
    "\n\n",
  )

  const sections = [transcript]
  if (attachments.length > 0) {
    const attachBlocks: string[] = ["Allegati PDF:"]
    let attachBudget = ATTACH_BUDGET
    for (const a of attachments) {
      const clipped =
        a.text.length > ATTACH_PER_FILE ? a.text.slice(0, ATTACH_PER_FILE) + " […]" : a.text
      const block = [`--- Allegato: ${a.name}`, clipped].join("\n")
      if (attachBudget - block.length < 0) {
        attachBlocks.push("(altri allegati omessi per lunghezza)")
        break
      }
      attachBudget -= block.length
      attachBlocks.push(block)
    }
    sections.push(attachBlocks.join("\n\n"))
  }

  // Scanned PDFs travel as images on the user turn (vision models); the
  // transcript cites them so the model knows what the pages belong to.
  const images: string[] = []
  const scannedNotes: string[] = []
  for (const s of scanned) {
    if (s.pages.length === 0) continue
    const room = MAX_SCANNED_PAGES - images.length
    if (room <= 0) {
      scannedNotes.push(`--- Allegato scansionato: ${s.name} (pagine non incluse per limite)`)
      continue
    }
    const kept = s.pages.slice(0, room)
    images.push(...kept)
    const omitted = Math.max(0, s.totalPages - kept.length)
    scannedNotes.push(
      `--- Allegato scansionato: ${s.name} (${kept.length} pagine allegate come immagini` +
        (omitted > 0 ? `, ${omitted} omesse` : "") +
        ")",
    )
  }
  if (scannedNotes.length > 0) {
    sections.push(["Allegati scansionati (pagine come immagini):", ...scannedNotes].join("\n"))
  }

  const userMessage: AIChatMessage = {
    role: "user",
    content: "Riassumi questo thread di email:\n\n" + sections.join("\n\n"),
  }
  if (images.length > 0) userMessage.images = images

  return [{ role: "system", content: SUMMARY_SYSTEM }, userMessage]
}

/** buildTranslation composes the chat request that translates a ready
 *  summary into `target`. Pure and stateless: the caller caches results. */
export function buildTranslation(content: string, target: TranslationLanguage): AIChatMessage[] {
  const lang = TRANSLATION_LANGUAGES.find((l) => l.code === target)?.label ?? target
  return [
    { role: "system", content: TRANSLATION_SYSTEM },
    { role: "user", content: `Traduci questo riassunto in ${lang}:\n\n${content}` },
  ]
}
