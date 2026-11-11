import * as React from "react"
import {
  Archive,
  ArrowLeft,
  Clock,
  Download,
  ExternalLink,
  Eye,
  MailCheck,
  MailOpen,
  MoonStar,
  Pencil,
  Reply,
  ShieldAlert,
  ShieldCheck,
  SunMedium,
  Undo2,
} from "lucide-react"
import { toast } from "sonner"

import { navigate } from "@/lib/router"
import { cn } from "@/lib/utils"
import { formatBytes, formatDate, initials } from "@/lib/format"
import { snoozePresets } from "@/lib/snooze"
import { setWhiteMessageBackground, whiteMessageBackground } from "@/lib/settings"
import { dateLocale, t, useT } from "@/lib/i18n"
import { getMailService } from "@/services/serviceRegistry"
import {
  markThreadRead as markThreadReadInStore,
  markThreadUnread as markThreadUnreadInStore,
} from "@/stores/threadStore"
import type { Message, Thread } from "@/types"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { MessageBody } from "@/components/app/MessageBody"
import { CopyButton } from "@/components/app/CopyButton"
import { SummaryDialog } from "@/components/app/SummaryDialog"
import { Separator } from "@/components/ui/separator"

interface ThreadPageProps {
  thread: Thread
  messages: Message[]
  /** true when the thread lives in the drafts folder: actions change */
  isDraft?: boolean
  /** type of the folder the thread lives in: switches the spam actions */
  folderType?: string
  /** rendered inside the inbox reading pane (no back button) */
  embedded?: boolean
}

