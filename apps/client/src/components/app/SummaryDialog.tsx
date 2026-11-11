import * as React from "react"
import { Languages, Loader2, Sparkles } from "lucide-react"
import { toast } from "sonner"

import {
  buildThreadSummary,
  buildTranslation,
  TRANSLATION_LANGUAGES,
  type AttachmentText,
  type TranslationLanguage,
} from "@/lib/aiPrompt"
import { useT } from "@/lib/i18n"
import { aiService, attachmentService } from "@/services/tauriMailService"
import type { Message, Thread } from "@/types"
import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/app/CopyButton"
import { Markdown } from "@/components/app/Markdown"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"

interface SummaryDialogProps {
  thread: Thread
  messages: Message[]
}

type SummaryState =
  | { status: "idle" }
  | { status: "loading"; threadId: string; note?: string }
  | { status: "ready"; threadId: string; content: string }
  | { status: "error"; threadId: string; message: string }

/** The dialog's language state: the Italian base summary or a translated
 *  copy. "it" is not in TRANSLATION_LANGUAGES (nothing to translate). */
type DialogLanguage = "it" | TranslationLanguage

/** PDF attachments worth reading: by contentType or .pdf name, under a size
 *  cap (the engine refuses > 50 MB; the UI skips early to keep UX snappy). */
function pdfAttachments(
  messages: Message[],
): { messageId: string; accountId: string; name: string }[] {
  const maxBytes = 25_000_000
  const out: { messageId: string; accountId: string; name: string }[] = []
  for (const m of messages) {
    for (const a of m.attachments ?? []) {
      const isPdf =
        a.contentType.toLowerCase().includes("pdf") || a.name.toLowerCase().endsWith(".pdf")
      if (isPdf && a.size <= maxBytes) {
        out.push({ messageId: m.id, accountId: m.accountId, name: a.name })
      }
    }
  }
  return out
}

/**
 * SummaryDialog — "Riassumi" opens the summary modal: a tinted Sparkles
 * header with the thread's meta as compact badges, a language toolbar (the
 * base summary is Italian; switching language translates on demand through
 * the local model and caches the result), then the summary as clean
 * Markdown with copy / regenerate. Bubbles come from the shadcn Bubble
 * primitive; prompts are built in the webview (lib/aiPrompt) and the
 * engine forwards them to the local server configured in Impostazioni →
 * AI locale. Closing during generation keeps the request running; a stale
 * result is discarded.
 */
