<div align="center">

# Mail

**A super-minimal, fast, local-first, privacy-first email client.**

Native desktop (macOS / Windows / Linux) and mobile (iOS / Android).
No cloud, no accounts, no tracking, no ads, no billing.
Optional AI runs **only** on a local server you own — off by default.

> One email client. Your mail stays on your device.

[![CI](https://github.com/Intycode-Sagl/intymail/actions/workflows/ci.yml/badge.svg)](https://github.com/Intycode-Sagl/intymail/actions/workflows/ci.yml)
[![GitHub Release](https://img.shields.io/github/v/release/Intycode-Sagl/intymail?include_prereleases)](https://github.com/Intycode-Sagl/intymail/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Platforms](https://img.shields.io/badge/platform-macOS%20%7C%20Windows%20%7C%20Linux%20%7C%20iOS%20%7C%20Android-lightgrey)](#download)

</div>

---

## Table of contents

- [Features](#features)
- [Download](#download)
- [What Mail is — and is not](#what-mail-is--and-is-not)
- [Architecture](#architecture)
- [Stack](#stack)
- [Repository structure](#repository-structure)
- [Development](#development)
- [Testing](#testing)
- [Build & release](#build--release)
- [Roadmap](#roadmap)
- [Privacy](#privacy)
- [Security](#security)
- [Contributing](#contributing)
- [Non-goals](#non-goals)
- [License](#license)

---

## Features

- **Local-first** — the UI renders from your local SQLite database instantly;
  a background sync keeps it up to date. The UI never waits on the network for
  data it already has, and never polls.
- **Real IMAP + SMTP** — generic IMAP provider (go-imap v2): UID-incremental
  sync, `BODY.PEEK`, SPECIAL-USE folder mapping, deletion reconciliation,
  UIDVALIDITY handling. SMTP send with attachments, drafts, undo-send, snooze,
  rules.
- **Thread resolution** — conversations stitched by Message-ID / References
  with a subject fallback, persisted locally.
- **Full-text search** — SQLite FTS5 over subject/sender/recipient/body, plus
  optional **semantic search** (local embeddings).
- **Attachments** — preview, download, export; PDFs open with the OS default
  app.
- **Optional local AI** — connect your own **Ollama** or any
  **OpenAI-compatible** server (LM Studio, llama.cpp…). Powers on-device
  thread **summaries** with **translations**, and semantic search. Text-layer
  PDFs are extracted engine-side (pure Go); scanned PDFs are not read.
  Non-loopback endpoints are refused unless explicitly allowed; with the
  server off, the app works exactly as before.
- **Privacy by construction** — passwords live in the **OS keychain** and
  never touch SQLite, logs, or the webview. HTML is sanitized (DOMPurify +
  CSP), remote content is blocked, tracking pixels never load.
- **Keyboard-first & responsive** — `j/k` navigation, `r` to compose, `Esc`,
  on-screen hints. System/Light/Dark theme with no flash of wrong theme.
- **Zero-config start** — works with no accounts configured; no seeded data,
  no invented threads.

---

## Download

Prebuilt bundles (dmg, msi, AppImage, deb) are attached to the
[GitHub Releases](https://github.com/Intycode-Sagl/intymail/releases) page.
Binaries are currently **unsigned**:

- **macOS** — Gatekeeper blocks the first launch. Unblock with:
  `xattr -cr /Applications/Mail.app` (or right-click → Open).
- **Windows** — SmartScreen: click "More info" → "Run anyway".
- **Linux** — `chmod +x Mail.AppImage && ./Mail.AppImage`, or install the deb.

> Builds are manual downloads: no auto-update, no proprietary channels.

---

## What Mail is — and is not

**It is:** a real application UI — keyboard-friendly, responsive, with a
System/Light/Dark theme switchable from Settings — built on a deliberately
small stack.

**It is NOT:** a marketing site, an Electron app, a Next.js SPA, a
cloud-hosted product, or something that phones home. There are no proprietary
cloud services, no analytics, no telemetry, no ads, no cloud AI, no billing,
no subscriptions, no social login, and no separate web version.

---

## Architecture

```
┌───────────────────────────────┐
│  Astro UI + React islands     │  keyboard nav, stores, services
│  (apps/client, shadcn/ui)     │  renders instantly from local SQLite
└──────────────┬────────────────┘
               │ IPC (structured events, no UI→engine coupling)
┌──────────────▼────────────────┐
│  Tauri 2  (src-tauri)         │  THIN native layer:
│                               │  window + webview + keychain bridge
│                               │  NO email business logic here
└──────────────┬────────────────┘
               │ spawn / bridge
┌──────────────▼────────────────┐
│  Go engine  (backend/)        │  sync engine, thread resolution,
│                               │  IMAP/SMTP/MIME, FTS5 search
└──────────────┬────────────────┘
               │ SQLite = local source of truth
┌──────────────▼────────────────┐
│  SQLite (FTS5)                │  accounts, threads, messages, search
└───────────────────────────────┘
```

**Local-first flow:**

1. Render from local SQLite immediately.
2. Background sync → provider → engine → SQLite.
3. SQLite update → events → frontend stores.
4. No polling in the UI.

Long-running operations (AI inference, IMAP/SMTP round-trips) run off the
main thread: the UI stays responsive while the engine works.

---

## Stack

| Layer      | Choice         | Why                                                       |
| ---------- | -------------- | --------------------------------------------------------- |
| Native     | **Tauri 2**    | ~MB binaries, Rust thin shell, OS keychain access         |
| UI         | **Astro 7**    | static output, islands architecture, tiny bundle          |
| Components | **shadcn/ui**  | owned Radix primitives + Tailwind 4 tokens, React islands |
| Language   | **TypeScript** | strict mode, shared types contract                        |
| Runtime    | **Bun**        | fast installs / scripts / tests                           |
| Engine     | **Go**         | compiled mail engine: sync, IMAP, SMTP, FTS5 search       |
| Storage    | **SQLite**     | local source of truth, FTS5 full-text search              |

Astro owns routing and static output; interactivity lives in one React island
per page, styled with shadcn/ui on Tailwind 4. No Svelte, no Next.js, no
Electron. Architectural simplicity is a hard priority.

---

## Repository structure

```
intymail/
├── package.json            # bun workspace root + scripts
├── bunfig.toml             # install cache inside workspace
├── apps/client/            # Astro UI + React islands (shadcn/ui)
│   ├── astro.config.ts     # static output, React integration, "@" alias
│   ├── components.json     # shadcn/ui config (radix-nova preset, "@/…" aliases)
│   └── src/
│       ├── pages/          # thin Astro shells: /, /thread/[id], /compose, /search, /settings, /accounts
│       ├── components/
│       │   ├── ui/         # shadcn/ui primitives (button, sidebar, dialog, …)
│       │   └── app/        # React islands: AppShell, AppSidebar + page components
│       ├── services/       # MailService, Tauri bridge, local store, search, sync, registry
│       ├── stores/         # observable stores (no Redux)
│       ├── lib/            # i18n, format, prompt builders, theme, settings, utils (cn)
│       └── styles/         # global.css — Tailwind 4 + shadcn tokens, class-based dark
├── packages/shared-types/  # single source of truth: TS types
├── backend/                # Go mail engine
│   ├── cmd/mail/           # engine entrypoint (--serve runs the IPC server)
│   ├── internal/
│   │   ├── domain/         # Go structs mirroring shared-types
│   │   ├── account/        # accounts + folders
│   │   ├── auth/           # SecureStorage (OS keychain via Tauri)
│   │   ├── database/       # SQLite open + migrations
│   │   ├── email/          # imap/ (transport), smtp/ (send), mime/ (parser)
│   │   ├── sync/           # Sync Engine pipeline
│   │   ├── thread/         # thread resolution + persistence
│   │   ├── search/         # FTS5 search
│   │   ├── semantic/       # embeddings for semantic search (local AI)
│   │   ├── attachment/     # attachment metadata
│   │   ├── pdfdoc/         # PDF text extraction
│   │   ├── events/         # event bus (sync.*, message.*, thread.*)
│   │   ├── ipc/            # JSON-lines server: op allowlist, per-op timeouts
│   │   ├── localai/        # local AI bridge (Ollama / OpenAI-compatible)
│   │   ├── queue/          # outbound operation queue
│   │   ├── rule/           # user rules engine
│   │   ├── assess/         # spam/phishing heuristics
│   │   ├── pgp/            # OpenPGP message handling
│   │   ├── provider/       # MailProvider contract: real IMAP + Mock
│   │   └── message/, mbox/, queue/, rule/, assess/, …
│   └── migrations/         # SQL schema, FTS5, indexes (go:embed)
├── src-tauri/              # Tauri 2 thin native layer
│   ├── src/                # engine.rs (IPC bridge), secrets.rs (keychain), pgp.rs, oauth.rs
│   └── capabilities/
├── scripts/                # project scripts (security-check, …)
├── docs/                   # architecture.md, ipc.md, security.md, roadmap.md, signing.md
└── README.md
```

---

## Development

### Prerequisites

- [Bun](https://bun.sh) ≥ 1.4
- [Go](https://go.dev) ≥ 1.24
- [Rust](https://rustup.rs) (stable) — Tauri 2 requirements per platform

### Commands

```sh
bun install           # install JS workspace (cache inside workspace)
bun run engine:build  # build the Go engine binary (src-tauri/binaries/mail-engine)
bun run tauri:dev     # build UI then launch the Tauri window

bun run dev           # Astro dev server (browser-only, without the engine)
bun run build         # static build (apps/client/dist)
bun run test          # bun test (unit tests)
bun run check         # tsc --noEmit + astro check (strict TS)
bun run lint          # prettier --check
bun run format        # prettier --write

bun run backend:test  # go test ./...
bun run backend:vet   # go vet ./...
bun run backend:build # go build ./...
bun run backend:run   # go run ./cmd/mail (smoke mode, no IPC)

bash scripts/security-check  # govulncheck + cargo audit (when installed)
```

Quality gates: strict TypeScript, prettier, `go fmt`, `go vet`,
`.editorconfig`, minimal dependencies.

---

## Testing

- **Frontend:** strict TS (`tsc --noEmit` + `astro check`) and unit tests of
  the core services, stores, and prompt builders (`bun test`).
- **Backend:** Go tests for database, thread, sync, mock provider, and FTS5
  search (`go test ./...`), plus `go vet ./...`.
- **CI:** GitHub Actions runs the frontend and backend gates on every push
  (`.github/workflows/ci.yml`).

---

## Build & release

Tauri bundles the Astro static build into a native window together with the Go
engine (sidecar, `bundle.externalBin`).

### Local build

```sh
bun run engine:build   # build the engine (also the sidecar with target triples)
bun run tauri:build    # frontend + native bundle (dmg/msi/AppImage)
```

Artifacts land in `src-tauri/target/release/bundle/`.

### Cutting a release

1. Make sure the tests are green (CI runs on every push).
2. `git tag vX.Y.Z && git push origin vX.Y.Z` — the workflow
   (`.github/workflows/release.yml`) builds bundles for macOS (ARM + Intel),
   Windows and Linux, and creates a **draft GitHub Release** with the
   artifacts and SHA-256 checksums.
3. Review the draft, publish. Optional code signing is documented in
   [docs/signing.md](docs/signing.md) (requires Apple Developer Program /
   Windows certificate).

---

## Roadmap

|                    Phase | Scope                                                                                              |
| -----------------------: | -------------------------------------------------------------------------------------------------- |
|        **1. Foundation** | this repo: stack, empty local store, SQLite/FTS5, Go engine, IPC shape, tests                      |
|             **2. Gmail** | Gmail provider, OAuth (OS keychain), real sync/search                                              |
|         **3. Microsoft** | Microsoft/Outlook provider (Graph), OAuth device-code                                              |
|     **4. Generic email** | IMAP receive ✅; SMTP send + attachments, IDLE multi-folder, drafts, undo-send/snooze/rules: ✅    |
| **5. Native experience** | notifications, offline queue, attachments, packaging ✅; optional macOS signing in docs/signing.md |
|            **6. Mobile** | iOS/Android Tauri shells, mobile keychain, adaptive UI                                             |

See [docs/roadmap.md](docs/roadmap.md) for details.

---

## Privacy

- Mail talks only to your IMAP/SMTP server, over **verified TLS** (plaintext
  is an explicit opt-in). No telemetry, no analytics, no cloud, no cloud AI.
- The optional AI features talk **only** to the local server the user
  configured (Ollama / LM Studio — loopback by default; a remote host needs an
  explicit consent toggle). Nothing is sent anywhere when the server is off.
- Passwords live only in the **OS keychain**; emails live only in your local
  **SQLite** database; removing an account purges both.
- Email content is rendered as plain text by default; HTML is sanitized and
  remote content is blocked (CSP) — no tracking pixels, no remote images.

Details and the full threat model: **[docs/security.md](docs/security.md)**.

---

## Security

Found a vulnerability? Please **do not open a public issue**. Use
[GitHub's private security advisory](https://github.com/Intycode-Sagl/intymail/security/advisories/new)
to report it, and we will respond as quickly as we can.

---

## Contributing

Contributions are welcome!

1. Fork the repository and create your branch from `main`.
2. Run the quality gates before opening a PR:
   `bun run check && bun run test && bun run lint` and `go test ./...`.
3. Keep PRs focused: one feature or fix per PR, with a clear description.
4. Architectural consistency matters here — no new frameworks, no new state
   libraries, no cloud dependencies. When in doubt, open an issue first.

---

## Non-goals

No proprietary cloud, no user accounts, no analytics, no tracking, no ads, no
cloud AI (AI runs only on the user's local server, opt-in), no
billing/subscriptions, no full Gmail/Outlook integration (we integrate via
provider APIs), no social login, no separate web version.

---

## License

[MIT](LICENSE) © Intycode Sagl