/** ThreadPage — a single thread: header with compact actions, then messages. */
export function ThreadPage({ thread, messages, isDraft, folderType, embedded }: ThreadPageProps) {
  const t = useT()
  const [busy, setBusy] = React.useState(false)
  const [whiteBg, setWhiteBg] = React.useState(whiteMessageBackground)
  const last = messages[messages.length - 1]
  // earlier messages start collapsed to one line; the latest is open
  const [openIds, setOpenIds] = React.useState(() => new Set(last ? [last.id] : []))

  function toggleWhiteBg(): void {
    const next = !whiteBg
    setWhiteBg(next)
    setWhiteMessageBackground(next)
  }

  /** Thread-level mark read (replaces the old first-message-only action). */
  async function markRead(): Promise<void> {
    const svc = await getMailService()
    try {
      await svc.markThreadRead(thread.id)
      markThreadReadInStore(thread.id)
      toast.success(t("thread.markedRead"))
    } catch {
      toast.error(t("common.failed"))
    }
  }

  /** Mark unread and go back to the list so the unread dot is visible. */
  async function markUnread(): Promise<void> {
    const svc = await getMailService()
    try {
      await svc.markThreadUnread(thread.id)
      markThreadUnreadInStore(thread.id)
      toast.success(t("thread.markedUnread"))
      navigate(`/?folder=${encodeURIComponent(thread.folderId)}`)
    } catch {
      toast.error(t("common.failed"))
    }
  }

  async function archive(): Promise<void> {
    setBusy(true)
    const svc = await getMailService()
    try {
      await svc.archiveThread(thread.id)
      navigate(`/?folder=${encodeURIComponent(thread.folderId)}`)
    } catch {
      setBusy(false)
      toast.error(t("thread.archiveFailed"))
    }
  }

  /** Folder-typed spam actions: "Segnala spam" moves the thread into the
   *  account's junk folder; inside Junk the button becomes "Non spam" and
   *  moves it back to the inbox. Both go through the generic thread.move. */
  async function moveToType(targetType: "junk" | "inbox", successMsg: string): Promise<void> {
    setBusy(true)
    const svc = await getMailService()
    try {
      const folders = thread.accountId ? await svc.getFolders(thread.accountId) : []
      const target = folders.find((f) => f.type === targetType)
      if (!target) {
        toast.error(
          t("thread.folderNotFound", {
            folder: targetType === "junk" ? t("folder.junk") : t("folder.inbox"),
          }),
        )
        setBusy(false)
        return
      }
      await svc.moveThread(thread.id, target.id)
      navigate(`/?folder=${encodeURIComponent(thread.folderId)}`)
      toast.success(successMsg)
    } catch {
      setBusy(false)
      toast.error(t("thread.moveFailed"))
    }
  }

  /** Defers the thread: it leaves the list and comes back at the chosen
   *  moment (engine-local scheduling, no IMAP involved). */
  async function snooze(until: string): Promise<void> {
    setBusy(true)
    const svc = await getMailService()
    try {
      await svc.snoozeThread(thread.id, until)
      navigate(`/?folder=${encodeURIComponent(thread.folderId)}`)
      toast.success(t("thread.snoozed"), {
        description: t("thread.snoozedDesc", {
          date: new Date(until).toLocaleString(dateLocale()),
        }),
      })
    } catch {
      setBusy(false)
      toast.error(t("thread.snoozeFailed"))
    }
  }

  /** Restores a snoozed thread right away. */
  async function unsnooze(): Promise<void> {
    setBusy(true)
    const svc = await getMailService()
    try {
      await svc.unsnoozeThread(thread.id)
      navigate(`/?folder=${encodeURIComponent(thread.folderId)}`)
      toast.success(t("thread.restored"))
    } catch {
      setBusy(false)
      toast.error(t("thread.restoreFailed"))
    }
  }

  return (
    <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto">
      <div className="sticky top-0 z-10 flex flex-wrap items-center gap-1 border-b border-border bg-background/95 px-4 py-2 backdrop-blur">
        {!embedded && (
          <Button asChild variant="ghost" size="sm" className="-ml-1">
            <a href={`/?folder=${encodeURIComponent(thread.folderId)}`}>
              <ArrowLeft />
              {t("common.back")}
            </a>
          </Button>
        )}
        <div className={cn("flex flex-wrap items-center gap-0.5", !embedded && "ml-auto")}>
          {isDraft ? (
            <Button asChild size="sm">
              <a href={`/compose?draft=${encodeURIComponent(last?.id ?? "")}`}>
                <Pencil data-icon="inline-start" />
                {t("thread.editDraft")}
              </a>
            </Button>
          ) : (
            <Button asChild size="sm">
              <a href={replyHref(last?.id ?? "")}>
                <Reply data-icon="inline-start" />
                {t("thread.reply")}
              </a>
            </Button>
          )}
          {!isDraft && messages.length > 0 && <SummaryDialog thread={thread} messages={messages} />}
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={archive}
            disabled={busy}
            aria-label={t("thread.archive")}
            title={t("thread.archive")}
          >
            <Archive />
          </Button>
          {!isDraft && folderType !== "junk" && (
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => void moveToType("junk", t("thread.reportedSpam"))}
              disabled={busy}
              aria-label={t("thread.reportSpam")}
              title={t("thread.reportSpam")}
            >
              <ShieldAlert />
            </Button>
          )}
          {!isDraft && folderType === "junk" && (
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => void moveToType("inbox", t("thread.notSpamMoved"))}
              disabled={busy}
              aria-label={t("thread.notSpam")}
              title={t("thread.notSpamTitle")}
            >
              <ShieldCheck />
            </Button>
          )}
          {!isDraft && folderType === "snoozed" && (
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={unsnooze}
              disabled={busy}
              aria-label={t("thread.restoreNow")}
              title={t("thread.restoreNow")}
            >
              <Undo2 />
            </Button>
          )}
          {!isDraft && folderType !== "snoozed" && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  disabled={busy}
                  aria-label={t("thread.snooze")}
                  title={t("thread.snooze")}
                >
                  <Clock />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {snoozePresets().map((p) => (
                  <DropdownMenuItem key={p.label} onClick={() => void snooze(p.until)}>
                    {p.label}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          {thread.isUnread ? (
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={markRead}
              aria-label={t("thread.markRead")}
              title={t("thread.markRead")}
            >
              <MailCheck />
            </Button>
          ) : (
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={markUnread}
              aria-label={t("thread.markUnread")}
              title={t("thread.markUnread")}
            >
              <MailOpen />
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={toggleWhiteBg}
            aria-pressed={whiteBg}
            aria-label={t("thread.whiteBg")}
            title={whiteBg ? t("thread.whiteBgOff") : t("thread.whiteBgOn")}
          >
            {whiteBg ? <MoonStar /> : <SunMedium />}
          </Button>
        </div>
      </div>

      <div className={cn("px-5 pt-6 pb-10", !embedded && "mx-auto max-w-3xl")}>
        <div className="mb-5 min-w-0">
          <h2 className="text-xl font-semibold tracking-tight">{thread.subject}</h2>
          <p className="mt-1 text-[13px] text-muted-foreground">
            {thread.from.name || thread.from.email}
            {thread.messageCount > 1 &&
              " · " + t("thread.messageCountMany", { n: thread.messageCount })}
          </p>
        </div>

        <div className="flex flex-col gap-2">
          {messages.map((m) =>
            openIds.has(m.id) ? (
              <MessageCard key={m.id} message={m} whiteBg={whiteBg} />
            ) : (
              <button
                key={m.id}
                type="button"
                aria-expanded={false}
                onClick={() => setOpenIds((prev) => new Set(prev).add(m.id))}
                className="flex w-full items-center gap-3 rounded-xl border border-border bg-muted/30 px-4 py-2.5 text-left text-sm transition-colors hover:bg-accent focus-visible:outline-2 focus-visible:outline-ring"
              >
                <SenderAvatar name={m.from.name || m.from.email} />
                <span className="w-36 shrink-0 truncate font-medium">
                  {m.from.name || m.from.email}
                </span>
                <span className="min-w-0 flex-1 truncate text-muted-foreground">
                  {m.bodyText.slice(0, 200)}
                </span>
                <time className="shrink-0 text-xs text-muted-foreground tabular-nums">
                  {formatDate(m.date)}
                </time>
              </button>
            ),
          )}
        </div>
      </div>
    </div>
  )
}

function SenderAvatar({ name }: { name: string }) {
  return (
    <span
      aria-hidden="true"
      className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary"
    >
      {initials(name)}
    </span>
  )
}

function MessageCard({ message, whiteBg }: { message: Message; whiteBg: boolean }) {
  const t = useT()
  /** Read flow: download into the cache dir, then open with the OS default
   *  application (PDF viewer, image viewer, office suite...). */
  async function openAttachment(messageId: string, accountId: string, name: string): Promise<void> {
    try {
      const { openAttachment } = await import("@/services/tauriMailService")
      await openAttachment(accountId, messageId, name)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("thread.openFailed"))
    }
  }

  /** One-click download into the system Downloads folder (no dialog). */
  async function downloadAttachment(
    messageId: string,
    accountId: string,
    name: string,
  ): Promise<void> {
    try {
      const { downloadAttachment } = await import("@/services/tauriMailService")
      const dest = await downloadAttachment(accountId, messageId, name)
      toast.success(t("thread.downloaded", { dest }))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("thread.downloadFailed"))
    }
  }

  /** Inline image preview (attachment.preview, engine-side type sniffing). */
  const [imagePreview, setImagePreview] = React.useState<{ name: string; url: string } | null>(null)
  async function previewAttachment(
    messageId: string,
    accountId: string,
    name: string,
  ): Promise<void> {
    try {
      const { attachmentService } = await import("@/services/tauriMailService")
      const res = await attachmentService.preview(accountId, messageId, name)
      setImagePreview({ name, url: `data:${res.mime};base64,${res.data}` })
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("thread.previewFailed"))
    }
  }

  return (
    <Card size="sm">
      <CardContent className="flex flex-col gap-2">
        <div className="flex items-center gap-1.5">
          <SenderAvatar name={message.from.name || message.from.email} />
          <span className="min-w-0 truncate font-medium">{message.from.name}</span>
          <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">
            {message.from.email}
          </span>
          <CopyButton value={message.from.email} label={t("thread.copyEmail")} />
          <span className="ml-auto flex shrink-0 items-center gap-1">
            <time className="text-xs tabular-nums text-muted-foreground">
              {formatDate(message.date)}
            </time>
            <Button
              asChild
              variant="ghost"
              size="icon-sm"
              aria-label={t("thread.replyToMessage")}
              title={t("thread.replyToMessage")}
            >
              <a href={replyHref(message.id)}>
                <Reply />
              </a>
            </Button>
          </span>
        </div>
        <Separator />
        {(message.isEncrypted || message.isSigned) && (
          <div className="flex flex-wrap items-center gap-2 text-xs">
            {message.isEncrypted && (
              <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/10 px-2 py-0.5 font-medium text-emerald-700 dark:text-emerald-400">
                {t("thread.encryptedPgp")}
              </span>
            )}
            {message.isSigned && message.sigStatus === "valid" && (
              <span className="inline-flex items-center gap-1 rounded-full bg-sky-500/10 px-2 py-0.5 font-medium text-sky-700 dark:text-sky-400">
                {t("thread.signatureValid")}
              </span>
            )}
            {message.isSigned && message.sigStatus === "invalid" && (
              <span className="inline-flex items-center gap-1 rounded-full bg-destructive/10 px-2 py-0.5 font-medium text-destructive">
                {t("thread.signatureInvalid")}
              </span>
            )}
            {message.isSigned && (message.sigStatus === "unknown" || !message.sigStatus) && (
              <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 font-medium text-muted-foreground">
                {t("thread.signatureUnknown")}
              </span>
            )}
          </div>
        )}
        {message.phishing && message.phishing.length > 0 && (
          <div
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs leading-relaxed text-destructive"
          >
            <p className="font-semibold">{t("thread.phishing")}</p>
            <ul className="mt-1 list-disc pl-4">
              {message.phishing.map((flag, i) => (
                <li key={i}>{flag}</li>
              ))}
            </ul>
          </div>
        )}
        {message.spamScore >= 35 && (
          <p className="text-[11px] text-muted-foreground/80">
            {t("thread.spamScore", { score: Math.round(message.spamScore) })}{" "}
            {message.spamScore >= 60
              ? t("thread.spamScoreLikely")
              : t("thread.spamScoreSuspicious")}
          </p>
        )}
        <MessageBody message={message} whiteBg={whiteBg} />
        {message.attachments?.length > 0 && (
          <div className="flex flex-col gap-1.5 pt-1">
            <p className="text-xs font-medium tracking-wide text-muted-foreground">
              {message.attachments.length === 1
                ? t("thread.attachmentOne")
                : t("thread.attachmentMany", { n: message.attachments.length })}
            </p>
            {message.attachments.map((a) => {
              const info = attachmentInfo(a.name, a.contentType)
              const open = () => void openAttachment(message.id, message.accountId, a.name)
              return (
                <div
                  key={a.id}
                  role="button"
                  tabIndex={0}
                  onClick={open}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault()
                      open()
                    }
                  }}
                  title={t("thread.openWithDefault", { name: a.name })}
                  className="group flex cursor-pointer items-center gap-3 rounded-lg border border-border bg-muted/30 p-2.5 transition-colors hover:border-ring/40 hover:bg-accent focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
                >
                  <span
                    aria-hidden="true"
                    className={cn(
                      "flex size-10 shrink-0 items-center justify-center rounded-md font-mono text-[11px] font-bold tracking-wide",
                      info.classes,
                    )}
                  >
                    {info.label}
                  </span>
                  <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span className="truncate text-sm font-medium">{a.name}</span>
                    <span className="font-mono text-xs text-muted-foreground">
                      {formatBytes(a.size)}
                      {info.kind !== "" && <span className="ml-2 not-italic">{info.kind}</span>}
                    </span>
                  </span>
                  <span className="flex shrink-0 items-center gap-1 opacity-70 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
                    {isImageAttachment(a.name, a.contentType) && (
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t("thread.previewOf", { name: a.name })}
                        title={t("thread.preview")}
                        onClick={(e) => {
                          e.stopPropagation()
                          void previewAttachment(message.id, message.accountId, a.name)
                        }}
                      >
                        <Eye />
                      </Button>
                    )}
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={t("thread.openWithDefault", { name: a.name })}
                      title={t("thread.openWithDefault", { name: a.name })}
                      onClick={(e) => {
                        e.stopPropagation()
                        void openAttachment(message.id, message.accountId, a.name)
                      }}
                    >
                      <ExternalLink />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={t("thread.download", { name: a.name })}
                      title={t("thread.downloadTitle")}
                      onClick={(e) => {
                        e.stopPropagation()
                        void downloadAttachment(message.id, message.accountId, a.name)
                      }}
                    >
                      <Download />
                    </Button>
                  </span>
                </div>
              )
            })}
          </div>
        )}
        <Dialog open={imagePreview !== null} onOpenChange={(o) => !o && setImagePreview(null)}>
          <DialogContent className="max-w-2xl">
            <DialogHeader>
              <DialogTitle className="truncate pr-6">{imagePreview?.name}</DialogTitle>
            </DialogHeader>
            {imagePreview && (
              <img
                src={imagePreview.url}
                alt={t("thread.previewOf", { name: imagePreview.name })}
                className="max-h-[70vh] w-auto rounded-md object-contain"
              />
            )}
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  )
}