export function SummaryDialog({ thread, messages }: SummaryDialogProps): React.JSX.Element {
  const t = useT()
  const [open, setOpen] = React.useState(false)
  const [state, setState] = React.useState<SummaryState>({ status: "idle" })
  const [lang, setLang] = React.useState<DialogLanguage>("it")
  const [translations, setTranslations] = React.useState<
    Partial<Record<TranslationLanguage, string>>
  >({})
  const [translating, setTranslating] = React.useState<TranslationLanguage | null>(null)
  // runId discards responses from a superseded run (reopen / regenerate)
  const runId = React.useRef(0)
  // generation counter: translations requested before a regenerate are stale
  // even if the summary request itself was never superseded
  const gen = React.useRef(0)
  // in-flight translation requests, deduped per language (rapid re-selections
  // reuse the pending call instead of firing a duplicate one)
  const inflight = React.useRef<Partial<Record<TranslationLanguage, Promise<string>>>>({})
  // mirror of `lang` for async callbacks (error fallback must only yank the
  // view when the user is still looking at the language that failed)
  const langRef = React.useRef<DialogLanguage>("it")
  React.useEffect(() => {
    langRef.current = lang
  }, [lang])

  const run = React.useCallback(async (): Promise<void> => {
    const id = ++runId.current
    gen.current++ // pending translations would serve the OLD base text
    inflight.current = {}
    setLang("it")
    setTranslations({})
    setTranslating(null)
    setState({ status: "loading", threadId: thread.id })
    try {
      // 1. PDF attachments: engine-side extraction, sequential so a mailbox
      // with many files never hammers the IMAP server in parallel. Scanned
      // PDFs (empty text) are skipped.
      const pdfs = pdfAttachments(messages)
      const texts: AttachmentText[] = []
      for (let i = 0; i < pdfs.length; i++) {
        if (runId.current !== id) return
        const p = pdfs[i]
        setState({
          status: "loading",
          threadId: thread.id,
          note: t("summary.readingPdf", { i: i + 1, n: pdfs.length, name: p.name }),
        })
        try {
          const res = await attachmentService.text(p.accountId, p.messageId, p.name)
          const text = res.text.trim()
          if (text) texts.push({ messageId: p.messageId, name: p.name, text })
        } catch {
          // unreadable (protected, offline server): skip it — the prompt
          // covers only what the model actually gets
        }
      }
      if (runId.current !== id) return
      // 2. generation
      setState({ status: "loading", threadId: thread.id })
      const res = await aiService.chat(buildThreadSummary(thread, messages, texts))
      if (runId.current !== id) return // dialog moved on: ignore stale result
      const content = res.content.trim()
      if (!content) {
        setState({
          status: "error",
          threadId: thread.id,
          message: t("summary.emptyResponse"),
        })
        return
      }
      setState({ status: "ready", threadId: thread.id, content })
    } catch (err: unknown) {
      if (runId.current !== id) return
      setState({
        status: "error",
        threadId: thread.id,
        message: err instanceof Error ? err.message : t("summary.failed"),
      })
    }
  }, [thread, messages])

  // translateTo — switch the view to `target` IMMEDIATELY (optimistic): a
  // cached copy renders at once, otherwise the still-readable original stays
  // on screen (dimmed) while the local model works. Requests are deduped
  // per language; a result landing after a regenerate is discarded. Errors
  // toast and fall back to the original: the user never loses the content.
  const translateTo = React.useCallback(
    (target: TranslationLanguage): void => {
      const base =
        state.status === "ready" && state.threadId === thread.id ? state.content : undefined
      if (!base) return
      setLang(target) // instant: cached text or dimmed original, never a block
      if (translations[target]) return
      const atGen = gen.current
      const pending = inflight.current[target]
      if (pending) return // already translating this language: UI swaps on arrival
      setTranslating(target)
      const request = (async (): Promise<string> => {
        const res = await aiService.chat(buildTranslation(base, target))
        const text = res.content.trim()
        if (!text) throw new Error("traduzione vuota")
        return text
      })()
      inflight.current[target] = request
      void request
        .then((text) => {
          if (gen.current !== atGen) return // regenerate happened: base changed
          setTranslations((prev) => ({ ...prev, [target]: text }))
        })
        .catch(() => {
          if (gen.current !== atGen) return
          if (langRef.current === target) setLang("it") // only if still watching it
          toast.error(t("summary.translateFailed"))
        })
        .finally(() => {
          delete inflight.current[target]
          setTranslating((cur) => (cur === target ? null : cur)) // never clears a newer one
        })
    },
    [state, thread.id, translations],
  )

  // Every open regenerates unless a summary for THIS thread is already ready.
  // `run` is intentionally omitted from the deps: it must fire once per open,
  // not on every thread/messages reference change.
  React.useEffect(() => {
    if (!open) return
    if (state.status === "ready" && state.threadId === thread.id) return
    void run()
  }, [open, thread.id])

  // "Thinking" renders on the very first frame the dialog opens: until a
  // result exists for THIS thread (idle, request in flight, or a stale state
  // from another thread) the modal shows the loading body — never an empty one.
  const loading =
    open && (state.status === "idle" || state.status === "loading" || state.threadId !== thread.id)
  const note = state.status === "loading" ? (state.note ?? "") : ""
  const base = state.status === "ready" && state.threadId === thread.id ? state.content : undefined
  const error = state.status === "error" && state.threadId === thread.id ? state.message : ""

  // the copy shown for the current language: base Italian or cached translate
  const shown = base ? (lang === "it" ? base : (translations[lang] ?? base)) : undefined
  // waiting UI only when the VIEW is the language still being translated
  const waiting = translating !== null && translating === lang

  // meta badges: what the model is being asked to read
  const pdfCount = pdfAttachments(messages).length
  const metaBadges = [
    messages.length === 1
      ? t("summary.messageCountOne")
      : t("summary.messageCountMany", { n: messages.length }),
    ...(pdfCount > 0 ? [`${pdfCount} PDF`] : []),
  ]

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          aria-label={t("summary.triggerTitle")}
          title={t("summary.triggerTitle")}
        >
          <Sparkles data-icon="inline-start" />
          {t("summary.trigger")}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <div className="flex items-center gap-2.5">
            <span
              aria-hidden="true"
              className="flex size-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary dark:bg-primary/20"
            >
              <Sparkles className="size-3.5" />
            </span>
            <div className="flex min-w-0 flex-col gap-0.5">
              <DialogTitle>{t("summary.title")}</DialogTitle>
              <DialogDescription className="truncate text-xs">{thread.subject}</DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-1.5">
          {metaBadges.map((b) => (
            <span
              key={b}
              className="inline-flex h-5 items-center rounded-4xl bg-muted px-2 text-xs font-medium text-muted-foreground"
            >
              {b}
            </span>
          ))}
        </div>

        {base && (
          <div
            className="flex items-center gap-2"
            role="group"
            aria-label={t("summary.languageGroup")}
          >
            <Languages aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
            <Select
              value={lang}
              onValueChange={(v) => {
                if (v === "it") setLang("it")
                else translateTo(v as TranslationLanguage)
              }}
            >
              <SelectTrigger size="sm" aria-label={t("summary.languageGroup")}>
                {translating && (
                  <Loader2
                    aria-hidden="true"
                    className="size-3.5 shrink-0 animate-spin text-muted-foreground"
                  />
                )}
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="it">{t("summary.original")}</SelectItem>
                {TRANSLATION_LANGUAGES.map((l) => (
                  <SelectItem key={l.code} value={l.code}>
                    {l.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}

        <ScrollArea className="-mx-1 max-h-[60vh] px-1">
          <div className="flex flex-col gap-3 py-1">
            {loading && (
              <>
                <Bubble
                  variant="muted"
                  className="w-fit animate-in fade-in slide-in-from-bottom-1 duration-200"
                >
                  <BubbleContent
                    className="flex items-center gap-1.5 px-3.5 py-2.5"
                    aria-hidden="true"
                  >
                    {[0, 150, 300].map((delay) => (
                      <span
                        key={delay}
                        className="size-1.5 animate-bounce rounded-full bg-muted-foreground/60"
                        style={{ animationDelay: delay + "ms" }}
                      />
                    ))}
                  </BubbleContent>
                </Bubble>
                <p className="text-xs text-muted-foreground" role="status" aria-live="polite">
                  {note || t("summary.thinking")}
                </p>
                {/* placeholder lines keep the layout stable when the summary
                    replaces them */}
                <div aria-hidden="true" className="flex flex-col gap-2 pt-1">
                  <Skeleton className="h-3 w-11/12" />
                  <Skeleton className="h-3 w-3/4" />
                  <Skeleton className="h-3 w-1/2" />
                </div>
              </>
            )}

            {!loading && error && (
              <div className="flex flex-col gap-2">
                <Bubble
                  variant="destructive"
                  className="animate-in fade-in slide-in-from-bottom-1 duration-200"
                >
                  <BubbleContent>{error}</BubbleContent>
                </Bubble>
                <Button variant="outline" size="sm" className="w-fit" onClick={() => void run()}>
                  {t("common.retry")}
                </Button>
              </div>
            )}

            {!loading && shown && (
              <>
                <Bubble
                  variant="muted"
                  className="w-full animate-in fade-in slide-in-from-bottom-1 duration-200"
                >
                  <BubbleContent className="text-[13px] leading-relaxed">
                    {waiting ? (
                      /* selected language not translated yet: skeleton lines
                         preview the incoming text (layout stays stable) */
                      <div aria-hidden="true" className="flex flex-col gap-2 py-0.5">
                        <Skeleton className="h-3 w-full" />
                        <Skeleton className="h-3 w-11/12" />
                        <Skeleton className="h-3 w-3/4" />
                        <Skeleton className="h-3 w-5/6" />
                      </div>
                    ) : (
                      <Markdown content={shown} />
                    )}
                  </BubbleContent>
                </Bubble>
                {waiting && (
                  <p
                    className="flex items-center gap-1.5 text-xs text-muted-foreground"
                    role="status"
                    aria-live="polite"
                  >
                    <Loader2 aria-hidden="true" className="size-3 animate-spin" />
                    {t("summary.translating")}
                  </p>
                )}
                {!waiting && (
                  <div className="flex justify-end">
                    <CopyButton
                      value={shown}
                      label={lang === "it" ? t("summary.copy") : t("summary.copyTranslation")}
                    />
                  </div>
                )}
              </>
            )}
          </div>
        </ScrollArea>

        {!loading && (
          <DialogFooter>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                void run()
              }}
            >
              {error ? t("common.retry") : t("summary.regenerate")}
            </Button>
            <Button variant="outline" size="sm" onClick={() => setOpen(false)}>
              {t("common.done")}
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  )
}
