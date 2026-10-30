import { invoke } from "@tauri-apps/api/core"
import { toast } from "sonner"
import type {
  Account,
  ComposeMessage,
  Contact,
  DraftInput,
  Folder,
  ImapAccountDraft,
  Message,
  Rule,
  SearchQuery,
  SearchResult,
  Thread,
} from "../types"
import type { MailService, ThreadListOptions } from "./mailService"

/**
 * TauriMailService — MailService backed by the Go engine through the Tauri
 * bridge (src-tauri/src/engine.rs). The UI only ever speaks high-level ops;
 * SQLite/IMAP internals stay in the engine. Secret-bearing operations
 * (accounts.add, sync.start) go through dedicated commands so the password
 * is fetched from the OS keychain by Rust and never round-trips the webview.
 */

interface EngineEnvelope<T> {
  ok: boolean
  data?: T
  error?: string
}

/** true when running inside the Tauri webview (vs plain browser dev). */
export function isTauri(): boolean {
  return typeof window !== "undefined" && "__TAURI_INTERNALS__" in window
}

async function call<T>(command: string, args: Record<string, unknown>): Promise<T> {
  const res = await invoke<EngineEnvelope<T>>(command, args)
  if (!res.ok) throw new Error(res.error ?? "Errore del motore di posta")
  return res.data as T
}

async function engineInvoke<T>(op: string, params: Record<string, unknown> = {}): Promise<T> {
  return call<T>("engine_invoke", { op, params })
}

/** Native file dialog shown by Rust: the file-touching commands accept only
 *  paths picked here (src-tauri/src/paths.rs), so a script injected through
 *  a hostile email cannot name ~/.zshrc. Returns [] when cancelled. */
export async function pickPath(
  mode: "save" | "open" | "openMany" | "folder",
  opts: {
    defaultPath?: string
    title?: string
    filters?: { name: string; extensions: string[] }[]
  } = {},
): Promise<string[]> {
  const res = await call<{ paths: string[] }>("pick_path", { mode, ...opts })
  return res.paths
}

/** IOService — import/export/backup (M11). File paths come from pickPath;
 *  the engine writes/reads them directly. */
export const ioService = {
  async exportMessage(messageId: string, dest: string): Promise<{ path: string; size: number }> {
    return engineInvoke("export.message", { messageId, dest })
  },
  async exportMailbox(
    folderId: string,
    dest: string,
  ): Promise<{ exported: number; failed: number }> {
    return engineInvoke("export.mailbox", { folderId, dest })
  },
  async importMails(
    accountId: string,
    src: string,
  ): Promise<{ imported: number; skipped: number; folderId: string }> {
    return engineInvoke("import.mails", { accountId, src })
  },
  async backupCreate(dest: string): Promise<{ path: string; size: number }> {
    // dedicated Rust commands speak the same {ok,data,error} envelope as
    // engine_invoke: parse it so failures reach the UI instead of showing a
    // false success toast.
    return call<{ path: string; size: number }>("backup_create", { dest })
  },
  async backupRestore(src: string): Promise<void> {
    await call<void>("backup_restore", { src })
  },
}

/** SemanticSearchService — optional embedding layer (M10): status of the
 *  local AI server and chunked reindex. All ops degrade softly: without the
 *  server the app stays on keyword search. Model config moved to aiService
 *  (M12). */
export const semanticSearchService = {
  async status(): Promise<{ ollama: boolean; model: string; indexed: number; total: number }> {
    return engineInvoke("search.status", {})
  },
  async reindex(limit = 200): Promise<{ indexed: number; remaining: number }> {
    return engineInvoke("search.reindex", { limit })
  },
}

/** Local AI server settings and chat (M12): one user-configured endpoint —
 *  Ollama natively or any OpenAI-compatible local server (LM Studio,
 *  llama.cpp...). The engine refuses non-loopback endpoints unless the user
 *  explicitly opted in; mail content never leaves the machine otherwise. */
export interface AIChatMessage {
  role: "system" | "user" | "assistant"
  content: string
  /** data URLs ("data:image/jpeg;base64,...") for vision models: pages of
   *  scanned PDFs. Ollama gets bare base64, OpenAI-compatible the URL. */
  images?: string[]
}

export interface AIStatus {
  available: boolean
  url: string
  kind: "ollama" | "openai"
  chatModel: string
  embedModel: string
  allowRemote: boolean
  /** set when the configured endpoint is blocked or invalid */
  error?: string
}

