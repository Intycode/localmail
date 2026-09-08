# IPC design: Astro ↔ Tauri ↔ Go

The UI is never coupled to Go internals. It talks to the engine through a small,
structured message/event protocol.

## Channel map

```
Astro UI  ── invoke ──▶ Tauri (Rust)  ── process/pipe ──▶ Go engine
Astro UI  ◀── events ── Tauri         ◀── event bus  ─── Go engine
```

- **Requests (UI → engine):** `invoke` from Astro into Tauri commands, which
  forward to the Go engine over its stdio/pipe bridge.
- **Events (engine → UI):** the Go `events.Bus` publishes
  `sync.started|progress|completed|error`, `message.created|updated|deleted`,
  `thread.updated`, `account.updated`; Tauri subscribes and forwards them to the
  webview as `tauri` events. The frontend updates its stores — never polls.

## Message envelope

Requests (UI -> engine, one JSON per line on the engine's stdin):

```json
{
  "id": "req-1",
  "op": "sync.start",
  "params": { "accountId": "a1", "secret": "<from keychain>" }
}
```

Responses (engine stdout, matched by id):

```json
{ "id": "req-1", "ok": true, "data": { "...": "..." } }
{ "id": "req-1", "ok": false, "error": "autenticazione non riuscita ..." }
```

Events (engine stdout, no id, forwarded by Tauri as `mail://event`):

```json
{ "kind": "message.created", "data": { "messageId": "m3", "threadId": "t2", "subject": "…" } }
```

## Operations (static allowlist)

| Op | Params | Note |
|----|--------|------|
| accounts.list | — | non-secret IMAP **and SMTP** metadata only |
| accounts.add | name, email, imap{host,port,security,username}, smtp?{host,port,security,username}, secret, allowInsecure?, provider? ("generic-imap" default, "jmap") | dials first, persists on success; empty smtp = derived from IMAP at send time; for jmap imap.host is the session URL and smtp is ignored (EmailSubmission) — see docs/jmap.md |
| accounts.remove | accountId | stops the push watcher, purges local data + keychain entry |
| accounts.setActive | accountId | |
| folders.list | accountId | |
| pgp.status | accountId | hasKey + fingerprint + chiavi pubbliche contatti |
| pgp.generate | accountId, name, email | (via comando Rust pgp_generate: chiave privata solo verso keychain) |
| pgp.importPrivate | accountId, armored | (via Rust pgp_import_private) |
| pgp.exportPublic | accountId | chiave pubblica in armor |
| pgp.forget | accountId | cancella la copia in memoria del vault |
| pgp.pubImport | email, armored | salva/aggiorna chiave pubblica contatto |
| pgp.pubList | — | |
| pgp.pubDelete | email | |
| export.message | messageId, dest | scrive il raw come file .eml |
| export.mailbox | folderId, dest | archivio mboxrd dell'intera cartella |
| import.mails | accountId, src | importa .eml/.mbox nella cartella locale Importate (solo locale) |
| search.query | query (+ mode=semantic) | FTS5 con prefissi/frasi/snippet; mode=semantic usa gli embedding se disponibili |
| search.status | - | stato ricerca semantica: server AI locale attivo, modello, indicizzate/totali |
| search.reindex | limit | indicizza fino a limit messaggi senza vettore (chunked, newest first) |
| ai.status | — | stato del server AI locale (M12): available, url, kind ("ollama"\|"openai"), chatModel, embedModel, allowRemote, error? |
| ai.models | — | elenco modelli esposti dall'endpoint configurato (Ollama /api/tags, OpenAI-compatible /v1/models); funge anche da test di connessione |
| ai.config | url?, kind?, chatModel?, embedModel?, allowRemote? | salva la config in app_config (chiavi ai.*); campi vuoti = invariato; embedModel passa da semantic.SetModel (cambio modello = vettori azzerati); risponde con lo stato effettivo |
| ai.chat | messages[{role, content, images?}] | completion non-streaming con il modello chat configurato → {content}; timeout 120s (bridge Rust 150s); cap: testo 64 KB, immagini 6 MB (data URL delle pagine dei PDF scansionati); NON è queueable. I prompt sono costruiti dalla UI (i corpi sono già nel webview), il motore inoltra solo al server locale. Vision: Ollama riceve base64 nudo in `images[]`, i server OpenAI-compatible il content[] con image_url. Privacy: endpoint non-locale rifiutato senza allowRemote=true |
| backup | (comandi Rust backup_create / backup_restore) | copia file-level del DB cifrato col motore fermo: il VFS adiantum non supporta VACUUM INTO su percorsi arbitrari |
| rules.list | — | tutte le regole (M8), ordine di posizione |
| rules.save | regola: {id?, accountId?, name, enabled, position?, field, op, value, action, target?} | crea o aggiorna; campo=from\|to\|subject\|body\|spam_score\|has_attachment, op=contains\|equals\|starts_with\|ends_with\|at_least\|any, azione=move\|mark_read\|star\|mark_spam |
| rules.delete | id | |
| threads.list | folderId **or** accountId, type? | two views: per-folder ({folderId}) or account-level ({accountId: "all"|<id>, type? default "inbox"}) — the unified inbox joins every account's folder of that type, newest first, cap 200; each thread carries its own folder/account, latest-message sender (from) and messageIds |
| thread.get | threadId | |
| message.get | messageId | |
| message.markRead | messageId | local + server (\Seen) |
| thread.markRead | threadId | marks every message of the thread read: local flags + aggregate, one \Seen STORE per folder involved, folder badges recomputed, emits account.updated |
| thread.markUnread | threadId | clears \Seen on every message of the thread (STORE -FLAGS (\Seen)) and restores the local unread state + folder badges, emits account.updated |
| message.delete | messageId | local + server (\Deleted + expunge) |
| thread.archive | threadId | local move + server MOVE; source/archive badges recomputed, emits account.updated |
| search.query | query, folderId?, accountId?, before?, after? | FTS5 (MATCH on subject/sender/recipient/body) + filters applied via messages/threads joins; rank order preserved; dedup by thread |
| attachment.save | messageId, name, dest, secret | re-downloads the raw message on demand, extracts the part and writes it to `dest` (0600); runs through the dedicated `attachment_export` Rust command |
| attachment.text | messageId, name, secret | estrazione testo di un allegato PDF (M12) per l'AI locale: fetch raw → parte → pdftext (puro Go, niente OCR) → {name, text, chars, truncated, pages}; max 50 MB, cap 100k caratteri; PDF scansionati → testo vuoto (la UI passa al rendering), protetti da password → errore; runs through the dedicated `attachment_text` Rust command |
| attachment.images | messageId, name, secret, maxPages? | rendering pagine di un PDF per il percorso vision (M12): engine rasterizza con MuPDF (go-fitz) fino a 4 pagine, JPEG 150 DPI max 1400px → {name, pages[{index, mime, data(base64)}], totalPage, rendered}; per i PDF scansionati letti da modelli multimodali; runs through the dedicated `attachment_images` Rust command |
| sync.start | accountId, secret | validates credentials synchronously (dial + login), answers `{started:true}` immediately, then runs the UID-incremental sync in the background and starts the push watcher |
| send | accountId?, secret, message{to,cc,subject,body,htmlBody?,attachments,inReplyTo?,references?} | with `htmlBody` set the message is composed as multipart/alternative (text + html: replies keep the quoted message in its original HTML); SMTP send → APPEND into Sent (best effort) → local persistence with thread resolution → message.created + account.updated |
| secrets.restore | secrets[{accountId, secret}] | **engine-internal, Rust-only** (engine_invoke rejects it): re-delivers keychain credentials into the engine runtime vault after each engine spawn, so server round-trip ops work without a manual sync first; the webview never sees the values |
| outbox.list | — | pending offline-first operations: `{pending, items[{id, accountId, kind, payload, createdAt, attempts, lastError}]}` (payload = original request params, secrets never persisted) |
| outbox.cancel | id | removes one queued operation, emits outbox.updated |
| outbox.drain | — | nudges the worker for an immediate replay pass (UI calls it when connectivity returns); acks `{queued:true}`, results arrive as events |

Timeouts: 30 s default (sync.start included: its response is immediate),
60 s accounts.add, 90 s send (SMTP session cap is 60 s), 120 s ai.chat
(inferenza del modello locale). Max line 1 MB.

## AI locale (opzionale, M12)

The engine talks to ONE user-configured local AI server (app_config keys
`ai.url`, `ai.kind`, `ai.chat_model`, `ai.allow_remote`; legacy env fallback
`OLLAMA_URL`). Two dialects: `ollama` (native `/api/tags`, `/api/chat`,
`/api/embed`) and `openai` (OpenAI-compatible `/v1/models`,
`/v1/chat/completions`, `/v1/embeddings` — LM Studio, llama.cpp, vLLM).

Privacy guard: the client refuses any non-loopback endpoint unless the user
explicitly set `ai.allow_remote` (toggle in Impostazioni → AI locale). With
the server off every consumer degrades: semantic search falls back to
keyword search, the thread "Riassumi" action reports the error in a toast.

## Offline-first queue (outbox)

When a **queueable op** (`send`, `thread.markRead`, `thread.markUnread`,
`thread.archive`, `message.delete`) fails with a **transport-level error**
(dial refused, DNS, timeout, TLS handshake...), the engine parks the request
in `pending_ops` (migration 0007) with its original params and answers
`{queued:true}` instead of an error — the UI shows "in coda di invio".
Auth/validation errors surface normally: only the network is worth retrying.

The worker (started with the engine, 5 s tick) replays due items through the
same handlers — so replay behaves exactly like a live request. Failures get
exponential backoff (30 s doubling, 15 min cap); successes are removed.
Events:

- `outbox.updated {pending}` after every enqueue/replay/cancel
- `connection.state {online}` on **transitions only** (offline after a
  network failure, back online after a successful replay)

Secrets are never written to the queue: payloads reference the account and
the credential is fetched from the runtime vault at replay time.

## Push (IDLE) and fallback polling

After a successful `sync.start` the engine keeps a watcher per account:

- IMAP **IDLE** on INBOX: server-side `EXISTS` updates end IDLE, trigger one
  incremental pass (emitting `message.created` per new message) and re-enter
  IDLE. go-imap restarts IDLE automatically every ~28 min (heartbeat).
- Servers without IDLE (or after a session drop) fall back to a **5 minute
  poll** of every syncable folder.
- Push passes emit `message.created` only — no `sync.started/completed`
  cycling, so the UI indicator does not flap. The UI debounces refreshes
  (200 ms) to coalesce bulk deliveries.

Event flow: `sync.started|progress|completed|error`, `message.created` and
`account.updated` are forwarded to the UI; `message.updated/deleted` and
`thread.updated` stay internal (lists refresh on the debounced refresh).

## Rules

1. UI sends high-level ops (getThreads, search, sync.start, send).
2. Engine answers with domain objects (shared-types), never SQL/IMAP internals.
3. Credentials: only `account.id` + the runtime secret cross the Rust->engine
   pipe, injected by Tauri from the OS keychain; secrets are never persisted
   by the engine, never logged, never sent back to the webview. `send` and
   `sync.start` are blocked from the generic `engine_invoke`: they run through
   dedicated Rust commands (`account_send`, `account_sync`) that read the
   keychain.
4. Events are the only channel for background progress; no polling anywhere.
