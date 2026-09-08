import * as React from "react"
import { Inbox, Paperclip, Star } from "lucide-react"
import { toast } from "sonner"

import { cn } from "@/lib/utils"
import { dayLabel, formatDate, shortSender } from "@/lib/format"
import { getSetting } from "@/lib/settings"
import { useT } from "@/lib/i18n"
import { getMailService } from "@/services/serviceRegistry"
import type { ThreadListOptions } from "@/services/mailService"
import { threadStore } from "@/stores/threadStore"
import type { Account, Thread } from "@/types"
import { ThreadContextMenu } from "@/components/app/ThreadContextMenu"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"

interface InboxPageProps {
  /** Pre-rendered threads from the local store (empty until an account syncs). */
  threads: Thread[]
  emptyText?: string
  /** Explicit folder from the router; the URL param is the fallback. */
  folderId?: string
  /** Account-level view: "all" = unified inbox across accounts, else one id. */
  accountId?: string
  /** Accounts, for the unified-view per-row badge. */
  accounts?: Account[]
}

/** Stable store key for the current view: unified inbox or per-folder. */
function viewKey(folderId: string, accountId?: string): string {
  return accountId ? `account:${accountId}:${folderId}` : folderId
}

/**
 * InboxPage — the message list. Reads from the local MailService, renders one
 * row per thread grouped by day, and reacts to the `a` shortcut (archive)
 * dispatched by the AppShell keyboard layer. Honours the "compact" setting.
 * In the unified view (`?account=all`) every account's inbox merges into one
 * newest-first list and each row carries an account badge.
 */
export function InboxPage({
  threads: initialThreads,
  emptyText,
  folderId: folderIdProp,
  accountId,
  accounts,
}: InboxPageProps) {
  const t = useT()
  const [threads, setThreads] = React.useState<Thread[]>(initialThreads)
  const [loading, setLoading] = React.useState(initialThreads.length === 0)
  const [compact, setCompact] = React.useState(() => getSetting("compact", false))

  // The router passes the folder explicitly; deep loads without it fall back
  // to the URL param. Never touch window during render: Astro prerenders.
  const [folderId, setFolderId] = React.useState(folderIdProp ?? "f-inbox")

  React.useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const fromUrl = params.get("folder")
    setFolderId(folderIdProp ?? fromUrl ?? "f-inbox")
    setCompact(getSetting("compact", false))
  }, [folderIdProp])

  const key = viewKey(folderId, accountId)

  // Engine pushes (IDLE, sync) land in threadStore via syncService; adopt them
  // when they are for this exact view so the visible list stays fresh without
  // a navigation.
  const stored = React.useSyncExternalStore(
    threadStore.subscribe,
    () => threadStore.get(),
    () => threadStore.get(),
  )
  const freshFromStore = stored.loaded && stored.folderId === key && stored.threads.length > 0

  React.useEffect(() => {
    if (freshFromStore) setThreads(stored.threads)
  }, [freshFromStore, stored.threads])

  // Pull the view's threads whenever the effective folder/account changes.
  React.useEffect(() => {
    let alive = true
    setLoading(true)
    const opts: ThreadListOptions | undefined = accountId
      ? { accountId, folderType: "inbox" }
      : undefined
    getMailService()
      .then((svc) => svc.getThreads(accountId ? undefined : folderId, opts))
      .then((t) => alive && setThreads(t ?? []))
      .catch((e: unknown) => console.error("inbox", e))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [folderId, accountId])

  // Archive via the "a" shortcut (dispatched from AppShell).
  React.useEffect(() => {
    async function onArchive(e: Event) {
      const threadId = (e as CustomEvent<{ threadId: string }>).detail.threadId
      const svc = await getMailService()
      try {
        await svc.archiveThread(threadId)
        setThreads((prev) => prev.filter((x) => x.id !== threadId))
        toast.success(t("inbox.archived"))
      } catch {
        toast.error(t("inbox.archiveFailed"))
      }
    }
    window.addEventListener("mail:archive-thread", onArchive)
    return () => window.removeEventListener("mail:archive-thread", onArchive)
  }, [])

  const accountById = React.useMemo(() => {
    const map = new Map<string, Account>()
    for (const a of accounts ?? []) map.set(a.id, a)
    return map
  }, [accounts])

  if (loading) {
    return (
      <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto" aria-busy="true">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="flex items-center gap-3 border-b border-border px-4 py-3">
            <Skeleton className="size-2 rounded-full" />
            <Skeleton className="h-4 w-32" />
            <Skeleton className="h-4 flex-1" />
            <Skeleton className="h-3 w-14" />
          </div>
        ))}
      </div>
    )
  }

  if (threads.length === 0) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-y-auto p-8">
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Inbox />
            </EmptyMedia>
            <EmptyTitle>
              {accountId === "all" ? t("inbox.emptyUnified") : t("inbox.emptyFolder")}
            </EmptyTitle>
            <EmptyDescription>{emptyText ?? t("inbox.emptyDesc")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    )
  }

  return (
    <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto">
      {threads.map((t, i) => {
        const label = dayLabel(t.date)
        const showLabel = label !== "" && (i === 0 || label !== dayLabel(threads[i - 1].date))
        return (
          <React.Fragment key={t.id}>
            {showLabel && (
              <div className="sticky top-0 z-10 border-b border-border/60 bg-background px-4 py-1.5 font-mono text-[10px] font-medium tracking-[0.1em] text-muted-foreground uppercase">
                {label}
              </div>
            )}
            <ThreadRow
              thread={t}
              compact={compact}
              accountId={accountId}
              account={accountById.get(t.accountId)}
              onChange={(next) =>
                setThreads((prev) =>
                  next === null
                    ? prev.filter((x) => x.id !== t.id)
                    : prev.map((x) => (x.id === t.id ? next : x)),
                )
              }
            />
          </React.Fragment>
        )
      })}
    </div>
  )
}

