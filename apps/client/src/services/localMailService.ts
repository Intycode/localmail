import type {
  Account,
  ComposeMessage,
  Contact,
  DraftInput,
  Folder,
  Message,
  SearchQuery,
  SearchResult,
  Thread,
} from "../types"
import type { MailService, ThreadListOptions } from "./mailService"

/**
 * LocalMailService — the empty, local-first implementation of MailService.
 *
 * It ships no seed data: no accounts and no messages exist until a real mail
 * account is connected. Everything the UI asks for is answered from this local
 * store, which is empty by default, so the app starts in a clean state and the
 * UI renders its empty states instead of invented content.
 *
 * When the Go engine lands over Tauri IPC (see docs/ipc.md) it replaces this
 * class behind the same MailService contract — the UI does not change.
 */
export class LocalMailService implements MailService {
  private accounts: Account[] = []
  private folders: Folder[] = []
  private threads: Thread[] = []
  private messages: Message[] = []

  async getAccounts(): Promise<Account[]> {
    return this.accounts.map((a) => ({ ...a, folders: this.foldersFor(a.id) }))
  }

  async getFolders(accountId: string): Promise<Folder[]> {
    return this.foldersFor(accountId)
  }

  async getThreads(folderId: string | undefined, opts?: ThreadListOptions): Promise<Thread[]> {
    const byDate = (a: Thread, b: Thread) => Date.parse(b.date) - Date.parse(a.date)
    if (opts?.accountId) {
      // account-level view: every folder of the given type, optionally scoped
      // to one account ("all" = every account) — mirrors the engine's JOIN
      const type = opts.folderType ?? "inbox"
      const folderIds = new Set(
        this.folders
          .filter(
            (f) => f.type === type && (opts.accountId === "all" || f.accountId === opts.accountId),
          )
          .map((f) => f.id),
      )
      return this.threads.filter((t) => folderIds.has(t.folderId)).sort(byDate)
    }
    return this.threads.filter((t) => t.folderId === folderId).sort(byDate)
  }

  async getThread(threadId: string): Promise<Thread> {
    const thread = this.threads.find((t) => t.id === threadId)
    if (!thread) throw new Error(`thread ${threadId} not found`)
    return thread
  }

  async getMessage(messageId: string): Promise<Message> {
    const message = this.messages.find((m) => m.id === messageId)
    if (!message) throw new Error(`message ${messageId} not found`)
    return message
  }

  async search(query: SearchQuery): Promise<SearchResult[]> {
    const q = query.query.trim().toLowerCase()
    if (!q) return []
    const results: SearchResult[] = []
    for (const t of this.threads) {
      if (query.folderId && t.folderId !== query.folderId) continue
      if (query.accountId && t.accountId !== query.accountId) continue
      const msgs = this.messages.filter((m) => t.messageIds.includes(m.id))
      for (const m of msgs) {
        let matchedOn: SearchResult["matchedOn"] | null = null
        if (m.subject.toLowerCase().includes(q)) matchedOn = "subject"
        else if (m.from.email.toLowerCase().includes(q) || m.from.name.toLowerCase().includes(q))
          matchedOn = "sender"
        else if (
          m.to.some((r) => r.email.toLowerCase().includes(q) || r.name.toLowerCase().includes(q))
        )
          matchedOn = "recipient"
        else if (m.body.toLowerCase().includes(q)) matchedOn = "body"
        if (matchedOn) {
          results.push({
            threadId: t.id,
            subject: t.subject,
            snippet: m.body.slice(0, 140),
            matchedOn,
            date: m.date,
          })
          break
        }
      }
    }
    return results.slice(0, 50)
  }

  async markAsRead(messageId: string): Promise<void> {
    const m = this.messages.find((x) => x.id === messageId)
    if (m) m.isUnread = false
  }

  async markThreadRead(threadId: string): Promise<void> {
    for (const m of this.messages) if (m.threadId === threadId) m.isUnread = false
    const t = this.threads.find((x) => x.id === threadId)
    if (t) t.isUnread = false
  }

  async markThreadUnread(threadId: string): Promise<void> {
    for (const m of this.messages) if (m.threadId === threadId) m.isUnread = true
    const t = this.threads.find((x) => x.id === threadId)
    if (t) t.isUnread = true
  }

  async archiveThread(threadId: string): Promise<void> {
    const archiveId = this.folders.find((f) => f.type === "archive")?.id
    if (!archiveId) return
    await this.moveThread(threadId, archiveId)
  }

  async moveThread(threadId: string, targetFolderId: string): Promise<void> {
    const t = this.threads.find((x) => x.id === threadId)
    if (t) t.folderId = targetFolderId
    for (const m of this.messages) if (m.threadId === threadId) m.folderId = targetFolderId
  }

  async snoozeThread(threadId: string, until: string): Promise<void> {
    // browser demo semantics: park in the snoozed folder, restore on timer
    let folder = this.folders.find((f) => f.type === "snoozed")
    if (!folder) {
      folder = {
        id: "f-demo-snoozed",
        accountId: this.accounts[0]?.id ?? "demo",
        name: "Snoozed",
        type: "snoozed",
        total: 0,
        unread: 0,
      }
      this.folders.push(folder)
    }
    await this.moveThread(threadId, folder.id)
    const delay = Math.max(0, Date.parse(until) - Date.now())
    const inbox = this.folders.find((f) => f.type === "inbox")?.id
    if (inbox) setTimeout(() => void this.moveThread(threadId, inbox), delay)
  }