export const aiService = {
  async status(): Promise<AIStatus> {
    return engineInvoke("ai.status", {})
  },
  async models(): Promise<{ models: string[] }> {
    return engineInvoke("ai.models", {})
  },
  /** Empty fields leave the stored value unchanged (engine-side rule). */
  async config(patch: {
    url?: string
    kind?: string
    chatModel?: string
    embedModel?: string
    allowRemote?: boolean
  }): Promise<AIStatus> {
    return engineInvoke("ai.config", patch)
  },
  async chat(messages: AIChatMessage[]): Promise<{ content: string }> {
    return engineInvoke("ai.chat", { messages })
  },
}

/** PGP commands go through Rust (keychain bridge): the private key never
 *  crosses the webview boundary. Rust commands return the same envelope as
 *  engine_invoke, so parse it here too (errors must not be swallowed). */
async function tauriInvoke<T>(cmd: string, args: Record<string, unknown> = {}): Promise<T> {
  if (!isTauri()) throw new Error("comando disponibile solo nell'app Tauri")
  return call<T>(cmd, args)
}

/** PGPService — OpenPGP key management (M9). */
export const pgpService = {
  async status(accountId: string): Promise<{
    hasKey: boolean
    fingerprint: string
    pubKeys: { email: string; fingerprint: string }[]
  }> {
    return engineInvoke("pgp.status", { accountId })
  },
  async generate(
    accountId: string,
    name: string,
    email: string,
  ): Promise<{ publicKey: string; fingerprint: string }> {
    return tauriInvoke("pgp_generate", { accountId, name, email })
  },
  async importPrivate(accountId: string, armored: string): Promise<{ ok: boolean }> {
    return tauriInvoke("pgp_import_private", { accountId, armored })
  },
  async remove(accountId: string): Promise<void> {
    await tauriInvoke("pgp_delete", { accountId })
  },
  async exportPublic(accountId: string): Promise<{ publicKey: string }> {
    return engineInvoke("pgp.exportPublic", { accountId })
  },
  async importPub(email: string, armored: string): Promise<{ fingerprint: string }> {
    return engineInvoke("pgp.pubImport", { email, armored })
  },
  async listPub(): Promise<{ email: string; fingerprint: string }[]> {
    return engineInvoke("pgp.pubList")
  },
  async deletePub(email: string): Promise<void> {
    await engineInvoke("pgp.pubDelete", { email })
  },
}

/** AttachmentService — on-demand attachment operations that need the
 *  keychain secret: the dedicated Rust commands inject it, the webview only
 *  sees the result. */
export const attachmentService = {
  /** Plain text of a PDF attachment (M12): fetched raw, extracted and
   *  parsed engine-side so the local AI can read it. Scanned PDFs return
   *  an empty text; password-protected ones raise a clear error. */
  async text(
    accountId: string,
    messageId: string,
    name: string,
  ): Promise<{ text: string; chars: number; truncated: boolean; pages: number }> {
    return tauriInvoke("attachment_text", { accountId, messageId, name })
  },
  /** Inline image preview: the engine sniffs the content type and caps the
   *  size; `data` arrives base64-encoded (engine []byte → JSON string) and
   *  the UI turns it into a data: URL. */
  async preview(
    accountId: string,
    messageId: string,
    name: string,
  ): Promise<{ mime: string; data: string; size: number }> {
    return tauriInvoke("attachment_preview", { accountId, messageId, name })
  },
  /** Rendered pages of a PDF attachment (M12 vision path): for scanned
   *  documents the multimodal model reads the JPEG pages instead. `data`
   *  arrives base64-encoded (engine []byte → JSON string). */
  async images(
    accountId: string,
    messageId: string,
    name: string,
  ): Promise<{
    name: string
    rendered: number
    totalPage: number
    pages: { index: number; mime: string; data: string }[]
  }> {
    return tauriInvoke("attachment_images", { accountId, messageId, name })
  },
}

// The engine marshals empty Go slices as JSON null; normalize entity fields
// once here so the UI can rely on arrays (same for threads and messages).
export function normalizeThread(thread: Thread): Thread {
  return {
    ...thread,
    messageIds: thread.messageIds ?? [],
    to: thread.to ?? [],
    labels: thread.labels ?? [],
  }
}

export function normalizeMessage(message: Message): Message {
  return {
    ...message,
    to: message.to ?? [],
    cc: message.cc ?? [],
    bcc: message.bcc ?? [],
    labels: message.labels ?? [],
    attachments: message.attachments ?? [],
  }
}

