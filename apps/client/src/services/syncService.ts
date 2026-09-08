import { getMailService } from "./serviceRegistry"
import { isTauri } from "./tauriMailService"
import { notifyNewMessage } from "../lib/notifications"
import type { SyncEvent } from "../types"
import { Store } from "../lib/store"
import { setAccounts } from "../stores/accountStore"
import { setThreads } from "../stores/threadStore"
import { setConnectionOnline, setOutboxPending } from "../stores/outboxStore"

export interface SyncState {
  /** latest sync status per account id */
  byAccount: Record<string, SyncEvent | null>
  running: boolean
}

export const syncStore = new Store<SyncState>({ byAccount: {}, running: false })

export function syncAccount(accountId: string): void {
  syncStore.update((s) => ({
    ...s,
    running: true,
    byAccount: { ...s.byAccount, [accountId]: { type: "sync.started", accountId } },
  }))
  getMailService()
    .then((service) => service.sync(accountId))
    .then(() => {
      syncStore.update((s) => ({
        ...s,
        running: false,
        byAccount: {
          ...s.byAccount,
          [accountId]: { type: "sync.completed", accountId, at: new Date().toISOString() },
        },
      }))
      void scheduleRefresh()
    })
    .catch((err: unknown) => {
      syncStore.update((s) => ({
        ...s,
        running: false,
        byAccount: {
          ...s.byAccount,
          [accountId]: {
            type: "sync.error",
            accountId,
            message: err instanceof Error ? err.message : "sync failed",
          },
        },
      }))
    })
}

let unlisten: (() => void) | null = null
let refreshTimer: ReturnType<typeof setTimeout> | null = null

interface EngineEvent {
  kind: string
  data: Record<string, unknown>
}

function handleAppEvent(e: EngineEvent): void {
  const kind = e.kind
  const accountId = String(e.data?.accountId ?? "")
  if (kind === "sync.started") {
    syncStore.update((s) => ({
      ...s,
      running: true,
      byAccount: { ...s.byAccount, [accountId]: { type: "sync.started", accountId } },
    }))
  } else if (kind === "sync.progress") {
    const done = Number(e.data?.done ?? 0)
    const total = Number(e.data?.total ?? 0)
    syncStore.update((s) => ({
      ...s,
      byAccount: { ...s.byAccount, [accountId]: { type: "sync.progress", accountId, done, total } },
    }))
  } else if (kind === "sync.completed") {
    syncStore.update((s) => ({
      ...s,
      running: false,
      byAccount: {
        ...s.byAccount,
        [accountId]: { type: "sync.completed", accountId, at: new Date().toISOString() },
      },
    }))
    void scheduleRefresh()
  } else if (kind === "sync.error") {
    syncStore.update((s) => ({
      ...s,
      running: false,
      byAccount: {
        ...s.byAccount,
        [accountId]: {
          type: "sync.error",
          accountId,
          message: String(e.data?.message ?? "sync error"),
        },
      },
    }))
  } else if (kind === "account.updated") {
    void scheduleRefresh()
  } else if (kind === "message.created") {
    // IDLE push: a new message arrived outside a full sync cycle. The
    // debounce coalesces bursts (bulk deliveries) into one refresh.
    void scheduleRefresh()
    // native notification (Settings → Notifiche): unread messages only
    void notifyNewMessage(e.data)
  } else if (kind === "outbox.updated") {
    // offline-first queue: pending count changed (enqueue or replay)
    setOutboxPending(Number(e.data?.pending ?? 0))
  } else if (kind === "connection.state") {
    // engine-side reachability transitions; on return-to-online the engine
    // is already replaying, we just mirror the state for the banner
    setConnectionOnline(e.data?.online !== false)
  }
}

/** Refresh accounts + current folder threads from the engine (debounced). */
function scheduleRefresh(): void {
  if (!isTauri()) return
  if (refreshTimer) clearTimeout(refreshTimer)
  refreshTimer = setTimeout(() => {
    refreshTimer = null
    void (async () => {
      try {
        const svc = await getMailService()
        const accounts = await svc.getAccounts()
        setAccounts(accounts)
        // keep the visible view fresh: the URL wins (?account=all unified,
        // else ?folder), then the active account's first folder
        const params = new URLSearchParams(window.location.search)
        const urlAccount = params.get("account")
        if (urlAccount) {
          const threads = await svc.getThreads(undefined, { accountId: urlAccount }).catch(() => [])
          setThreads(`account:${urlAccount}:inbox`, threads)
          return
        }
        const urlFolder = params.get("folder")
        const active = accounts.find((x) => x.isActive) ?? accounts[0]
        const folderId = urlFolder ?? active?.folders?.[0]?.id
        if (folderId) {
          const threads = await svc.getThreads(folderId).catch(() => [])
          setThreads(folderId, threads)
        }
      } catch {
        // engine restarting or offline: events will retrigger a refresh
      }
    })()
  }, 200)
}

/**
 * Subscribes to the engine event stream ("mail://event" emitted by Tauri) and
 * refreshes the relevant stores. Idempotent: calling it twice does not
 * double-subscribe. No-op in the plain browser.
 */
export async function wireEventStream(): Promise<void> {
  if (!isTauri() || unlisten) return
  const { listen } = await import("@tauri-apps/api/event")
  unlisten = await listen<EngineEvent>("mail://event", (e) => handleAppEvent(e.payload))

  // browser-level connectivity flips: mirror them for the banner and ask the
  // engine for an immediate outbox replay when the network comes back
  window.addEventListener("online", () => {
    setConnectionOnline(true)
    void drainOutbox()
  })
  window.addEventListener("offline", () => setConnectionOnline(false))
}

/** Asks the engine to replay queued operations now (best-effort). */
async function drainOutbox(): Promise<void> {
  try {
    const svc = (await getMailService()) as { drainOutbox?: () => Promise<void> }
    await svc.drainOutbox?.()
  } catch {
    // engine not ready: the periodic worker covers it
  }
}

/** test hook */
export function stopEventStream(): void {
  if (unlisten) {
    unlisten()
    unlisten = null
  }
}
