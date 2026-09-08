import { test, expect } from "bun:test"
import { LocalMailService } from "./localMailService"
import type { Message, Thread } from "../types"

/** Thread-level read/unread: the local service mirrors the engine aggregate. */

const svc = new LocalMailService()

const mkThread = (id: string, isUnread: boolean): Thread => ({
  id,
  folderId: "f1",
  accountId: "a1",
  subject: `Soggetto ${id}`,
  messageIds: [`m-${id}-1`, `m-${id}-2`],
  from: { name: "Mittente", email: "mitt@x.dev" },
  to: [],
  snippet: "",
  date: "2026-08-25T09:00:00Z",
  isUnread,
  isStarred: false,
  labels: [],
  messageCount: 2,
  hasAttachments: false,
})
const mkMessage = (id: string, threadId: string, isUnread: boolean): Message => ({
  id,
  threadId,
  folderId: "f1",
  accountId: "a1",
  subject: "Soggetto",
  from: { name: "Mittente", email: "mitt@x.dev" },
  to: [],
  cc: [],
  bcc: [],
  date: "2026-08-25T09:00:00Z",
  body: "corpo",
  bodyText: "corpo",
  remoteImages: 0,
  isEncrypted: false,
  isSigned: false,
  spamScore: 0,
  phishing: [],
  isUnread,
  isStarred: false,
  labels: [],
  attachments: [],
})

const internal = svc as unknown as { threads: Thread[]; messages: Message[] }
internal.threads = [mkThread("t1", true)]
internal.messages = [mkMessage("m-t1-1", "t1", true), mkMessage("m-t1-2", "t1", true)]

test("markThreadRead clears every message and the thread", async () => {
  await svc.markThreadRead("t1")
  expect(internal.messages.every((m) => !m.isUnread)).toBe(true)
  expect(internal.threads[0].isUnread).toBe(false)
})

test("markThreadUnread restores the unread state", async () => {
  await svc.markThreadUnread("t1")
  expect(internal.messages.every((m) => m.isUnread)).toBe(true)
  expect(internal.threads[0].isUnread).toBe(true)
})
