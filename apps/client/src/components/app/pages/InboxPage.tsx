import * as React from "react"
import { Inbox, Mail, Paperclip, RefreshCw, Star } from "lucide-react"
import { toast } from "sonner"

import { cn } from "@/lib/utils"
import { dayLabel, formatDate, formatFolderName, initials, shortSender } from "@/lib/format"
import { getSetting, getSettingString, setSettingString } from "@/lib/settings"
import { useIsMobile } from "@/hooks/use-mobile"
import { useT } from "@/lib/i18n"
import { getMailService } from "@/services/serviceRegistry"
import { mergeFirstPage, THREAD_PAGE, type ThreadListOptions } from "@/services/mailService"
import { syncAll, syncStore } from "@/services/syncService"
import { threadStore } from "@/stores/threadStore"
import type { Account, Thread } from "@/types"
import { Button } from "@/components/ui/button"
import { SearchBox } from "@/components/app/SearchBox"
import { ThreadContextMenu } from "@/components/app/ThreadContextMenu"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { ThreadPageLoader } from "./ThreadPageLoader"

interface InboxPageProps {
  /** Pre-rendered threads from the local store (empty until an account syncs). */
  threads: Thread[]
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
const LIST_MIN = 260
const LIST_MAX = 720

export function InboxPage({
  threads: initialThreads,
  folderId: folderIdProp,
  accountId,
  accounts,
}: InboxPageProps) {
  const t = useT()
  const [threads, setThreads] = React.useState<Thread[]>(initialThreads)
  const [loading, setLoading] = React.useState(initialThreads.length === 0)
  const [compact, setCompact] = React.useState(() => getSetting("compact", false))
  // Desktop: rows open the thread in the reading pane; mobile keeps /thread.
  const [selectedId, setSelectedId] = React.useState<string | null>(null)
  const isMobile = useIsMobile()
  // Draggable divider: list width in px (the reader takes the rest), persisted.
  const [listW, setListW] = React.useState(416)
  React.useEffect(() => {
    const saved = Number(getSettingString("list-width"))
    if (saved >= LIST_MIN && saved <= LIST_MAX) setListW(saved)
  }, [])
  const resize = (w: number) => {
    const v = Math.min(LIST_MAX, Math.max(LIST_MIN, Math.round(w)))
    setListW(v)
    setSettingString("list-width", String(v))
  }

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
  const syncing = React.useSyncExternalStore(
    syncStore.subscribe,
    () => syncStore.get().running,
    () => syncStore.get().running,
  )
  const stored = React.useSyncExternalStore(
    threadStore.subscribe,
    () => threadStore.get(),
    () => threadStore.get(),
  )
  const freshFromStore = stored.loaded && stored.folderId === key && stored.threads.length > 0

  React.useEffect(() => {
    if (freshFromStore) setThreads((prev) => mergeFirstPage(stored.threads, prev))
  }, [freshFromStore, stored.threads])

  const listOpts: ThreadListOptions | undefined = accountId
    ? { accountId, folderType: "inbox" }
    : undefined
  // Pages: the list loads THREAD_PAGE threads at a time; scrolling near the
  // end pulls the next page (the whole history can be tens of thousands).
  const [hasMore, setHasMore] = React.useState(false)
  const loadingMore = React.useRef(false)

  // Pull the view's threads whenever the effective folder/account changes.
  React.useEffect(() => {
    let alive = true
    setLoading(true)
    setHasMore(false)
    getMailService()
      .then((svc) => svc.getThreads(accountId ? undefined : folderId, listOpts))
      .then((t) => {
        if (!alive) return
        setThreads(t ?? [])
        setHasMore((t ?? []).length >= THREAD_PAGE)
      })
      .catch((e: unknown) => console.error("inbox", e))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [folderId, accountId])

  const loadMore = React.useCallback(async () => {
    const last = threads[threads.length - 1]
    if (!hasMore || loadingMore.current || !last) return
    loadingMore.current = true
    try {
      const svc = await getMailService()
      const next = await svc.getThreads(accountId ? undefined : folderId, {
        ...listOpts,
        before: last,
      })
      setThreads((prev) => {
        const ids = new Set(prev.map((x) => x.id))
        return [...prev, ...next.filter((x) => !ids.has(x.id))]
      })
      setHasMore(next.length >= THREAD_PAGE)
    } catch (e) {
      console.error("inbox page", e)
    } finally {
      loadingMore.current = false
    }
  }, [threads, hasMore, folderId, accountId])

  // Sentinel at the end of the list: within ~2 screens of it, load the next page.
  const scrollRef = React.useRef<HTMLDivElement>(null)
  const sentinelRef = React.useRef<HTMLDivElement>(null)
  React.useEffect(() => {
    const el = sentinelRef.current
    if (!el || !hasMore) return
    const io = new IntersectionObserver(
      (entries) => entries[0]?.isIntersecting && void loadMore(),
      {
        root: scrollRef.current,
        rootMargin: "0px 0px 200% 0px",
      },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [loadMore, hasMore])

  // Archive via the "a" shortcut (dispatched from AppShell).
  React.useEffect(() => {
    async function onArchive(e: Event) {
      const threadId = (e as CustomEvent<{ threadId: string }>).detail.threadId
      const svc = await getMailService()
      try {
        await svc.archiveThread(threadId)
        setThreads((prev) => prev.filter((x) => x.id !== threadId))
        setSelectedId((prev) => (prev === threadId ? null : prev))
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

  const folder = (accounts ?? []).flatMap((a) => a.folders ?? []).find((f) => f.id === folderId)
  const title =
    accountId === "all"
      ? t("nav.allMailboxes")
      : folder
        ? formatFolderName(folder.name)
        : t("folder.inbox")
  // the folder counter covers threads beyond the loaded pages
  const unread = folder && !accountId ? folder.unread : threads.filter((x) => x.isUnread).length

  function select(id: string): void {
    setSelectedId(id)
    // the loader marks it read on the engine; mirror that in the list now
    setThreads((prev) => prev.map((x) => (x.id === id ? { ...x, isUnread: false } : x)))
  }

  let list: React.ReactNode
  if (loading) {
    list = (
      <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto" aria-busy="true">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="flex gap-3 border-b border-border px-4 py-3">
            <Skeleton className="size-8 rounded-full" />
            <div className="flex flex-1 flex-col gap-1.5">
              <Skeleton className="h-4 w-32" />
              <Skeleton className="h-3.5 w-full" />
            </div>
          </div>
        ))}
      </div>
    )
  } else if (threads.length === 0) {
    list = (
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-y-auto p-8">
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Inbox />
            </EmptyMedia>
            <EmptyTitle>{t("inbox.emptyUnified")}</EmptyTitle>
            {(accounts ?? []).length === 0 && (
              <EmptyDescription>
                <a href="/accounts">{t("nav.connectAccount")}</a>
              </EmptyDescription>
            )}
          </EmptyHeader>
        </Empty>
      </div>
    )
  } else {
    list = (
      <div ref={scrollRef} className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto">
        {threads.map((t, i) => {
          const label = dayLabel(t.date)
          const showLabel = label !== "" && (i === 0 || label !== dayLabel(threads[i - 1].date))
          return (
            <React.Fragment key={t.id}>
              {showLabel && (
                <div className="sticky top-0 z-10 bg-background px-4 pt-2 pb-1.5 font-mono text-[10px] font-medium tracking-[0.1em] text-muted-foreground uppercase">
                  {label}
                </div>
              )}
              <ThreadRow
                thread={t}
                compact={compact}
                accountId={accountId}
                account={accountById.get(t.accountId)}
                selected={t.id === selectedId}
                onSelect={isMobile ? undefined : () => select(t.id)}
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
        {hasMore && (
          <div ref={sentinelRef} aria-busy="true" className="flex gap-3 px-4 py-3">
            <Skeleton className="size-8 rounded-full" />
            <div className="flex flex-1 flex-col gap-1.5">
              <Skeleton className="h-4 w-32" />
              <Skeleton className="h-3.5 w-full" />
            </div>
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="flex min-h-0 flex-1">
      <section
        aria-label={title}
        style={{ "--list-w": `${listW}px` } as React.CSSProperties}
        className="flex min-h-0 w-full min-w-0 flex-col md:w-[var(--list-w)] md:shrink-0 md:border-r md:border-border"
      >
        <div className="flex shrink-0 items-center justify-end gap-1 border-b border-border px-3 py-1.5">
          {/* mobile has the search page link in the top bar */}
          <SearchBox onPick={select} className="hidden min-w-0 flex-1 md:block" />
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t("accounts.sync")}
            title={t("accounts.sync")}
            disabled={syncing}
            onClick={() => void syncAll()}
          >
            <RefreshCw className={syncing ? "animate-spin" : undefined} />
          </Button>
        </div>
        <header className="shrink-0 border-b border-border px-4 pt-3.5 pb-3">
          <h1 className="truncate text-[17px] font-semibold tracking-tight">{title}</h1>
          {unread > 0 && (
            <p className="mt-0.5 text-xs text-muted-foreground">
              {t("inbox.unreadCount", { n: unread })}
            </p>
          )}
        </header>
        {list}
      </section>
      <div
        role="separator"
        aria-orientation="vertical"
        aria-valuenow={listW}
        aria-valuemin={LIST_MIN}
        aria-valuemax={LIST_MAX}
        tabIndex={0}
        className="-ml-1 hidden w-2 shrink-0 cursor-col-resize touch-none focus-visible:bg-primary/30 focus-visible:outline-none md:block"
        onPointerDown={(e) => {
          // no focus (and no text selection) from the mouse: WebKit would keep
          // the focus tint lit after the drag; the keyboard still focuses it
          e.preventDefault()
          e.currentTarget.setPointerCapture(e.pointerId)
        }}
        onPointerUp={(e) => e.currentTarget.blur()}
        onPointerMove={(e) => {
          if (!e.currentTarget.hasPointerCapture(e.pointerId)) return
          const left = e.currentTarget.parentElement!.getBoundingClientRect().left
          resize(e.clientX - left)
        }}
        onKeyDown={(e) => {
          if (e.key === "ArrowLeft") resize(listW - 16)
          if (e.key === "ArrowRight") resize(listW + 16)
        }}
      />
      <div className="hidden min-h-0 min-w-0 flex-1 flex-col md:flex">
        {selectedId ? (
          <ThreadPageLoader key={selectedId} threadId={selectedId} embedded />
        ) : (
          <div className="flex flex-1 items-center justify-center p-8">
            <Empty>
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Mail />
                </EmptyMedia>
                <EmptyTitle>{t("inbox.selectThread")}</EmptyTitle>
              </EmptyHeader>
            </Empty>
          </div>
        )}
      </div>
    </div>
  )
}

function ThreadRow({
  thread,
  compact,
  accountId,
  account,
  selected,
  onSelect,
  onChange,
}: {
  thread: Thread
  compact: boolean
  accountId?: string
  account?: Account
  selected: boolean
  /** set on desktop: open in the reading pane instead of navigating */
  onSelect?: () => void
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
  const sender = shortSender(thread)

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
        data-pane-link={onSelect ? "" : undefined}
        aria-current={selected ? "true" : undefined}
        onClick={
          onSelect
            ? (e) => {
                e.preventDefault()
                onSelect()
              }
            : undefined
        }
        className={cn(
          "flex gap-3 border-b border-border px-4 outline-none",
          compact ? "py-2" : "py-3",
          "hover:bg-accent/60 focus-visible:bg-accent focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring",
          selected && "bg-accent shadow-[inset_3px_0_0_var(--primary)] hover:bg-accent",
        )}
      >
        <span
          aria-hidden="true"
          className={cn(
            "flex size-8 shrink-0 items-center justify-center rounded-full text-xs font-semibold",
            thread.isUnread ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground",
          )}
        >
          {initials(sender)}
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex items-center gap-1.5 text-sm">
            {showBadge && (
              <span
                className="shrink-0 rounded border border-border bg-muted/60 px-1.5 py-px text-[10px] font-medium leading-4 text-muted-foreground"
                title={account.email}
              >
                {account.name || account.email.split("@")[0]}
              </span>
            )}
            <span
              className={cn(
                "min-w-0 flex-1 truncate",
                thread.isUnread ? "font-semibold text-foreground" : "text-foreground/90",
              )}
            >
              {sender}
            </span>
            {thread.isStarred && (
              <Star
                className="size-3.5 shrink-0 fill-current text-muted-foreground"
                aria-label={t("inbox.starred")}
              />
            )}
            {thread.hasAttachments && (
              <Paperclip className="size-3 shrink-0 text-muted-foreground" />
            )}
            <time className="shrink-0 text-xs whitespace-nowrap text-muted-foreground tabular-nums">
              {formatDate(thread.date)}
            </time>
            {thread.isUnread && (
              <span
                role="img"
                className="size-2 shrink-0 rounded-full bg-primary"
                aria-label={t("inbox.unread")}
              />
            )}
          </span>
          <span
            className={cn(
              "truncate text-[13px]",
              thread.isUnread ? "font-medium text-foreground" : "text-foreground/80",
            )}
          >
            {thread.subject || t("inbox.noSubject")}
          </span>
          {!compact && (
            <span className="truncate text-[13px] text-muted-foreground">{thread.snippet}</span>
          )}
        </span>
      </a>
    </ThreadContextMenu>
  )
}
