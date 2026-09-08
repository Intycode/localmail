# PLAN — Colmare le lacune del progetto Mail (repo: intymail)

> Piano operativo nato dall'analisi del 28/08 (workspace originale `~/Desktop/client-email`,
> progetto trasferito qui). **Task 1 è a carico del maintainer**; i Task 2–7 sono l'implementazione pianificata.
>
> **Stato (28/08, sessione di sviluppo):** Task 1–6 IMPLEMENTATI e testati;
> Task 7 parzialmente (`bundle.active: true`; firma e distribuzione fuori scope).
> Dettagli implementativi e scostamenti dal piano in fondo al file.

## Stato dell'arte verificato (non lacune)
- IPC stdio JSON-lines con allowlist e timeout (`backend/internal/ipc/server.go`)
- Bridge Rust con keychain e rollback account (`src-tauri/src/engine.rs`, `secrets.rs`)
- Provider IMAP reale: sync UID-incrementale, SPECIAL-USE, UIDVALIDITY, riconciliazione cancellazioni
- SQLite FTS5, thread resolution, sanitizzazione HTML con DOMPurify + CSP, keyboard nav, temi
- Gate qualità verdi: frontend 0 errori TS, 12 test pass, prettier pulito; `go test ./...` verde

## Lacune individuate (con evidenza)

| # | Lacuna | Evidenza |
|---|--------|----------|
| 1 | Nessun repository git *(risolta: repo creato qui)* | — |
| 2 | Nessuna CI | manca `.github/` |
| 3 | .gitignore incompleto | non ignora .bun-cache/.bun-tmp/.go-*/.cargo-*/backend/.go-home; `backend/mail.db` artefatto dev |
| 4 | Invio SMTP assente | `opSend` errore sempre; `smtp.Client` mock; `TauriMailService.sendMessage` lancia eccezione; nessuna config SMTP su Account |
| 5 | Risposta incompleta | reply precompila solo l'oggetto (`?reply=1&subject=`): niente destinatari, citazione, header threading |
| 6 | Bozze assenti | "Scarta" in ComposePage perde il testo; nessun op draft.* |
| 7 | Niente IDLE né sync periodica | sync solo all'avvio e dal bottone su AccountsPage |
| 8 | Allegati solo metadata | `attachment.Store` solo Create/ListByMessage; UI badge non cliccabili |
| 9 | File temporaneo dimenticato | `backend/internal/sync/perf_tmp_test.go` |
| 10 | Coverage parziale | niente test per account, message, attachment, smtp; hint `React.FormEvent` deprecato (AccountsPage:226); stderr motore scartato |
| 11 | Packaging disattivato | `bundle.active: false` (fase 5 roadmap, accettabile ora) |

---

## Task 1 — Igiene repository (P0) — **a carico del maintainer**
1. Estendere `.gitignore`: `.bun-cache/`, `.bun-tmp/`, `.go-cache/`, `.go-mod/`, `.go-path/`, `.go-home/`, `.cargo-home/`, `.cargo-target/`, `backend/.go-home/`, `backend/mail.db`, `backend/*.db`, `*.tsbuildinfo`
2. Rimuovere `backend/mail.db` (rigenerabile; il DB vero vive in app_data_dir)
3. CI GitHub Actions (`.github/workflows/ci.yml`): job bun 22 (check/test/lint) + Go (vet/test/build con GOPATH/GOCACHE nel repo come `engine:build`) + cargo (fmt --check, clippy)
4. `perf_tmp_test.go` → benchmark `perf_bench_test.go` (o rimozione se ridondante)

**Accettazione:** repo pulito, CI verde, nessun artefatto binario tracciato.

---

## Task 2 — Invio SMTP end-to-end (P1, ~3 giorni) — completa la fase 4

**Schema e tipi**
- Migrazione `0005_smtp.sql`: colonne `smtp_host`, `smtp_port`, `smtp_security`, `smtp_username` NULLABILI su `accounts`; fallback: `smtp.<imap.host>`:465/TLS con username IMAP
- `packages/shared-types`: `SmtpConfig`, `Account.smtp?`, `ImapAccountDraft.smtp?` opzionale

