import * as React from "react"
import { Archive } from "lucide-react"

import { getMailService } from "@/services/serviceRegistry"
import { useT } from "@/lib/i18n"
import { markThreadRead } from "@/stores/threadStore"
import type { Message, Thread } from "@/types"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { ThreadPage } from "./ThreadPage"

/**
 * ThreadPageLoader — serves the /thread?id=<threadId> route. The Astro shells
 * are static, so thread pages cannot be prerendered per id: this loader reads
 * the id from the query string and fetches thread + messages from the engine.
 */
export function ThreadPageLoader({ threadId: threadIdProp }: { threadId?: string }) {
  const t = useT()
  const [data, setData] = React.useState<{
    thread: Thread
    messages: Message[]
    isDraft?: boolean
    folderType?: string
  } | null>(null)
  const [error, setError] = React.useState<string | null>(null)

  React.useEffect(() => {
    const id = threadIdProp ?? new URLSearchParams(window.location.search).get("id")
    if (!id) {
      setError(t("thread.notSpecified"))
      return
    }
    let alive = true
    void (async () => {
      try {
        const svc = await getMailService()
        const thread = await svc.getThread(id)
        const messages = await Promise.all(thread.messageIds.map((mid) => svc.getMessage(mid)))
        messages.sort((a, b) => Date.parse(a.date) - Date.parse(b.date))
        const folders = thread.accountId ? await svc.getFolders(thread.accountId) : []
        const isDraft = folders.some((f) => f.id === thread.folderId && f.type === "drafts")
        const folderType = folders.find((f) => f.id === thread.folderId)?.type
        // Opening an email marks it read (engine: local + server \Seen on
        // every message of the thread). Best-effort: the page still opens if
        // the round-trip fails; the flag syncs on the next refresh.
        let shownThread = thread
        if (thread.isUnread) {
          try {
            await svc.markThreadRead(thread.id)
            markThreadRead(thread.id) // keep the folder list in sync
            shownThread = { ...thread, isUnread: false }
          } catch {
            // ignore: the row stays unread until the next sync
          }
        }
        if (alive) setData({ thread: shownThread, messages, isDraft, folderType })
      } catch (err) {
        if (alive) {
          setError(err instanceof Error ? err.message : t("thread.notFound"))
        }
      }
    })()
    return () => {
      alive = false
    }
  }, [threadIdProp])

  if (error) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-8">
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Archive />
            </EmptyMedia>
            <EmptyTitle>{t("thread.notFoundTitle")}</EmptyTitle>
            <EmptyDescription>{error}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    )
  }

  if (!data) {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-6" aria-busy="true">
        <div className="mx-auto flex max-w-3xl flex-col gap-3">
          <Skeleton className="h-6 w-2/3" />
          <Skeleton className="h-4 w-1/3" />
          <Skeleton className="h-28 w-full" />
          <Skeleton className="h-28 w-full" />
        </div>
      </div>
    )
  }

  return (
    <ThreadPage
      thread={data.thread}
      messages={data.messages}
      isDraft={data.isDraft}
      folderType={data.folderType}
    />
  )
}
