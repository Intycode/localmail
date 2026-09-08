import { setAccounts } from "../stores/accountStore"
import { setThreads } from "../stores/threadStore"
import { setThreadMessages } from "../stores/messageStore"
import { getMailService } from "../services/serviceRegistry"
import { uiStore } from "../stores/uiStore"
import type { Account, Folder, Thread } from "../types"

/**
 * loadShellData — build-time shell data for the Astro pages.
 *
 * Reads the connected accounts and the active account's folders from the
 * local store so the sidebar renders them. With no account connected (the
 * current state) this returns empty lists and the default inbox folder id,
 * and the UI lands on its empty states.
 */
export async function loadShellData(): Promise<{
  accounts: Account[]
  folders: Folder[]
  activeFolderId: string
}> {
  const service = await getMailService()
  const accounts = await service.getAccounts()
  const active = accounts.find((a) => a.isActive) ?? accounts[0]
  if (!active) {
    return { accounts: [], folders: [], activeFolderId: "f-inbox" }
  }
  const folders = await service.getFolders(active.id)
  return { accounts, folders, activeFolderId: folders[0]?.id ?? "f-inbox" }
}

/**
 * loadInitialData — local-first bootstrap.
 *
 * Renders from local data immediately, then applies any background sync
 * events. The UI never waits on the network for data that is already
 * available locally. With no account connected the local store is empty, so
 * the UI lands on its empty states rather than invented content.
 */
export async function loadInitialData(): Promise<void> {
  const service = await getMailService()
  const accounts = await service.getAccounts()
  setAccounts(accounts)

  const state = uiStore.get()
  const accountId = state.activeAccountId
  const folderId = state.activeFolderId

  const folders = await service.getFolders(accountId)
  const activeFolder = folders.find((f) => f.id === folderId) ?? folders[0]
  if (!activeFolder) {
    // No account, hence no folder: render the empty state and stop here.
    setThreads(folderId, [])
    return
  }

  const threads = await service.getThreads(activeFolder.id)
  uiStore.update((s) => ({ ...s, activeFolderId: activeFolder.id }))
  setThreads(activeFolder.id, threads)

  // background sync: never blocks the initial render
  void service.sync(accountId).catch(() => undefined)
}

/** Load the messages of a thread into the message store. */
export async function loadThreadMessages(thread: Thread): Promise<void> {
  const service = await getMailService()
  const messages = await Promise.all(thread.messageIds.map((id) => service.getMessage(id)))
  setThreadMessages(messages.sort((a, b) => Date.parse(a.date) - Date.parse(b.date)))
}

/** Convenience: active account. */
export function activeAccount(): Account | undefined {
  return undefined // filled in by pages from accountStore
}
