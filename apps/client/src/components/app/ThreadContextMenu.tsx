import * as React from "react"
import {
  Archive,
  FileText,
  FolderInput,
  MailCheck,
  MailOpen,
  ShieldAlert,
  Trash2,
} from "lucide-react"
import { toast } from "sonner"

import { formatFolderName } from "@/lib/format"
import { useT } from "@/lib/i18n"
import {
  markThreadRead as markThreadReadInStore,
  markThreadUnread as markThreadUnreadInStore,
} from "@/stores/threadStore"
import { getMailService } from "@/services/serviceRegistry"
import type { Folder, Thread } from "@/types"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"

interface ThreadContextMenuProps {
  /** The thread to act on. When null, `threadId` is fetched on menu open
   *  (search results only carry the id). */
  thread: Thread | null
  threadId?: string
  /** The row was removed from the list (archive/move/delete). */
  onRemoved?: () => void
  /** The row changed in place (read flags). */
  onUpdated?: (next: Thread) => void
  children: React.ReactNode
}

/**
 * ThreadContextMenu — right-click actions on a thread row: open, read
 * flags, archive, spam, move-to-folder, delete. The folder list for the
 * "Sposta in cartella" submenu is loaded lazily when the menu opens. The
 * engine does the server-side work (IMAP MOVE / \Seen), the caller drops
 * the row from its own list via the callbacks.
 */
export function ThreadContextMenu({
  thread: threadProp,
  threadId,
  onRemoved,
  onUpdated,
  children,
}: ThreadContextMenuProps) {
  const t = useT()
  const [thread, setThread] = React.useState<Thread | null>(threadProp)
  const [folders, setFolders] = React.useState<Folder[] | null>(null)

  React.useEffect(() => setThread(threadProp), [threadProp])

  async function load(onOpen: boolean): Promise<void> {
    if (!onOpen) return
    if (!thread && threadId) {
      try {
        const svc = await getMailService()
        setThread(await svc.getThread(threadId))
      } catch {
        // the menu opens with the always-available actions only
      }
    }
    if (!folders && thread?.accountId) {
      try {
        const svc = await getMailService()
        setFolders(await svc.getFolders(thread.accountId))
      } catch {
        setFolders([]) // submenu stays empty, menu stays usable
      }
    }
  }

  function removed(): void {
    setThread(null)
    onRemoved?.()
  }

  async function archive(): Promise<void> {
    if (!thread) return
    try {
      const svc = await getMailService()
      await svc.archiveThread(thread.id)
      toast.success(t("inbox.archived"))
      removed()
    } catch {
      toast.error(t("inbox.archiveFailed"))
    }
  }

  async function moveToFolder(target: Folder): Promise<void> {
    if (!thread || target.id === thread.folderId) return
    try {
      const svc = await getMailService()
      await svc.moveThread(thread.id, target.id)
      toast.success(t("thread.movedTo", { folder: formatFolderName(target.name) }))
      removed()
    } catch {
      toast.error(t("thread.moveFailed"))
    }
  }

  async function moveToSpam(): Promise<void> {
    if (!thread) return
    try {
      const svc = await getMailService()
      const list = folders ?? (thread.accountId ? await svc.getFolders(thread.accountId) : [])
      const junk = list.find((f) => f.type === "junk")
      if (!junk) {
        toast.error(t("thread.folderNotFound", { folder: t("folder.junk") }))
        return
      }
      await svc.moveThread(thread.id, junk.id)
      toast.success(t("thread.reportedSpam"))
      removed()
    } catch {
      toast.error(t("thread.moveFailed"))
    }
  }

  async function markRead(): Promise<void> {
    if (!thread) return
    try {
      const svc = await getMailService()
      await svc.markThreadRead(thread.id)
      markThreadReadInStore(thread.id)
      const next = { ...thread, isUnread: false }
      setThread(next)
      onUpdated?.(next)
    } catch {
      toast.error(t("common.failed"))
    }
  }

  async function markUnread(): Promise<void> {
    if (!thread) return
    try {
      const svc = await getMailService()
      await svc.markThreadUnread(thread.id)
      markThreadUnreadInStore(thread.id)
      const next = { ...thread, isUnread: true }
      setThread(next)
      onUpdated?.(next)
    } catch {
      toast.error(t("common.failed"))
    }
  }

  async function remove(): Promise<void> {
    if (!thread) return
    try {
      const svc = await getMailService()
      await Promise.all((thread.messageIds ?? []).map((mid) => svc.deleteMessage(mid)))
      toast.success(t("common.deleted"))
      removed()
    } catch {
      toast.error(t("common.deleteFailed"))
    }
  }

  const movableFolders = (folders ?? []).filter(
    (f) => f.id !== thread?.folderId && f.type !== "snoozed",
  )
  const ready = thread !== null

  return (
    <ContextMenu onOpenChange={(open) => void load(open)}>
      <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem asChild disabled={!ready}>
          <a href={ready ? `/thread?id=${encodeURIComponent(thread.id)}` : "#"}>
            <FileText />
            {t("thread.open")}
          </a>
        </ContextMenuItem>
        <ContextMenuItem
          disabled={!ready}
          onClick={() => void (thread?.isUnread ? markRead() : markUnread())}
        >
          {thread?.isUnread ? <MailCheck /> : <MailOpen />}
          {thread?.isUnread ? t("thread.markRead") : t("thread.markUnread")}
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem disabled={!ready} onClick={() => void archive()}>
          <Archive />
          {t("thread.archive")}
        </ContextMenuItem>
        <ContextMenuItem disabled={!ready} onClick={() => void moveToSpam()}>
          <ShieldAlert />
          {t("thread.reportSpam")}
        </ContextMenuItem>
        {ready && movableFolders.length > 0 && (
          <ContextMenuSub>
            <ContextMenuSubTrigger>
              <FolderInput />
              {t("thread.moveToFolder")}
            </ContextMenuSubTrigger>
            <ContextMenuSubContent>
              {movableFolders.map((f) => (
                <ContextMenuItem key={f.id} onClick={() => void moveToFolder(f)}>
                  {formatFolderName(f.name)}
                </ContextMenuItem>
              ))}
            </ContextMenuSubContent>
          </ContextMenuSub>
        )}
        <ContextMenuSeparator />
        <ContextMenuItem variant="destructive" disabled={!ready} onClick={() => void remove()}>
          <Trash2 />
          {t("common.delete")}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  )
}
