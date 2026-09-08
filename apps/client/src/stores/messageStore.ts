import type { Message } from "../types"
import { Store } from "../lib/store"

export interface MessageState {
  /** messages of the open thread */
  messages: Message[]
  /** whether compose is open */
  composing: boolean
  /** draft subject/body while composing */
  draftSubject: string
  draftBody: string
}

export const messageStore = new Store<MessageState>({
  messages: [],
  composing: false,
  draftSubject: "",
  draftBody: "",
})

export function setThreadMessages(messages: Message[]): void {
  messageStore.update((s) => ({ ...s, messages }))
}

export function setComposing(open: boolean): void {
  messageStore.update((s) => ({ ...s, composing: open }))
}

export function setDraft(subject: string, body: string): void {
  messageStore.update((s) => ({ ...s, draftSubject: subject, draftBody: body }))
}
