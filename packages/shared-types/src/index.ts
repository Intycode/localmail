/**
 * Shared domain types for Mail.
 *
 * These types are the single source of truth for the data model and are used
 * by both the Astro frontend (apps/client) and, conceptually, by the Go mail
 * engine (backend). Keeping them simple and stable is a deliberate choice:
 * they form the contract between the UI and the engine, and must never leak
 * IMAP/SQLite internals.
 */

export interface Account {
  id: string
  name: string
  email: string
  /** provider kind: "gmail" | "microsoft" | "generic-imap" | "mock" */
  provider: string
  /** true when the account is currently active in the UI */
  isActive: boolean
  /** IMAP connection details (generic-imap accounts); no secrets here */
  imap?: ImapConfig | null
  /** optional outgoing SMTP details; empty = derived from IMAP at send time */
  smtp?: SmtpConfig | null
  /** folders owned by this account, see Folder */
  folders: Folder[]
}

/** Draft payload: a compose state persisted local-first (IMAP APPEND into
 *  Drafts is best effort). An existing `id` means "update this draft". */
export interface DraftInput {
  id?: string
  to: Recipient[]
  cc: Recipient[]
  bcc: Recipient[]
  subject: string
  /** plain-text body (also the text part of a multipart send) */
  body: string
  /** optional HTML body: when set the message goes out as multipart/alternative */
  htmlBody?: string
}

/** SMTP (outgoing) connection details for an account. No secrets here: the
 *  password is the account secret living in the OS keychain. */
export interface SmtpConfig {
  host: string
  port: number
  /** "tls" = implicit TLS (465) | "starttls" (587) | "plain" (explicit opt-in) */
  security: "tls" | "starttls" | "plain"
  username: string
  /** SASL mechanism: "password" (default) | "oauth" (XOAUTH2, M4) */
  auth?: "password" | "oauth"
}

/** IMAP connection details for a generic IMAP account (no secrets). */
export interface ImapConfig {
  host: string
  port: number
  /** "tls" = implicit TLS (993) | "starttls" | "plain" (explicit opt-in) */
  security: "tls" | "starttls" | "plain"
  username: string
  /** SASL mechanism: "password" (default) | "oauth" (XOAUTH2, M4) */
  auth?: "password" | "oauth"
}

/** Draft for adding a generic IMAP account. The secret goes to the OS
 *  keychain via Tauri and is never stored in SQLite or the UI. */
export interface ImapAccountDraft {
  name: string
  email: string
  imap: ImapConfig
  /** optional explicit outgoing server; omitted = derived from IMAP */
  smtp?: SmtpConfig
  /** password, app-password or (OAuth accounts) the refresh token; lives
   *  only in the OS keychain */
  secret: string
  /** explicit opt-in for a non-encrypted connection */
  allowInsecure?: boolean
  /** provider kind: "generic-imap" (default) | "gmail-oauth" (M4) */
  provider?: string
}

export interface Folder {
  id: string
  accountId: string
  name: string
  /** Inbox, Sent, Archive, Drafts, Trash, Junk, Snoozed, Custom, Import (local-only, M11) */
  type: "inbox" | "sent" | "archive" | "drafts" | "trash" | "junk" | "snoozed" | "custom" | "import"
  /** total number of threads in the folder (from local SQLite) */
  total: number
  /** number of unread threads in the folder */
  unread: number
}

export interface Thread {
  id: string
  folderId: string
  accountId: string
  subject: string
  /** message ids in this thread, chronological order */
  messageIds: string[]
  /** sender of the first/latest message, see Recipient */
  from: Recipient
  /** recipients (to) of the latest message */
  to: Recipient[]
  /** short snippet of the latest message */
  snippet: string
  /** timestamp of the latest message, ISO 8601 */
  date: string
  isUnread: boolean
  isStarred: boolean
  /** labels applied to this thread, see Label */
  labels: Label[]
  /** message count */
  messageCount: number
  /** true when thread has at least one attachment */
  hasAttachments: boolean
}

