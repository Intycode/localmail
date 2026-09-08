import { test, expect } from "bun:test"
import { LocalMailService } from "./localMailService"
import type { Account, Folder, Thread } from "../types"

/**
 * Unified (account-level) getThreads: mirrors the engine's JOIN on
 * folders.type — every account's inbox merges into one newest-first list.
 */

const svc = new LocalMailService()

const accounts: Account[] = [
  {
    id: "a1",
    name: "Luca",
    email: "luca@x.dev",
    provider: "generic-imap",
    isActive: true,
    folders: [],
  },
  {
    id: "a2",
    name: "Bo",
    email: "bo@y.dev",
    provider: "generic-imap",
    isActive: false,
    folders: [],
  },
]
const folders: Folder[] = [
  { id: "f1", accountId: "a1", name: "INBOX", type: "inbox", total: 1, unread: 1 },
  { id: "f2", accountId: "a2", name: "INBOX", type: "inbox", total: 1, unread: 0 },
  { id: "f3", accountId: "a1", name: "Archive", type: "archive", total: 1, unread: 0 },
]
const mkThread = (id: string, folderId: string, accountId: string, date: string): Thread => ({
  id,
  folderId,
  accountId,
  subject: `Soggetto ${id}`,
  messageIds: [`m-${id}`],
  from: { name: "Mittente", email: "mitt@x.dev" },
  to: [],
  snippet: "",
  date,
  isUnread: false,
  isStarred: false,
  labels: [],
  messageCount: 1,
  hasAttachments: false,
})
const threads: Thread[] = [
  mkThread("t1", "f1", "a1", "2026-08-25T09:00:00Z"),
  mkThread("t2", "f2", "a2", "2026-08-28T09:00:00Z"),
  mkThread("t3", "f3", "a1", "2026-08-30T09:00:00Z"),
]

// seed the private store (compile-time private only)
const internal = svc as unknown as { accounts: Account[]; folders: Folder[]; threads: Thread[] }
internal.accounts = accounts
internal.folders = folders
internal.threads = threads

test("unified view merges every account's inbox, archive excluded", async () => {
  const res = await svc.getThreads(undefined, { accountId: "all" })
  expect(res.map((t) => t.id)).toEqual(["t2", "t1"]) // newest first, no t3
})

test("account-scoped view returns only that account's inbox", async () => {
  const res = await svc.getThreads(undefined, { accountId: "a1" })
  expect(res.map((t) => t.id)).toEqual(["t1"])
})

test("per-folder fetch still works alongside the unified view", async () => {
  const res = await svc.getThreads("f2")
  expect(res.map((t) => t.id)).toEqual(["t2"])
})

test("unified view honours a custom folder type", async () => {
  const res = await svc.getThreads(undefined, { accountId: "all", folderType: "archive" })
  expect(res.map((t) => t.id)).toEqual(["t3"])
})