**Motore Go**
- `backend/internal/email/smtp/smtp.go`: client reale (net/smtp + TLS implicito/STARTTLS, min TLS 1.2, verifica certificati, auth PLAIN/LOGIN, timeout 15s); Message-ID/Date/From generati; MIME text/plain; header `In-Reply-To`/`References` se reply
- `provider.MailProvider`: aggiungere `Append(mailbox string, raw []byte, flags []string, date time.Time) error` (IMAP APPEND) implementato nel client IMAP; `ImapProvider.Send` delega al client SMTP
- `ipc/server.go`: implementare `opSend` (accountId opzionale, ComposeMessage): invio SMTP → APPEND in Sent → persistenza locale + thread resolution → evento `account.updated`

**Tauri e frontend**
- `engine.rs`: comando `account_send` che legge il secret dal keychain (come `account_sync`) e invia `send`; la password non tocca il webview; `send` vincolato lato Rust
- `tauriMailService.sendMessage` → `account_send`; `localMailService` invariato
- `AccountsPage`: sezione "Server in uscita (SMTP)" opzionale con default precompilati
- Reply completa: ThreadPage passa `?reply=<messageId>`; precompilare A/CC, citare il corpo ("> Da: … <data>"); il messaggio inviato appare nel thread esistente

**Test/Accettazione:** fake SMTP server (net/textproto); test IPC `opSend` con provider mock; test frontend parsing destinatari reply. Invio reale verso server di test (es. Mailpit), copia in Sent, thread aggiornato, `sqlite3 .dump | grep -i pass` vuoto.

## Task 3 — IDLE push con fallback periodico (P1, ~2 giorni)
- `imap/client.go`: IDLE (go-imap v2, heartbeat ~25 min, riconnessione su errore)
- `provider.MailProvider`: `Idle(mailbox string, notify chan<- struct{}) (stop func(), err error)`
- `sync/sync.go`: `sync.start` non bloccante (goroutine + risposta immediata; progressi via eventi — aggiornare docs/ipc.md). Dopo sync iniziale: IDLE su INBOX + SPECIAL-USE principali; evento → incremental sync → `message.created`
- Fallback: se il server non dichiara IDLE (CAPABILITY), poll ogni 5 min
- `syncService.ts`: gestire `message.created` con refresh debounced della cartella visibile
- Timeout Rust: `sync.start` a timeout breve
**Test/Accettazione:** server IMAP fake per IDLE; nuova mail visibile in <5s senza azione utente.

## Task 4 — Bozze (P2, ~1.5 giorni)
- Op `draft.save` (upsert locale in SQLite, APPEND IMAP su Drafts best-effort), `draft.delete`
- `ComposePage`: auto-save ogni 30s se dirty + "Salva bozza"; conferma su "Scarta" se dirty; sidebar mostra Bozze; `?draft=<id>` riapre in compose
**Accettazione:** bozza sopravvive al riavvio e si riapre compilata.

## Task 5 — Allegati in lettura (P2, ~1.5 giorni)
- Motore: fetch `BODY[part]`, scrittura in `app_data_dir/attachments/` (bypass cap 1MB IPC: si passa un path), op `attachment.save` → path; verificare part-id nei metadata
- Rust: comando `attachment_export` (salva-con-nome via tauri_plugin_dialog)
- `ThreadPage`: badge allegato → bottoni "Salva…"
**Accettazione:** allegato 10MB salvato integro (hash verificato in test Go).

## Task 6 — Pulizia e robustezza (P3, ~mezza giornata)
- Fix `React.FormEvent` deprecato (AccountsPage:226)
- Test Go per account store, message store, attachment store
- `engine.rs`: stderr motore → log file in `app_data_dir/logs/` (rotazione semplice) invece di `Stdio::null()`
- Esportare/testare i normalizzatori `normalizeThread/normalizeMessage`

## Task 7 — Packaging (P3, opzionale, fase 5)
- `bundle.active: true`, target per piattaforma, firma macOS; distribuzione senza auto-update (download manuale firmato)

