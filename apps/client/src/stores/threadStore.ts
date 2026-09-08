import type { Thread } from "../types"
import { Store } from "../lib/store"

export interface ThreadState {
  /** threads for the currently selected folder */
  threads: Thread[]
  /** folder id these threads belong to */
  folderId: string
  /** id of the selected/open thread */
  openThreadId: string | null
  /** thread detail currently open (expanded) */
  openThread: Thread | null
  loaded: boolean
}

export const threadStore = new Store<ThreadState>({
  threads: [],
  folderId: "",
  openThreadId: null,
  openThread: null,
  loaded: false,
})

export function setThreads(folderId: string, threads: Thread[]): void {
  threadStore.update((s) => ({ ...s, folderId, threads, loaded: true }))
}

export function setOpenThread(thread: Thread | null): void {
  threadStore.update((s) => ({
    ...s,
    openThread: thread,
    openThreadId: thread?.id ?? null,
  }))
}

export function markThreadRead(threadId: string): void {
  threadStore.update((s) => ({
    ...s,
    threads: s.threads.map((t) => (t.id === threadId ? { ...t, isUnread: false } : t)),
    openThread: s.openThread?.id === threadId ? { ...s.openThread, isUnread: false } : s.openThread,
  }))
}

export function markThreadUnread(threadId: string): void {
  threadStore.update((s) => ({
    ...s,
    threads: s.threads.map((t) => (t.id === threadId ? { ...t, isUnread: true } : t)),
    openThread: s.openThread?.id === threadId ? { ...s.openThread, isUnread: true } : s.openThread,
  }))
}

export function moveThreadToArchive(threadId: string): void {
  threadStore.update((s) => ({
    ...s,
    threads: s.threads.filter((t) => t.id !== threadId),
    openThread: s.openThread?.id === threadId ? null : s.openThread,
    openThreadId: s.openThread?.id === threadId ? null : s.openThreadId,
  }))
}
