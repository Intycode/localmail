# Architecture

## Principles

1. **Tauri is a thin native layer.** No Rust email logic. Window + webview +
   keychain bridge only.
2. **Local-first.** SQLite is the source of truth; the UI renders from it
   immediately and updates via events after background sync.
3. **Minimal stack, minimal deps.** Astro + pure TypeScript + Go + SQLite + Bun.
   No React, no Svelte, no state framework, no Next.js, no Electron.
4. **Simplicity is absolute.** A small app stays a small architecture.

## Data flow

```
SQLite ──render──▶ Astro UI ──sync──▶ Go engine ──provider──▶ network
   ▲                                                            │
   └──────────── events (message.created …) ◀──────────────────┘
```

## Layer responsibilities

| Layer | Owns |
|-------|------|
| Astro UI | rendering, keyboard nav, stores, compose, search UI |
| Tauri | window, webview, OS keychain bridge, process management |
| Go engine | sync pipeline, thread resolution, IMAP/SMTP/MIME, FTS5 search |
| SQLite | persistent local state: accounts, threads, messages, search index |

## Event names (canonical)

`sync.started`, `sync.progress`, `sync.completed`, `sync.error`,
`message.created`, `message.updated`, `message.deleted`, `thread.updated`,
`account.updated`.