function replyHref(messageId: string): string {
  return `/compose?reply=${encodeURIComponent(messageId)}`
}

/** True for image attachments the inline viewer can render (the engine
 *  re-checks with http.DetectContentType before serving bytes). */
function isImageAttachment(name: string, contentType: string): boolean {
  if (contentType.startsWith("image/")) return true
  return /\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(name)
}

/** Tint classes per attachment family (used with the kind badge). */
const ATTACHMENT_TINTS = {
  red: "bg-red-500/10 text-red-600 dark:text-red-400",
  amber: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
  violet: "bg-violet-500/10 text-violet-600 dark:text-violet-400",
  blue: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
  green: "bg-green-500/10 text-green-600 dark:text-green-400",
  orange: "bg-orange-500/10 text-orange-600 dark:text-orange-400",
  pink: "bg-pink-500/10 text-pink-600 dark:text-pink-400",
  purple: "bg-purple-500/10 text-purple-600 dark:text-purple-400",
  gray: "bg-muted text-muted-foreground",
} as const

/**
 * attachmentInfo — file-type presentation for an attachment: a short badge
 * label (PDF, ZIP, PNG...) from the extension or MIME type, its tint and a
 * human kind for the secondary line ("" when the extension is self-evident).
 */
function attachmentInfo(
  name: string,
  contentType?: string,
): {
  label: string
  classes: string
  kind: string
} {
  const ext = (name.includes(".") ? name.split(".").pop() : "")?.toLowerCase() ?? ""
  const mime = (contentType ?? "").toLowerCase()
  if (mime.includes("pdf") || ext === "pdf") {
    return { label: "PDF", classes: ATTACHMENT_TINTS.red, kind: t("attachment.pdf") }
  }
  if (mime.includes("zip") || ["zip", "rar", "7z", "tar", "gz", "bz2", "xz", "dmg"].includes(ext)) {
    const label = ["gz", "bz2", "xz"].includes(ext)
      ? "ARC"
      : (ext || "zip").slice(0, 4).toUpperCase()
    return { label, classes: ATTACHMENT_TINTS.amber, kind: t("attachment.archive") }
  }
  if (
    mime.startsWith("image/") ||
    ["png", "jpg", "jpeg", "gif", "webp", "svg", "heic", "avif", "bmp", "tiff"].includes(ext)
  ) {
    return {
      label: (ext || "img").slice(0, 4).toUpperCase(),
      classes: ATTACHMENT_TINTS.violet,
      kind: t("attachment.image"),
    }
  }
  if (["doc", "docx", "odt", "rtf"].includes(ext) || mime.includes("word")) {
    return { label: "DOC", classes: ATTACHMENT_TINTS.blue, kind: t("attachment.document") }
  }
  if (
    ["xls", "xlsx", "csv", "ods"].includes(ext) ||
    mime.includes("spreadsheet") ||
    mime.includes("excel")
  ) {
    const label = ext === "csv" ? "CSV" : "XLS"
    return { label, classes: ATTACHMENT_TINTS.green, kind: t("attachment.spreadsheet") }
  }
  if (["ppt", "pptx", "odp", "key"].includes(ext) || mime.includes("presentation")) {
    return { label: "PPT", classes: ATTACHMENT_TINTS.orange, kind: t("attachment.presentation") }
  }
  if (mime.startsWith("audio/") || ["mp3", "wav", "ogg", "m4a", "flac"].includes(ext)) {
    return { label: "AUDIO", classes: ATTACHMENT_TINTS.pink, kind: t("attachment.audio") }
  }
  if (mime.startsWith("video/") || ["mp4", "mov", "mkv", "avi", "webm"].includes(ext)) {
    return { label: "VIDEO", classes: ATTACHMENT_TINTS.purple, kind: t("attachment.video") }
  }
  return {
    label: (ext || "file").slice(0, 4).toUpperCase(),
    classes: ATTACHMENT_TINTS.gray,
    kind: "",
  }
}
