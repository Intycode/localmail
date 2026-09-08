import * as React from "react"
import {
  ChevronUp,
  Clock,
  Lock,
  MessageSquareQuote,
  Paperclip,
  Send,
  ShieldCheck,
  Trash2,
  Save,
  X,
} from "lucide-react"
import { toast } from "sonner"

import type {
  Account,
  ComposeMessage,
  Contact,
  DraftInput,
  Message,
  OutgoingFile,
  Recipient,
} from "@/types"
import { getMailService } from "@/services/serviceRegistry"
import { isTauri } from "@/services/tauriMailService"
import { sendUndoSeconds } from "@/lib/settings"
import { dateLocale, useT } from "@/lib/i18n"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { PageBody, PageHeader } from "../PageHeader"
import { MessageBody } from "@/components/app/MessageBody"
import { whiteMessageBackground } from "@/lib/settings"
import {
  buildQuoteCtx,
  buildTextPart,
  composeHtmlBody,
  displayBody,
  restoreQuoteCtx,
  typedPart,
  type QuoteContext,
} from "@/lib/composeQuote"

interface ComposePageProps {
  /** Connected accounts; empty until a real account is connected. */
  accounts?: Account[]
}

const AUTOSAVE_MS = 30_000

/**
 * ComposePage — new message form. Validates recipients, sends through the
 * MailService, and returns to the originating folder. Honours
 * ?reply=<messageId> (prefill + In-Reply-To/References so the sent message
 * joins its thread) and ?draft=<id> (reopen a saved draft). Drafts are
 * saved local-first by the engine and auto-saved while writing.
 *
 * The editor is plain text and never hosts the quoted thread: a reply
 * starts empty and the original message is shown on demand (toggle below
 * the textarea) rendered exactly like the thread view. At send time the
 * engine receives text + html (multipart/alternative): the quote is woven
 * into both parts — "> " text quote and original HTML in a blockquote.
 */