---

## Ordine di esecuzione
Task 1 (maintainer, in parallelo) → 2 → 3 (P1; entrambi toccano ipc/server.go: in sequenza) → 4 → 5 → 6 → 7 (opzionale).

## Assunzioni e decisioni
- SMTP config esplicita ma opzionale, default derivati dall'IMAP
- `sync.start` asincrono (risposta immediata + eventi): trasparente alla UI attuale
- Bozze local-first; APPEND su Drafts best-effort
- Fuori scope ora: Gmail/Microsoft OAuth (fasi 2–3), allegati in invio, notifiche/offline queue (fase 5), mobile (fase 6)
- Nessuna nuova dipendenza (go-imap v2 già presente; net/smtp è stdlib)

---

## Esito implementazione (aggiornato a fine sessione)

- **Task 1** — `.gitignore` esteso (cache toolchain in-repo), `perf_tmp_test.go` sostituito da `perf_bench_test.go` (bench espliciti), CI 3 job in `.github/workflows/ci.yml` (bun/go/rust). Il `.db` dev locale non è tracciato.
- **Task 2** — Client SMTP reale (`backend/internal/email/smtp/`): implicit TLS 465 / STARTTLS 587 / plain opt-in (gated in accounts.add), AUTH PLAIN→LOGIN, QP body, RFC 2047, threading headers; `account_send` legge il keychain; APPEND in Sent (APPENDUID) + thread resolution; test end-to-end con fake SMTP (`send_test.go`).
- **Task 3** — IDLE su INBOX con supervisor (`sync/watch.go`): notify → sync incrementale (`message.created`) → re-IDLE; fallback poll 5 min su server senza IDLE; `sync.start` risponde subito dopo la validazione del dial (`provider.Ping`); timeout Rust di sync.start tornato al default; test push in tempo reale (`idle_test.go`, consegna in ~3 ms).
- **Task 4** — `draft.save`/`draft.delete` local-first con cartella Bozze auto-creata (`EnsureDraftsFolder`); l'id della bozza è stabile tra i salvataggi; auto-save 30 s + "Salva bozza" + conferma su "Scarta"; riapertura via `?draft=<id>` e pulsante "Modifica bozza" in ThreadPage; test IPC (`draft_test.go`) e frontend.
- **Task 5** — Metadati allegati da MIME walk in sync + persistenza rows (`UpsertBatch`/`Get`/aggregato `has_attach`); `attachment.save` riscarica il raw on demand (`provider.FetchRaw`), estrae la parte (`mime.ExtractAttachment`) e scrive sul path scelto dal save-dialog (`tauri-plugin-dialog`, permesso `dialog:allow-save`); badge cliccabili in ThreadPage; test end-to-end (`attachment_test.go`).
- **Task 6** — `SubmitEvent` al posto di `FormEvent`; test store per account e message; stderr del motore su `app_data_dir/logs/engine.log` con rotazione a 1 MB; normalizzatori `normalizeThread/normalizeMessage` esportati e testati.
- **Task 7** — `bundle.active: true`; firma macOS/distribuzione restano fuori scope come da piano.

**Scostamenti dal piano (documentati):**
1. IDLE solo su INBOX + poll 5 min per le altre cartelle (vincolo: una sessione IMAP per account; SPECIAL-USE multipli rimandati).
2. Allegati: estrazione dal raw riscaricato invece di `BODY[part]` con part-path — evita migration e BODYSTRUCTURE, costo: riscaricare il messaggio al salvataggio.
3. Le bozze senza mailbox Drafts sul server restano solo locali (best-effort come da piano).
4. I messaggi inviati entrano nel thread originale via In-Reply-To/References; la lista Sent mostra thread separati (quirk accettato).

**Gate finali (tutti verdi):** `go vet/test ./...` · `cargo fmt --check` + `clippy --all-targets` 0 warning · frontend `check` 0 errori/warning/hint · 14 test bun · prettier pulito · `engine:build` rigenerato.
