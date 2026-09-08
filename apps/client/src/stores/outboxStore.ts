import * as React from "react"

import { Store } from "../lib/store"

export interface OutboxState {
  /** operations parked while offline, awaiting replay */
  pending: number
  /** last known server reachability (connection.state events) */
  online: boolean
  /** true once the engine has reported any state */
  known: boolean
}

export const outboxStore = new Store<OutboxState>({ pending: 0, online: true, known: false })

export function setOutboxPending(pending: number): void {
  outboxStore.update((s) => ({ ...s, pending, known: true }))
}

export function setConnectionOnline(online: boolean): void {
  outboxStore.update((s) => ({ ...s, online, known: true }))
}

/** useOutbox — reactive outbox/connectivity state for the UI (banner). */
export function useOutbox(): OutboxState {
  return React.useSyncExternalStore(outboxStore.subscribe, outboxStore.get, outboxStore.get)
}