export function ComposePage({ accounts = [] }: ComposePageProps) {
  const t = useT()
  const canSend = accounts.length > 0
  const [from, setFrom] = React.useState(
    accounts.find((a) => a.isActive)?.email ?? accounts[0]?.email ?? "",
  )
  const [to, setTo] = React.useState("")
  const [cc, setCc] = React.useState("")
  const [subject, setSubject] = React.useState("")
  const [body, setBody] = React.useState("")
  const [sending, setSending] = React.useState(false)
  const [savingDraft, setSavingDraft] = React.useState(false)
  const [dirty, setDirty] = React.useState(false)
  // compose context loaded from ?reply= / ?draft=
  const replyRef = React.useRef<Message | null>(null)
  // quote of the replied message: kept out of the editor, shown on demand
  // via the toggle and woven into the outgoing parts at send time
  const [quoteCtx, setQuoteCtx] = React.useState<QuoteContext | null>(null)
  const [quotePreview, setQuotePreview] = React.useState<Message | null>(null)
  const [showQuote, setShowQuote] = React.useState(false)
  const [sign, setSign] = React.useState(false)
  const [encrypt, setEncrypt] = React.useState(false)
  const [pgpReady, setPgpReady] = React.useState(false)
  // outgoing attachments: picked via the native dialog, sent by path so the
  // content never round-trips the IPC bridge (engine reads them at send time)
  const [files, setFiles] = React.useState<OutgoingFile[]>([])
  const [picking, setPicking] = React.useState(false)
  // compose autocomplete: recipients from the local mail history (the query
  // is the whole field; the engine reads the last comma-separated entry)
  const [contacts, setContacts] = React.useState<Contact[]>([])
  const draftIdRef = React.useRef<string | null>(null)
  const [contextLoaded, setContextLoaded] = React.useState(true)
  const dirtyRef = React.useRef(false)
  dirtyRef.current = dirty

  const markDirty = (): void => setDirty(true)

  // M9: check whether the active account has a PGP private key in the vault
  React.useEffect(() => {
    let alive = true
    void (async () => {
      try {
        const acc = accounts.find((a) => a.isActive) ?? accounts[0]
        if (!acc) {
          if (alive) setPgpReady(false)
          return
        }
        const svc = await getMailService()
        if (!svc.pgpStatus) {
          if (alive) setPgpReady(false)
          return
        }
        const st = await svc.pgpStatus(acc.id)
        if (alive) setPgpReady(st.hasKey)
      } catch {
        if (alive) setPgpReady(false)
      }
    })()
    return () => {
      alive = false
    }
    // accounts is stable here
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Reply / draft prefill.
  React.useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const replyId = params.get("reply")
    const draftId = params.get("draft")
    if (!replyId && !draftId) return
    let alive = true
    setContextLoaded(false)
    getMailService()
      .then(async (svc) => {
        if (replyId) {
          const m: Message = await svc.getMessage(replyId)
          if (!alive) return
          replyRef.current = m
          // the quote is never hosted in the editor: it goes out at send
          // time and is shown on demand, rendered like the thread view
          setQuoteCtx(buildQuoteCtx(m))
          setQuotePreview(m)
          setShowQuote(false)
          const own = new Set(accounts.map((a) => a.email.toLowerCase()))
          setTo(addrList([m.from], own))
          setCc(addrList(m.cc, own))
          setSubject(reSubject(m.subject))
          setBody("")
        } else if (draftId) {
          const m: Message = await svc.getMessage(draftId)
          if (!alive) return
          draftIdRef.current = draftId
          setTo(addrList(m.to, new Set()))
          setCc(addrList(m.cc, new Set()))
          setSubject(m.subject)
          const ctx = restoreQuoteCtx(m)
          setQuoteCtx(ctx)
          if (ctx) {
            // only the typed part goes back into the editor: the quote is
            // rebuilt on send and viewable through the toggle
            setBody(typedPart(displayBody(m), ctx).trimEnd())
            setQuotePreview({ ...m, id: `${m.id}-quote`, body: ctx.html, bodyText: ctx.text })
            setShowQuote(false)
          } else {
            setBody(displayBody(m))
          }
        }
      })
      .catch(() => {
        if (alive) toast.error(replyId ? t("compose.replyNotFound") : t("compose.draftNotFound"))
      })
      .finally(() => {
        if (alive) setContextLoaded(true)
      })
    return () => {
      alive = false
    }
    // accounts is stable across the component lifetime here
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function saveDraftNow(silent: boolean): Promise<string | null> {
    if (!canSend) {
      if (!silent) toast.error(t("compose.needAccountForDraft"))
      return null
    }
    setSavingDraft(true)
    try {
      const svc = await getMailService()
      const htmlBody = composeHtmlBody(body, quoteCtx)
      const draft: DraftInput = {
        ...(draftIdRef.current ? { id: draftIdRef.current } : {}),
        to: parseRecipients(to),
        cc: parseRecipients(cc),
        bcc: [],
        subject: subject.trim(),
        body: buildTextPart(body, quoteCtx),
        // kept in the draft so a reopened draft still quotes the original
        // message in HTML when sent later
        ...(htmlBody ? { htmlBody } : {}),
      }
      const id = await svc.saveDraft(draft)
      draftIdRef.current = id
      setDirty(false)
      if (!silent) toast.success(t("compose.draftSaved"))
      return id
    } catch (err: unknown) {
      if (!silent) toast.error(err instanceof Error ? err.message : t("compose.draftSaveFailed"))
      return null
    } finally {
      setSavingDraft(false)
    }
  }

  // auto-save while writing
  React.useEffect(() => {
    const timer = setInterval(() => {
      if (dirtyRef.current) void saveDraftNow(true)
    }, AUTOSAVE_MS)
    return () => clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canSend, to, cc, subject, body])

  // autocomplete suggestions, debounced, from the local history
  React.useEffect(() => {
    const query = to || cc
    if (!query.trim()) {
      setContacts([])
      return
    }
    const t = setTimeout(() => {
      void (async () => {
        try {
          const svc = await getMailService()
          if (!svc.suggestContacts) return
          setContacts(await svc.suggestContacts(query))
        } catch {
          // suggestions are best-effort: never disturb typing
        }
      })()
    }, 150)
    return () => clearTimeout(t)
  }, [to, cc])

  /** Opens the native multi-file dialog and appends the picks (dedup by
   *  path). Browser dev has no paths: the engine would have nothing to
   *  read, so the feature is desktop-only with a clear message. */
  async function attach(): Promise<void> {
    if (!isTauri()) {
      toast.error(t("compose.attachDesktopOnly"))
      return
    }
    setPicking(true)
    try {
      const { open } = await import("@tauri-apps/plugin-dialog")
      const picked = await open({ multiple: true, title: t("compose.attachDialogTitle") })
      if (!picked) return
      const paths = Array.isArray(picked) ? picked : [picked]
      setFiles((prev) => {
        const seen = new Set(prev.map((f) => f.path))
        const next = [...prev]
        for (const p of paths) {
          if (seen.has(p)) continue
          seen.add(p)
          next.push({ path: p, name: p.split(/[\\/]/).pop() })
        }
        return next
      })
      markDirty()
    } catch {
      toast.error(t("compose.pickFailed"))
    } finally {
      setPicking(false)
    }
  }

  function removeFile(path: string): void {
    setFiles((prev) => prev.filter((f) => f.path !== path))
    markDirty()
  }

  async function send(options: { sendAt?: string } = {}): Promise<void> {
    const toList = parseRecipients(to)
    if (toList.length === 0) {
      toast.error(t("compose.needRecipient"))
      return
    }
    if (encrypt && files.length > 0) {
      toast.error(t("compose.encryptOnlyText"))
      return
    }
    setSending(true)
    const reply = replyRef.current
    const editingDraft = draftIdRef.current
    const htmlBody = composeHtmlBody(body, quoteCtx)
    // undo-send: the configured window parks the message in the engine's
    // outbox queue; the toast's "Annulla" action cancels it. A user-chosen
    // sendAt (Programma invio) is used as-is.
    const undoSec = options.sendAt ? 0 : sendUndoSeconds()
    const sendAt =
      options.sendAt ??
      (undoSec > 0 ? new Date(Date.now() + undoSec * 1000).toISOString() : undefined)
    const message: ComposeMessage = {
      to: toList,
      cc: parseRecipients(cc),
      bcc: [],
      subject: subject.trim() || t("inbox.noSubject"),
      body: buildTextPart(body, quoteCtx).trim(),
      // text + html go out as multipart/alternative: the quoted previous
      // message reaches the recipient in its original HTML form
      ...(htmlBody ? { htmlBody } : {}),
      attachments: [],
      ...(files.length > 0 ? { files } : {}),
      ...(sendAt ? { sendAt } : {}),
      ...(sign || encrypt ? { sign, encrypt } : {}),
      ...(reply && reply.messageId
        ? {
            inReplyTo: reply.messageId,
            references: [...(reply.references ?? []), reply.messageId],
          }
        : {}),
    }
    try {
      const svc = await getMailService()
      const result = await svc.sendMessage(message)
      // the draft is now a sent message: drop the local copy. With a
      // queued send (undo window / scheduled) the draft stays: if the user
      // cancels, the text is still there.
      if (editingDraft && !result?.opId) void svc.deleteDraft(editingDraft).catch(() => {})
      if (result?.scheduled && options.sendAt) {
        toast.success(t("compose.scheduled"), {
          description: t("compose.scheduledDesc", {
            date: new Date(options.sendAt).toLocaleString(dateLocale()),
          }),
        })
      } else if (result?.opId) {
        toast.success(t("compose.queuedUndo", { sec: undoSec }), {
          description: t("compose.queuedUndoDesc"),
          duration: undoSec * 1000,
          action: {
            label: t("common.cancel"),
            onClick: () => {
              void svc.cancelQueuedOp?.(result.opId!).catch(() => {})
              toast.success(t("compose.sendCancelled"))
            },
          },
        })
      } else if (result?.queued) {
        toast.success(t("compose.queuedOffline"))
      } else {
        toast.success(t("compose.sent"))
      }
      setTimeout(
        () => {
          if (reply?.threadId) {
            window.location.href = `/thread?id=${encodeURIComponent(reply.threadId)}&folder=${encodeURIComponent(reply.folderId)}`
          } else {
            window.location.href = "/"
          }
        },
        result?.opId ? 600 : 600,
      )
    } catch (err: unknown) {
      setSending(false)
      toast.error(err instanceof Error ? err.message : t("compose.sendFailed"))
    }
  }

  /** Presets for the scheduled send: the engine parks the message in the
   *  outbox until the chosen moment (cancellable from the outbox list). */
  const schedulePresets: { label: string; ms: number }[] = [
    { label: t("compose.scheduleIn1h"), ms: 3_600_000 },
    { label: t("compose.scheduleIn1d"), ms: 86_400_000 },
    { label: t("compose.scheduleIn1w"), ms: 7 * 86_400_000 },
  ]

  async function discard(): Promise<void> {
    if (dirty) {
      const ok = window.confirm(t("compose.discardConfirm"))
      if (!ok) return
    }
    if (draftIdRef.current) {
      try {
        const svc = await getMailService()
        await svc.deleteDraft(draftIdRef.current)
      } catch {
        // best effort: the user asked to leave anyway
      }
    }
    window.location.href = "/"
  }

  const title = replyRef.current
    ? t("compose.replyTitle")
    : draftIdRef.current
      ? t("compose.draftTitle")
      : t("compose.newTitle")

  return (
    <PageBody>
      <PageHeader
        title={title}
        description={
          canSend ? t("compose.description", { account: from || "—" }) : t("compose.needAccount")
        }
      />
      <FieldGroup>
        <Field orientation="horizontal">
          <FieldLabel htmlFor="c-from" className="w-16 shrink-0">
            {t("compose.from")}
          </FieldLabel>
          {canSend ? (
            <Select
              value={from}
              onValueChange={(v) => {
                setFrom(v)
                markDirty()
              }}
            >
              <SelectTrigger id="c-from" className="w-full">
                <SelectValue placeholder={t("compose.fromPlaceholder")} />
              </SelectTrigger>
              <SelectContent>
                {accounts.map((a) => (
                  <SelectItem key={a.id} value={a.email}>
                    {a.name} · {a.email}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <Input id="c-from" value={from} placeholder={t("compose.noAccount")} disabled />
          )}
        </Field>
        <datalist id="contacts-suggestions">
          {contacts.map((c) => (
            <option key={c.email} value={c.email} label={c.name || c.email} />
          ))}
        </datalist>
        <Field orientation="horizontal">
          <FieldLabel htmlFor="c-to" className="w-16 shrink-0">
            {t("compose.to")}
          </FieldLabel>
          <Input
            id="c-to"
            value={to}
            onChange={(e) => {
              setTo(e.target.value)
              markDirty()
            }}
            placeholder={t("compose.toPlaceholder")}
            list="contacts-suggestions"
            autoComplete="off"
            autoFocus
          />
        </Field>
        <Field orientation="horizontal">
          <FieldLabel htmlFor="c-cc" className="w-16 shrink-0">
            {t("compose.cc")}
          </FieldLabel>
          <Input
            id="c-cc"
            value={cc}
            onChange={(e) => {
              setCc(e.target.value)
              markDirty()
            }}
            placeholder={t("compose.ccPlaceholder")}
            list="contacts-suggestions"
            autoComplete="off"
          />
        </Field>
        <Field orientation="horizontal">
          <FieldLabel htmlFor="c-subject" className="w-16 shrink-0">
            {t("compose.subject")}
          </FieldLabel>
          <Input
            id="c-subject"
            value={subject}
            onChange={(e) => {
              setSubject(e.target.value)
              markDirty()
            }}
            placeholder={t("compose.subjectPlaceholder")}
          />
        </Field>
        <Field orientation="horizontal">
          <FieldLabel htmlFor="c-body" className="w-16 shrink-0">
            {t("compose.body")}
          </FieldLabel>
          <Textarea
            id="c-body"
            value={body}
            onChange={(e) => {
              setBody(e.target.value)
              markDirty()
            }}
            placeholder={t("compose.bodyPlaceholder")}
            className="min-h-52"
          />
        </Field>
        {quoteCtx && quotePreview && (
          <div className="flex flex-col gap-1">
            <Button
              variant="ghost"
              size="sm"
              className="-ml-2 self-start"
              onClick={() => setShowQuote(!showQuote)}
              aria-expanded={showQuote}
            >
              {showQuote ? (
                <ChevronUp data-icon="inline-start" />
              ) : (
                <MessageSquareQuote data-icon="inline-start" />
              )}
              {showQuote ? t("compose.hideQuote") : t("compose.showQuote")}
            </Button>
            {showQuote && (
              <div className="max-h-72 overflow-y-auto rounded-md border bg-muted/30 p-3">
                <MessageBody message={quotePreview} whiteBg={whiteMessageBackground()} />
              </div>
            )}
          </div>
        )}
        {files.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {files.map((f) => (
              <span
                key={f.path}
                className="inline-flex max-w-64 items-center gap-1 rounded-md border bg-muted/40 px-2 py-1 text-xs"
                title={f.path}
              >
                <Paperclip className="size-3 shrink-0" />
                <span className="truncate">{f.name ?? f.path}</span>
                <button
                  type="button"
                  onClick={() => removeFile(f.path)}
                  aria-label={t("compose.removeAttachment", { name: f.name ?? f.path })}
                  className="rounded-sm p-0.5 hover:bg-muted"
                >
                  <X className="size-3" />
                </button>
              </span>
            ))}
          </div>
        )}
        <div className="flex flex-wrap items-center gap-2 pt-1">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => void attach()}
            disabled={picking}
            title={t("compose.attachTitle")}
          >
            <Paperclip data-icon="inline-start" />
            {picking ? t("compose.picking") : t("compose.attach")}
          </Button>
          {pgpReady && (
            <>
              <Button
                type="button"
                variant={sign ? "default" : "outline"}
                size="sm"
                onClick={() => setSign(!sign)}
                aria-pressed={sign}
                title={t("compose.signTitle")}
              >
                <ShieldCheck data-icon="inline-start" />
                {t("compose.sign")}
              </Button>
              <Button
                type="button"
                variant={encrypt ? "default" : "outline"}
                size="sm"
                onClick={() => setEncrypt(!encrypt)}
                aria-pressed={encrypt}
                disabled={files.length > 0}
                title={
                  files.length > 0 ? t("compose.encryptWithAttachments") : t("compose.encryptTitle")
                }
              >
                <Lock data-icon="inline-start" />
                {t("compose.encrypt")}
              </Button>
            </>
          )}
          <Button onClick={() => void send()} disabled={!canSend || sending || !contextLoaded}>
            <Send data-icon="inline-start" />
            {sending ? t("compose.sending") : t("compose.send")}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="outline"
                disabled={!canSend || sending || !contextLoaded}
                aria-label={t("compose.schedule")}
                title={t("compose.schedule")}
              >
                <Clock data-icon="inline-start" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              {schedulePresets.map((p) => (
                <DropdownMenuItem
                  key={p.label}
                  onClick={() => void send({ sendAt: new Date(Date.now() + p.ms).toISOString() })}
                >
                  {p.label}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
          <Button
            variant="outline"
            onClick={() => void saveDraftNow(false)}
            disabled={!canSend || savingDraft || !contextLoaded}
          >
            <Save data-icon="inline-start" />
            {savingDraft ? t("compose.savingDraft") : t("compose.saveDraft")}
          </Button>
          <Button variant="destructive" onClick={() => void discard()}>
            <Trash2 data-icon="inline-start" />
            {t("compose.discard")}
          </Button>
        </div>
      </FieldGroup>
    </PageBody>
  )
}

/** "Re: x" without stacking prefixes. */
function reSubject(subject: string): string {
  const s = (subject ?? "").trim()
  return /^re\s*:/i.test(s) ? s : `Re: ${s}`
}

/** Comma-separated addresses excluding the ones in `skip`. */
function addrList(rs: Recipient[], skip: Set<string>): string {
  return rs
    .filter((r) => r.email && !skip.has(r.email.toLowerCase()))
    .map((r) => (r.name ? `${r.name} <${r.email}>` : r.email))
    .join(", ")
}

function parseRecipients(value: string): Recipient[] {
  return value
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => {
      const m = s.match(/^\s*(.*?)\s*<([^>]+)>$/) ?? s.match(/([^<@]+@[^>]+)/)
      const email = m ? (m[2] ?? m[1] ?? m[0]) : s
      const name = m && m[1] && m[1] !== email ? m[1].trim() : ""
      return { name, email }
    })
}