/** Extra account-administration operations available under Tauri. */
export interface AccountAdminService {
  addAccount(draft: ImapAccountDraft): Promise<Account>
  removeAccount(accountId: string): Promise<void>
  /** Persist the active account in the engine (SQLite), not just the UI. */
  setActive(accountId: string): Promise<void>
}

export class TauriMailService implements MailService, AccountAdminService {
  async getAccounts(): Promise<Account[]> {
    // the engine marshals empty Go slices as null: normalize for the UI
    const accounts = (await engineInvoke<Account[] | null>("accounts.list")) ?? []
    return accounts.map((a) => ({ ...a, folders: a.folders ?? [] }))
  }

  async getFolders(accountId: string): Promise<Folder[]> {
    return (await engineInvoke<Folder[] | null>("folders.list", { accountId })) ?? []
  }

  async pgpStatus(accountId: string): Promise<{
    hasKey: boolean
    fingerprint: string
    pubKeys: { email: string; fingerprint: string }[]
  }> {
    const st = await engineInvoke<{
      hasKey: boolean
      fingerprint: string
      pubKeys: { email: string; fingerprint: string }[] | null
    }>("pgp.status", { accountId })
    return { ...st, pubKeys: st.pubKeys ?? [] }
  }

  async listRules(): Promise<Rule[]> {
    return (await engineInvoke<Rule[] | null>("rules.list")) ?? []
  }

  async saveRule(rule: Rule): Promise<Rule> {
    return engineInvoke<Rule>("rules.save", { ...rule })
  }

  async deleteRule(id: string): Promise<void> {
    await engineInvoke("rules.delete", { id })
  }

  async getThreads(folderId: string | undefined, opts?: ThreadListOptions): Promise<Thread[]> {
    // account-level view: the engine ignores folderId when accountId is set
    const threads =
      (await engineInvoke<Thread[] | null>("threads.list", {
        folderId: folderId ?? "",
        accountId: opts?.accountId ?? "",
        type: opts?.folderType ?? "",
      })) ?? []
    return threads.map(normalizeThread)
  }

  async getThread(threadId: string): Promise<Thread> {
    return normalizeThread(await engineInvoke<Thread>("thread.get", { threadId }))
  }

  async getMessage(messageId: string): Promise<Message> {
    return normalizeMessage(await engineInvoke<Message>("message.get", { messageId }))
  }

  async search(query: SearchQuery): Promise<SearchResult[]> {
    return (await engineInvoke<SearchResult[] | null>("search.query", { ...query })) ?? []
  }

  async markAsRead(messageId: string): Promise<void> {
    await engineInvoke("message.markRead", { messageId })
  }

  async markThreadRead(threadId: string): Promise<void> {
    await engineInvoke("thread.markRead", { threadId })
  }

  async markThreadUnread(threadId: string): Promise<void> {
    await engineInvoke("thread.markUnread", { threadId })
  }

  async archiveThread(threadId: string): Promise<void> {
    await engineInvoke("thread.archive", { threadId })
  }

  async moveThread(threadId: string, targetFolderId: string): Promise<void> {
    await engineInvoke("thread.move", { threadId, target: targetFolderId })
  }

  async snoozeThread(threadId: string, until: string): Promise<void> {
    await engineInvoke("thread.snooze", { threadId, until })
  }

  async unsnoozeThread(threadId: string): Promise<void> {
    await engineInvoke("thread.unsnooze", { threadId })
  }

  async suggestContacts(query: string): Promise<Contact[]> {
    const res = await engineInvoke<Contact[]>("contacts.suggest", { query, limit: 8 })
    return res ?? []
  }

  async deleteMessage(messageId: string): Promise<void> {
    await engineInvoke("message.delete", { messageId })
  }

  async saveDraft(draft: DraftInput): Promise<string> {
    const accounts = await this.getAccounts()
    const active = accounts.find((a) => a.isActive) ?? accounts[0]
    const res = await call<{ draftId: string }>("draft.save", {
      ...(active ? { accountId: active.id } : {}),
      draftId: draft.id,
      message: {
        to: draft.to,
        cc: draft.cc,
        bcc: draft.bcc,
        subject: draft.subject,
        body: draft.body,
        htmlBody: draft.htmlBody,
      },
    })
    return res.draftId
  }

  async deleteDraft(draftId: string): Promise<void> {
    await call("draft.delete", { draftId })
  }