export interface Message {
  id: string
  threadId: string
  folderId: string
  accountId: string
  subject: string
  from: Recipient
  to: Recipient[]
  cc: Recipient[]
  bcc: Recipient[]
  /** ISO 8601 */
  date: string
  body: string
  bodyText: string
  /** remote images left in the HTML body (anti-tracking M6): blocked until
   *  the user explicitly loads them */
  remoteImages: number
  /** local spam/phishing assessment (M7): 0..100 heuristic score plus
   *  human-readable phishing flags; UI warnings only, nothing auto-moves */
  spamScore: number
  phishing: string[]
  /** M9 OpenPGP: encrypted body (decrypted on view when the key is
   *  available) and signature status: valid | invalid | unknown */
  isEncrypted: boolean
  isSigned: boolean
  sigStatus?: string
  /** raw MIME kept on disk; not exposed to UI */
  isUnread: boolean
  isStarred: boolean
  labels: Label[]
  attachments: Attachment[]
  /** internal id of the message on the provider (e.g. IMAP UID) */
  providerMessageId?: string
  /** RFC 5322 threading metadata (local only; used to prefill replies) */
  messageId?: string
  inReplyTo?: string[]
  references?: string[]
}

export interface Recipient {
  name: string
  email: string
}

export interface Attachment {
  id: string
  messageId: string
  name: string
  /** bytes, lazily loaded */
  size: number
  contentType: string
}

/** Outgoing attachment referenced by path (content never crosses the IPC
 *  bridge: the engine reads the file at send time). */
export interface OutgoingFile {
  path: string
  name?: string
  contentType?: string
}

/** One address seen in the local mail history (compose autocomplete). */
export interface Contact {
  email: string
  name?: string
}

export interface Label {
  id: string
  name: string
  color?: string
}

export interface SearchQuery {
  /** free text matched against subject/sender/recipient/body */
  query: string
  folderId?: string
  accountId?: string
  before?: string
  after?: string
  /** "semantic" (M10, needs local Ollama + indexing) or omitted = keyword */
  mode?: "keyword" | "semantic"
}

export interface SearchResult {
  threadId: string
  subject: string
  snippet: string
  /** which field produced the match: subject | sender | recipient | body | semantic */
  matchedOn: "subject" | "sender" | "recipient" | "body" | "semantic"
  date: string
  /** cosine similarity 0..1, semantic results only */
  score?: number
}

/** Local mail rule (M8): one condition, one action, evaluated at ingest in
 *  position order. accountId omitted = the rule applies to every account. */
export interface Rule {
  id: string
  accountId?: string
  name: string
  enabled: boolean
  position: number
  /** from | to | subject | body | spam_score | has_attachment */
  field: string
  /** contains | equals | starts_with | ends_with | at_least | any */
  op: string
  value: string
  /** move | mark_read | star | mark_spam */
  action: string
  /** local folder id, required for the move action */
  target?: string
}

export interface ComposeMessage {
  to: Recipient[]
  cc: Recipient[]
  bcc: Recipient[]
  subject: string
  /** plain-text body (also the text part of a multipart send) */
  body: string
  /** optional HTML body: when set the message goes out as multipart/alternative */
  htmlBody?: string
  attachments: Attachment[]
  /** outgoing attachments: files picked via the native dialog, sent by path */
  files?: OutgoingFile[]
  /** RFC 3339: schedule the send (undo-send window or deferred delivery);
   *  the engine parks the message in the outbox until that moment, and the
   *  response carries the opId for cancellation */
  sendAt?: string
  /** M9 OpenPGP: clearsign the text part (PGP/Inline) */
  sign?: boolean
  /** M9 OpenPGP: encrypt for every recipient (needs their public keys);
   *  sends text-only, refused with attachments */
  encrypt?: boolean
  /** threading headers for replies: the sent message joins its thread */
  inReplyTo?: string
  references?: string[]
}

export type SyncStatus =
  | "idle"
  | "syncing"
  | "completed"
  | "error"

export type SyncEvent =
  | { type: "sync.started"; accountId: string }
  | { type: "sync.progress"; accountId: string; done: number; total: number }
  | { type: "sync.completed"; accountId: string; at: string }
  | { type: "sync.error"; accountId: string; message: string }

export type MessageEvent =
  | { type: "message.created"; message: Message }
  | { type: "message.updated"; message: Message }
  | { type: "message.deleted"; messageId: string }

export type ThreadEvent =
  | { type: "thread.updated"; thread: Thread }

export type AccountEvent =
  | { type: "account.updated"; account: Account }

export type AppEvent =
  | SyncEvent
  | MessageEvent
  | ThreadEvent
  | AccountEvent

/** A single page route of the app. */
export type Route =
  | { name: "inbox"; folderId: string; /** "all" = unified inbox, else account id */ accountId?: string }
  | { name: "thread"; threadId: string }
  | { name: "compose" }
  | { name: "search" }
  | { name: "settings" }
  | { name: "accounts" }

/** UI navigation state. */
export interface UiState {
  activeAccountId: string
  activeFolderId: string
  /** route name for keyboard-driven navigation */
  route: Route
  sidebarOpen: boolean
  compact: boolean
}