  async unsnoozeThread(threadId: string): Promise<void> {
    const inbox = this.folders.find((f) => f.type === "inbox")?.id
    if (inbox) await this.moveThread(threadId, inbox)
  }

  async suggestContacts(query: string): Promise<Contact[]> {
    const q = (query.split(",").pop() ?? "").trim().toLowerCase()
    if (!q) return []
    const found = new Map<string, Contact>()
    for (const m of this.messages) {
      for (const r of [...(m.to ?? []), ...(m.cc ?? [])]) {
        if (!r.email) continue
        if (!r.email.toLowerCase().includes(q) && !r.name.toLowerCase().includes(q)) continue
        found.set(r.email.toLowerCase(), { email: r.email, name: r.name })
      }
    }
    return [...found.values()].slice(0, 8)
  }

  async deleteMessage(messageId: string): Promise<void> {
    const idx = this.messages.findIndex((m) => m.id === messageId)
    if (idx >= 0) this.messages.splice(idx, 1)
  }

  async saveDraft(draft: DraftInput): Promise<string> {
    const account = this.accounts.find((a) => a.isActive) ?? this.accounts[0]
    if (!account) {
      throw new Error("Nessun account configurato: aggiungi un account prima di salvare bozze.")
    }
    let draftsId = this.folders.find((f) => f.accountId === account.id && f.type === "drafts")?.id
    if (!draftsId) {
      draftsId = `f-drafts-${account.id}`
      this.folders.push({
        id: draftsId,
        accountId: account.id,
        name: "Bozze",
        type: "drafts",
        total: 0,
        unread: 0,
      })
    }
    const id = draft.id ?? `m-draft-${Date.now()}`
    const threadId = `t-draft-${Date.now()}`
    // replace a previously stored version of the same draft
    const prev = this.messages.findIndex((m) => m.id === id)
    if (prev >= 0) this.messages.splice(prev, 1)
    const threadIdx = this.threads.findIndex((t) => t.messageIds.includes(id))
    if (threadIdx >= 0) this.threads.splice(threadIdx, 1)
    const msg: Message = {
      id,
      threadId,
      folderId: draftsId,
      accountId: account.id,
      subject: draft.subject,
      from: { name: account.name, email: account.email },
      to: draft.to,
      cc: draft.cc,
      bcc: draft.bcc,
      date: new Date().toISOString(),
      // same convention as the engine: body carries the html part when set
      body: draft.htmlBody || draft.body,
      bodyText: draft.body,
      remoteImages: 0,
      isEncrypted: false,
      isSigned: false,
      spamScore: 0,
      phishing: [],
      isUnread: false,
      isStarred: false,
      labels: [],
      attachments: [],
    }
    this.messages.push(msg)
    this.threads.unshift({
      id: threadId,
      folderId: draftsId,
      accountId: account.id,
      subject: draft.subject || "(senza oggetto)",
      snippet: draft.body.slice(0, 140),
      date: msg.date,
      isUnread: false,
      isStarred: false,
      labels: [],
      messageIds: [id],
      messageCount: 1,
      hasAttachments: false,
      from: msg.from,
      to: draft.to,
    })
    return id
  }

  async deleteDraft(draftId: string): Promise<void> {
    const idx = this.messages.findIndex((m) => m.id === draftId)
    if (idx >= 0) this.messages.splice(idx, 1)
    const tIdx = this.threads.findIndex((t) => t.messageIds.includes(draftId))
    if (tIdx >= 0) this.threads.splice(tIdx, 1)
  }

  async sendMessage(message: ComposeMessage): Promise<void> {
    const account = this.accounts.find((a) => a.isActive) ?? this.accounts[0]
    if (!account) {
      throw new Error("Nessun account configurato: aggiungi un account prima di inviare.")
    }
    const sentFolderId = this.folders.find(
      (f) => f.accountId === account.id && f.type === "sent",
    )?.id
    if (!sentFolderId) {
      throw new Error(`Cartella Sent mancante per l'account ${account.id}.`)
    }
    const id = `m-sent-${Date.now()}`
    const threadId = `t-sent-${Date.now()}`
    const sent: Message = {
      id,
      threadId,
      folderId: sentFolderId,
      accountId: account.id,
      subject: message.subject,
      from: { name: account.name, email: account.email },
      to: message.to,
      cc: message.cc,
      bcc: message.bcc,
      date: new Date().toISOString(),
      // same convention as the engine: body carries the html part when set
      body: message.htmlBody || message.body,
      bodyText: message.body,
      remoteImages: 0,
      isEncrypted: false,
      isSigned: false,
      spamScore: 0,
      phishing: [],
      isUnread: false,
      isStarred: false,
      labels: [],
      attachments: message.attachments,
    }
    this.messages.push(sent)
    this.threads.unshift({
      id: threadId,
      folderId: sentFolderId,
      accountId: account.id,
      subject: message.subject,
      messageIds: [id],
      from: sent.from,
      to: message.to,
      snippet: message.body.slice(0, 120),
      date: sent.date,
      isUnread: false,
      isStarred: false,
      labels: [],
      messageCount: 1,
      hasAttachments: message.attachments.length > 0,
    })
  }

  async sync(_accountId: string): Promise<void> {
    // No-op until the Go engine is bridged over IPC: there is no local data to
    // refresh and no provider to pull from yet.
  }

  private foldersFor(accountId: string): Folder[] {
    return this.folders.filter((f) => f.accountId === accountId)
  }
}