  async sendMessage(message: ComposeMessage): Promise<{ queued?: boolean }> {
    // dedicated command: the SMTP secret is read from the OS keychain in
    // Rust (account_send) and never round-trips the webview
    const accounts = await this.getAccounts()
    const active = accounts.find((a) => a.isActive) ?? accounts[0]
    if (!active) {
      throw new Error("Nessun account configurato: aggiungi un account prima di inviare.")
    }
    // queued: true → offline, the message was parked in the engine's outbox
    return call<{ queued?: boolean }>("account_send", { accountId: active.id, message })
  }

  /** Asks the engine for an immediate outbox replay pass (back online). */
  async drainOutbox(): Promise<void> {
    await call("outbox.drain", {})
  }

  /** Cancels one queued operation (undo-send / scheduled send). */
  async cancelQueuedOp(opId: string): Promise<void> {
    await engineInvoke("outbox.cancel", { id: opId })
  }

  async sync(accountId: string): Promise<void> {
    // dedicated command: the secret is read from the OS keychain in Rust
    await call("account_sync", { accountId })
  }

  async addAccount(draft: ImapAccountDraft): Promise<Account> {
    // dedicated command: the secret is persisted to the OS keychain in Rust
    return call<Account>("account_add", {
      name: draft.name,
      email: draft.email,
      imap: draft.imap,
      secret: draft.secret,
      provider: draft.provider,
      allowInsecure: draft.allowInsecure ?? false,
    })
  }

  /** Google (Gmail) OAuth2: opens the system browser, waits for the
   *  loopback redirect and returns { email, refreshToken }. */
  async oauthGoogleStart(): Promise<{ email: string; refreshToken: string }> {
    if (!isTauri()) {
      throw new Error("L'accesso con Google richiede l'app desktop.")
    }
    return call<{ email: string; refreshToken: string }>("oauth_google_start", {})
  }

  /** Microsoft (Outlook) OAuth2 device-code flow: step 1 returns the code
   *  to type (the browser is already open on devicelogin), step 2 polls
   *  until the user completes and returns { email, refreshToken }. */
  async oauthMicrosoftBegin(): Promise<{
    deviceCode: string
    userCode: string
    verificationUri: string
    intervalSecs: number
  }> {
    if (!isTauri()) {
      throw new Error("L'accesso con Microsoft richiede l'app desktop.")
    }
    return call<{
      deviceCode: string
      userCode: string
      verificationUri: string
      intervalSecs: number
    }>("oauth_microsoft_begin", {})
  }

  async oauthMicrosoftWait(
    deviceCode: string,
    intervalSecs: number,
  ): Promise<{ email: string; refreshToken: string }> {
    return call<{ email: string; refreshToken: string }>("oauth_microsoft_wait", {
      deviceCode,
      intervalSecs,
    })
  }

  async removeAccount(accountId: string): Promise<void> {
    await call("account_remove", { accountId })
  }

  async setActive(accountId: string): Promise<void> {
    await engineInvoke("accounts.setActive", { accountId })
  }
}

/** Save-dialog + engine download for one attachment. Returns the chosen
 *  path, or null when the dialog was cancelled (browser: not supported).
 *  accountId must be the OWNING message's account, not the active one. */
export async function exportAttachment(
  accountId: string,
  messageId: string,
  name: string,
): Promise<string | null> {
  if (!isTauri()) {
    toast.info("Il salvataggio degli allegati richiede l'app desktop.")
    return null
  }
  const [dest] = await pickPath("save", { defaultPath: name })
  if (!dest) return null
  const res = await call<{ path: string }>("attachment_export", {
    accountId,
    messageId,
    name,
    dest,
  })
  return res.path
}

/** Opens an attachment with the OS default application: the engine downloads
 *  the part into the per-message cache dir and Tauri launches it. */
export async function openAttachment(
  accountId: string,
  messageId: string,
  name: string,
): Promise<string> {
  if (!isTauri()) {
    throw new Error("L'apertura degli allegati richiede l'app desktop.")
  }
  const res = await call<{ path: string }>("attachment_open", {
    accountId,
    messageId,
    name,
  })
  return res.path
}

/** One-click download into the system Downloads folder (no dialog). The
 *  engine picks a non-clashing file name automatically. Returns the path. */
export async function downloadAttachment(
  accountId: string,
  messageId: string,
  name: string,
): Promise<string> {
  if (!isTauri()) {
    throw new Error("Il download degli allegati richiede l'app desktop.")
  }
  const res = await call<{ path: string }>("attachment_download", {
    accountId,
    messageId,
    name,
  })
  return res.path
}
