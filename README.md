<div align="center">

# Mail

**A minimal, fast, local-first email client that keeps your mail on your device.**

No cloud backend, no sign-up, no telemetry, no ads.
Optional AI features run only on a local model server that you run yourself.

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Platforms](https://img.shields.io/badge/platform-macOS%20%7C%20Windows%20%7C%20Linux-lightgrey)
![Status](https://img.shields.io/badge/status-early%20(v0.1)-orange)

</div>

---

Mail is a desktop email client built with Tauri, Astro and a Go engine. Your
messages live in an **encrypted SQLite database on your machine**. The interface
reads from that database right away, and a background sync keeps it up to date.
Passwords and keys go in the operating system keychain and nowhere else.

> **Status:** early software (v0.1). It is used every day by its author, but
> expect rough edges. There are no prebuilt binaries yet:
> [build it from source](#build-from-source).

## Contents

- [Features](#features)
- [Build from source](#build-from-source)
- [Adding accounts](#adding-accounts)
- [Architecture](#architecture)
- [Development](#development)
- [Documentation](#documentation)
- [Roadmap](#roadmap)
- [Privacy](#privacy)
- [Contributing](#contributing)
- [Security](#security)
- [License](#license)

## Features

**Accounts and sync**

- Generic **IMAP/SMTP** with UID-incremental sync, SPECIAL-USE folder mapping
  and multi-folder **IDLE push**
- **Gmail** via OAuth2 (PKCE with a loopback redirect, XOAUTH2)
- **Microsoft / Outlook** via Microsoft Graph (OAuth2 device-code flow)
- **JMAP** (Fastmail, Stalwart) with native push
- Provider presets in the account setup, and an onboarding wizard on first launch

**Reading and organizing**

- Conversations grouped into threads by Message-ID and References
- Instant full-text search (SQLite FTS5)
- Rules (sender, recipient, subject, body, attachments, spam score), snooze, move, archive
- Local spam and phishing heuristics, with no message sent to any external service
- Attachment preview, download and export
- Desktop notifications

**Writing**

- Drafts, undo send and scheduled send through an offline-capable outbox
- Attachments and recipient autocomplete
- **OpenPGP** (PGP/Inline): generate or import keys, encrypt and sign the message
  text, decrypt and verify incoming mail

**Privacy and data ownership**

- Database **encrypted at rest**; the key is kept in the OS keychain
- Remote content blocked by default: HTML is sanitized (DOMPurify + CSP), so
  tracking pixels and remote images never load
- **Import/export** of `mbox` and `eml` files
- **Encrypted database backup and restore**, with an optional daily automatic backup

**Optional local AI** (off by default)

- Connect your own **Ollama** or any **OpenAI-compatible** server (LM Studio,
  llama.cpp…)
- Thread summaries, translations and semantic search
- Only loopback endpoints are allowed unless you explicitly allow a remote one.
  When the AI server is off, nothing changes.

**Interface**

- Keyboard-first: `j`/`k` to move, `r` to compose, `Esc` to go back
- System, light and dark themes, with accent colors
- English and Italian

## Build from source

### Prerequisites

- [Bun](https://bun.sh) ≥ 1.1 and Node.js ≥ 22.12
- [Go](https://go.dev) ≥ 1.27
- [Rust](https://rustup.rs) (stable) and the
  [Tauri 2 system prerequisites](https://v2.tauri.app/start/prerequisites/) for your OS

### Run in development

```sh
bun install
bun run engine:build   # compile the Go engine into src-tauri/binaries/
bun run tauri:dev      # build the UI and open the app window
```

### Build a native bundle

```sh
bun run engine:build
bun run build
bunx tauri build --config src-tauri/tauri.conf.json
```

Bundles (`.app`/`.dmg`, `.msi`, `.AppImage`/`.deb`) end up in
`src-tauri/target/release/bundle/`. They are unsigned. On macOS, the first time
you open the app, right-click it and choose **Open**. To build signed bundles,
see [docs/signing.md](docs/signing.md).

## Adding accounts

| Provider               | What you need                                                                                          |
| ---------------------- | ------------------------------------------------------------------------------------------------------ |
| IMAP/SMTP              | Server settings (or a preset) and your password or app password                                        |
| JMAP                   | Session URL and an API token, see [docs/jmap.md](docs/jmap.md)                                         |
| Gmail                  | Your own Google OAuth client in `oauth.json`, see [docs/oauth.md](docs/oauth.md)                       |
| Microsoft / Outlook    | Your own Azure app registration (client ID) in `oauth.json`, see [docs/oauth.md](docs/oauth.md)        |

Mail does not ship any OAuth credentials. For Gmail and Microsoft you register
your own OAuth client once, so no third party is involved.

## Architecture

```
┌───────────────────────────────┐
│  UI: Astro + React islands    │  shadcn/ui, keyboard nav, observable stores
│  apps/client                  │  renders straight from local data
└──────────────┬────────────────┘
               │ Tauri IPC
┌──────────────▼────────────────┐
│  Tauri 2 shell  (src-tauri)   │  window, OS keychain, file dialogs, OAuth
│                               │  no email business logic
└──────────────┬────────────────┘
               │ JSON-lines over stdio (sidecar process)
┌──────────────▼────────────────┐
│  Go engine  (backend)         │  sync, IMAP/SMTP/Graph/JMAP, MIME, threads,
│                               │  search, rules, PGP, local AI bridge
└──────────────┬────────────────┘
               │
┌──────────────▼────────────────┐
│  SQLite + FTS5 (encrypted)    │  the single source of truth
└───────────────────────────────┘
```

1. The UI renders from SQLite right away.
2. Background sync: provider → engine → SQLite.
3. The engine sends events to the UI stores. The UI never polls.

| Layer   | Choice                            | Why                                             |
| ------- | --------------------------------- | ----------------------------------------------- |
| Native  | Tauri 2 (Rust)                    | small binaries, thin shell, keychain access     |
| UI      | Astro 7 + React 19 + shadcn/ui    | static output, one island per page, Tailwind 4  |
| Engine  | Go (pure Go, no CGO)              | one compiled binary that cross-compiles easily  |
| Storage | SQLite + FTS5                     | local source of truth and full-text search      |
| Tooling | Bun + TypeScript (strict)         | fast installs, scripts and tests                |

More detail: [docs/architecture.md](docs/architecture.md) and the IPC contract in
[docs/ipc.md](docs/ipc.md).

### Repository layout

```
apps/client/            Astro UI: pages, React components, services, stores, i18n
packages/shared-types/  TypeScript types shared with the engine
backend/                Go engine: cmd/mail (entrypoint), internal/* (sync, email,
                        thread, search, rule, pgp, localai, …), migrations/
src-tauri/              Tauri shell: engine bridge, keychain, OAuth, PGP, paths
scripts/                build, quality-gate and security scripts
docs/                   architecture, IPC, security, OAuth, JMAP, signing, roadmap
```

## Development

```sh
bun run dev                  # UI only in the browser (no engine, no real data)
bun run check                # strict TypeScript + astro check
bun run test                 # frontend unit tests (bun test)
bun run lint                 # prettier --check
bun run format               # prettier --write

bun run backend:test         # go test ./...
bun run backend:vet          # go vet ./...

bash scripts/check-all.sh    # every quality gate, frontend and backend
bash scripts/security-check  # govulncheck + cargo audit (when installed)
```

## Documentation

| Document                                   | Topic                                                    |
| ------------------------------------------ | -------------------------------------------------------- |
| [architecture.md](docs/architecture.md)    | Principles, data flow, layer responsibilities, events    |
| [ipc.md](docs/ipc.md)                      | Engine operations and the IPC protocol                   |
| [security.md](docs/security.md)            | Threat model, encryption, secrets, hostile content       |
| [oauth.md](docs/oauth.md)                  | Gmail and Microsoft OAuth setup                          |
| [jmap.md](docs/jmap.md)                    | JMAP accounts                                            |
| [signing.md](docs/signing.md)              | Code signing and distribution                            |
| [roadmap.md](docs/roadmap.md)              | Phases and status                                        |

Some documents are written in Italian.

## Roadmap

| Phase                | Status                                                                    |
| -------------------- | ------------------------------------------------------------------------- |
| 1. Foundation        | ✅ stack, encrypted local store, FTS5, engine, IPC, tests                  |
| 2. Gmail             | ✅ OAuth2 + XOAUTH2 over IMAP                                              |
| 3. Microsoft         | ✅ v1 over Graph (polling, limits in [oauth.md](docs/oauth.md))            |
| 4. Generic email     | ✅ IMAP/SMTP, IDLE, JMAP, drafts, undo send, snooze, rules                 |
| 5. Native experience | ✅ notifications, offline outbox, attachments, packaging                   |
| 6. Mobile            | ⏳ planned: iOS/Android Tauri shells, adaptive UI                          |

## Privacy

- Mail connects only to your mail servers, over **verified TLS**. Plaintext
  connections need an explicit opt-in.
- The only other connections are:
  - the OAuth endpoints of Google or Microsoft, when you use those accounts;
  - your local AI server, if you turn it on.
- There is no telemetry, no analytics and no update check.
- Passwords, OAuth tokens, PGP private keys and the database key are kept only
  in the **OS keychain**.
- Removing an account deletes its mail and its secrets.

The full threat model, including what Mail does **not** protect against, is in
[docs/security.md](docs/security.md).

## Contributing

Contributions are welcome. Before you start:

1. For anything larger than a small fix, open an issue first.
2. Branch from `main` and keep each PR to one change.
3. Run `bash scripts/check-all.sh` before you open the PR.
4. Keep the architecture simple:
   - no new frameworks or state libraries;
   - no cloud dependencies;
   - no email logic in the Tauri layer.

## Security

Please **do not open a public issue** for a vulnerability. Report it privately
through [GitHub Security Advisories](https://github.com/Intycode/localmail/security/advisories/new).

## License

[MIT](LICENSE) © 2026 Intycode Sagl
