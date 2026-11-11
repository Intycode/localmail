# Roadmap

## PHASE 1 — Foundation (DONE)

- Stack: Tauri 2 + Astro + TS + Bun + Go + SQLite(FTS5).
- Empty local store (no seeded accounts or emails) + provider stub; navigable app
  with zero accounts.
- SQLite schema + migrations + FTS5; Go engine skeleton; event bus.
- Tests (frontend + Go), lint, format, README, docs.

## PHASE 2 — Gmail (DONE)

- Gmail provider on the IMAP transport with XOAUTH2; OAuth2 (authorization
  code + PKCE, loopback redirect) via OS keychain (Tauri).
- Real sync over the engine pipeline; thread resolution by Message-ID.
- Search over FTS5, local-first rendering.

## PHASE 3 — Microsoft (v1 DONE, limits documented)

- Microsoft/Outlook provider over **Graph API** (`backend/internal/email/graph`),
  OAuth2 **device-code** flow (keychain-only refresh token, Rust bridge).
- UID mapping = `receivedDateTime` epoch; well-known folder names in
  `folders.imap_name` (no discovery). Documented v1 limits: no IDLE push
  (5 min poll), deletion reconciliation over the synced window, local-only
  drafts. See docs/oauth.md.

## PHASE 4 — Generic email (DONE)

- Generic IMAP provider (password / app-password via OS keychain): DONE.
- Folder mapping (SPECIAL-USE + heuristics, incl. \Junk), UID-incremental
  sync, deletion reconciliation, UIDVALIDITY handling: DONE.
- IPC stdio bridge Tauri <-> Go + events: DONE.
- SMTP send (TLS 465 / STARTTLS 587, threading headers, **attachments**): DONE.
- IDLE push: DONE — multi-folder watcher (INBOX + SPECIAL-USE, one IMAP
  session per folder) with 5 min poll fallback.
- JMAP provider (Fastmail & co.): DONE (EmailSubmission for sending).
- Drafts (local-first + best-effort APPEND): DONE.
- Undo-send / scheduled send (outbox `sendAt`), thread move / spam actions,
  snooze (local restore loop), compose autocomplete: DONE.

## PHASE 5 — Native experience (largely DONE)

- Offline outbox queue: DONE. Notifications (Settings → Notifiche): DONE.
- Attachment handling (read: save/open/download + inline image previews;
  send: multipart/mixed): DONE.
- Import/export (mbox, per-message EML): DONE. Automatic daily backup of
  the encrypted DB (Settings → Backup): DONE.
- Packaging and releases: DONE. Installers (macOS Apple Silicon `.dmg`,
  Windows NSIS `-setup.exe`) are published on GitHub Releases with signed
  auto-update (`latest.json`), built locally with `bun run release`
  (scripts/release.ts; no CI because the Windows signing token is a USB
  device). Intel Macs and Linux are not supported. See docs/signing.md.
- e2e: protocol-level end-to-end test of the real engine binary
  (backend/e2e) over stdio JSON-lines: DONE.

## PHASE 6 — Mobile

- iOS/Android Tauri shells; mobile keychain; adaptive responsive UI;
  background sync on mobile constraints.

## Non-goals (always)

No proprietary cloud, no user accounts, no analytics/tracking/ads, no
billing/subscriptions, no social login, no separate web version. Cloud AI
stays a non-goal: the optional AI layer talks only to the user's own local
server (Ollama / LM Studio), off by default.