function ThreadRow({
  thread,
  compact,
  accountId,
  account,
  onChange,
}: {
  thread: Thread
  compact: boolean
  accountId?: string
  account?: Account
  /** null = remove the row from the list; Thread = replace it (flag changes). */
  onChange: (next: Thread | null) => void
}) {
  const t = useT()
  // Threads carry their own folder: in the unified view each row links back
  // to the folder the message actually lives in.
  const href = `/thread?id=${encodeURIComponent(thread.id)}&folder=${encodeURIComponent(
    thread.folderId,
  )}`
  const showBadge = Boolean(accountId) && account !== undefined

  return (
    <ThreadContextMenu
      thread={thread}
      onRemoved={() => onChange(null)}
      onUpdated={(next) => onChange(next)}
    >
      <a
        href={href}
        data-thread-row
        data-thread={thread.id}
        className={cn(
          "grid grid-cols-[16px_150px_1fr_auto] items-center gap-3 border-b border-border px-4 outline-none",
          compact ? "min-h-9 py-1" : "min-h-12 py-2.5",
          "hover:bg-accent focus-visible:bg-accent focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring",
          "max-md:grid-cols-[16px_1fr_auto]",
        )}
      >
        <span className="flex justify-center">
          {thread.isUnread ? (
            <span className="size-2 rounded-full bg-primary" aria-label={t("inbox.unread")} />
          ) : thread.isStarred ? (
            <Star
              className="size-3.5 fill-current text-muted-foreground"
              aria-label={t("inbox.starred")}
            />
          ) : null}
        </span>
        <span
          className={cn(
            "flex min-w-0 items-center gap-1.5 text-sm max-md:hidden",
            thread.isUnread ? "font-semibold text-foreground" : "text-foreground/90",
          )}
        >
          {showBadge && (
            <span
              className="shrink-0 rounded border border-border bg-muted/60 px-1.5 py-px text-[10px] font-medium leading-4 text-muted-foreground"
              title={account.email}
            >
              {account.name || account.email.split("@")[0]}
            </span>
          )}
          <span className="truncate">{shortSender(thread)}</span>
        </span>
        <span className="flex min-w-0 items-baseline gap-2 text-sm">
          <span
            className={cn(
              "truncate",
              thread.isUnread ? "font-medium text-foreground" : "text-muted-foreground",
            )}
          >
            {thread.subject || t("inbox.noSubject")}
          </span>
          <span className="hidden min-w-0 truncate text-muted-foreground sm:block">
            {thread.snippet}
          </span>
        </span>
        <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
          {thread.hasAttachments && <Paperclip className="size-3" />}
          <time className="whitespace-nowrap tabular-nums">{formatDate(thread.date)}</time>
        </span>
      </a>
    </ThreadContextMenu>
  )
}
