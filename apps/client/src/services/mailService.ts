import type {
  Account,
  ComposeMessage,
  Contact,
  DraftInput,
  Folder,
  Message,
  Rule,
  SearchQuery,
  SearchResult,
  Thread,
} from "../types"

/**
 * MailService — the single contract between the UI and the mail engine.
 *
 * This is implemented by LocalMailService, an empty local store: no accounts
 * and no messages exist until a real one is connected. In the shipped app the
 * same interface is backed by Tauri IPC to the Go mail engine, which
 * reads/writes local SQLite. The UI never touches SQLite, IMAP or SMTP
 * directly — it only ever talks to this interface.
 */
/**
 * Options for the account-level thread views. When accountId is set the
 * engine ignores folderId: the unified inbox joins every account's folder of
 * the given type (default "inbox"); a specific account id scopes the view.
 */
export interface ThreadListOptions {
  accountId?: string
  folderType?: string
}

export interface MailService {
  getAccounts(): Promise<Account[]>

  /** M8 rules engine (optional: local demo store has none) */
  listRules?(): Promise<Rule[]>

  saveRule?(rule: Rule): Promise<Rule>

  deleteRule?(id: string): Promise<void>

  /** M9 PGP: does the account have a private key in the vault? */
  pgpStatus?(accountId: string): Promise<{
    hasKey: boolean
    fingerprint: string
    pubKeys: { email: string; fingerprint: string }[]
  }>

  getFolders(accountId: string): Promise<Folder[]>

  getThreads(folderId: string | undefined, opts?: ThreadListOptions): Promise<Thread[]>

  getThread(threadId: string): Promise<Thread>

  getMessage(messageId: string): Promise<Message>

  search(query: SearchQuery): Promise<SearchResult[]>

  markAsRead(messageId: string): Promise<void>

  /** marks every message of the thread read (local + server \Seen) */
  markThreadRead(threadId: string): Promise<void>

  /** clears the read flag on every message of the thread (mark as unread) */
  markThreadUnread(threadId: string): Promise<void>

  archiveThread(threadId: string): Promise<void>

  /** moves the thread (and its messages) to a client-chosen folder:
   *  server-side IMAP MOVE + local folder switch. Used by archive and the
   *  spam / not-spam actions. */
  moveThread(threadId: string, targetFolderId: string): Promise<void>

  /** defers the thread: it returns to its folder at `until` (RFC 3339). */
  snoozeThread(threadId: string, until: string): Promise<void>

  /** restores a snoozed thread immediately. */
  unsnoozeThread(threadId: string): Promise<void>

  /** compose autocomplete: distinct recipients from the local history
   *  matching the query (optional: the local demo store implements it over
   *  its in-memory messages, the engine over the recipients table). */
  suggestContacts?(query: string): Promise<Contact[]>

  deleteMessage(messageId: string): Promise<void>

  /** queued: true → the message was parked in the outbox (offline). */
  sendMessage(
    message: ComposeMessage,
  ): Promise<{ queued?: boolean; scheduled?: boolean; opId?: string } | void>

  /** Asks the engine for an immediate outbox replay pass. */
  drainOutbox?(): Promise<void>

  /** Cancels one queued operation (undo-send / scheduled send). */
  cancelQueuedOp?(opId: string): Promise<void>

  /** Saves a draft (create or update) and returns its id. */
  saveDraft(draft: DraftInput): Promise<string>

  /** Deletes a draft locally and from the server when possible. */
  deleteDraft(draftId: string): Promise<void>

  sync(accountId: string): Promise<void>

  /** Google (Gmail) OAuth2 flow (Tauri only). */
  oauthGoogleStart?(): Promise<{ email: string; refreshToken: string }>

  /** Microsoft (Outlook) OAuth2 device-code flow (desktop only). */
  oauthMicrosoftBegin?(): Promise<{
    deviceCode: string
    userCode: string
    verificationUri: string
    intervalSecs: number
  }>
  oauthMicrosoftWait?(
    deviceCode: string,
    intervalSecs: number,
  ): Promise<{ email: string; refreshToken: string }>
}

/** Returns the active MailService implementation. */
export async function createMailService(): Promise<MailService> {
  // Local store for now (empty by default). The real implementation will talk
  // to Go over Tauri IPC; see docs/ipc.md.
  const { LocalMailService } = await import("../services/localMailService")
  return new LocalMailService()
}
