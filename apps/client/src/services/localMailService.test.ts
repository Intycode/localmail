import { test, expect } from "bun:test"
import { LocalMailService } from "./localMailService"
import { getMailService, _resetMailService } from "./serviceRegistry"
import type { Account, ComposeMessage, Folder } from "../types"

const svc = new LocalMailService()

test("starts with no accounts", async () => {
  expect(await svc.getAccounts()).toEqual([])
})

test("starts with no folders and no threads", async () => {
  expect(await svc.getFolders("no-such-account")).toEqual([])
  expect(await svc.getThreads("no-such-folder")).toEqual([])
})

test("search finds nothing to find", async () => {
  expect(await svc.search({ query: "roadmap" })).toEqual([])
  expect(await svc.search({ query: "" })).toEqual([])
})

test("unknown thread and message raise", async () => {
  let threadErr: unknown = null
  try {
    await svc.getThread("t1")
  } catch (e: unknown) {
    threadErr = e
  }
  expect(threadErr).toBeInstanceOf(Error)

  let messageErr: unknown = null
  try {
    await svc.getMessage("m1")
  } catch (e: unknown) {
    messageErr = e
  }
  expect(messageErr).toBeInstanceOf(Error)
})

test("actions are safe no-ops on the empty store", async () => {
  await svc.markAsRead("m1")
  await svc.archiveThread("t1")
  await svc.deleteMessage("m1")
  await svc.sync("no-such-account")
  expect(await svc.getThreads("no-such-folder")).toEqual([])
})

test("sendMessage refuses without a configured account", async () => {
  const message: ComposeMessage = {
    to: [{ name: "", email: "anna@acme.dev" }],
    cc: [],
    bcc: [],
    subject: "Test invio",
    body: "ciao",
    attachments: [],
  }
  let err: unknown = null
  try {
    await svc.sendMessage(message)
  } catch (e: unknown) {
    err = e
  }
  expect(err).toBeInstanceOf(Error)
  expect((err as Error).message).toContain("Nessun account")
})

test("serviceRegistry lazily creates a single instance", async () => {
  _resetMailService()
  const a = await getMailService()
  const b = await getMailService()
  expect(a).toBe(b)
})

test("drafts round-trip through save/get/update/delete", async () => {
  const { LocalMailService } = await import("./localMailService")
  const local = new LocalMailService()
  // seed an account so drafts have an owner (private fields, test-only)
  const internal = local as unknown as {
    accounts: Account[]
    folders: Folder[]
  }
  internal.accounts.push({
    id: "acc1",
    name: "Luca",
    email: "luca@x.dev",
    provider: "generic-imap",
    isActive: true,
    folders: [],
  })

  const draftId = await local.saveDraft({
    to: [{ name: "", email: "anna@acme.dev" }],
    cc: [],
    bcc: [],
    subject: "Bozza",
    body: "prima versione",
  })
  expect(draftId).toBeTruthy()

  const saved = await local.getMessage(draftId)
  expect(saved.subject).toBe("Bozza")
  expect(saved.body).toBe("prima versione")

  // update keeps the id
  const again = await local.saveDraft({
    id: draftId,
    to: [{ name: "", email: "anna@acme.dev" }],
    cc: [],
    bcc: [],
    subject: "Bozza rivista",
    body: "seconda versione",
  })
  expect(again).toBe(draftId)
  const updated = await local.getMessage(draftId)
  expect(updated.subject).toBe("Bozza rivista")

  await local.deleteDraft(draftId)
  let err: unknown = null
  try {
    await local.getMessage(draftId)
  } catch (e: unknown) {
    err = e
  }
  expect(err).toBeInstanceOf(Error)
})
