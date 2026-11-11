# LocalMail — note per Claude

Client email desktop: Tauri 2 (Rust, `src-tauri/`) + frontend Astro/React
(`apps/client/`) + motore Go come sidecar (`backend/`, binario `mail-engine`).
Quality gate: `bash scripts/check-all.sh`.

## Rilasci (macOS + Windows con auto-update)

Tutto passa da `bun run release <comando>` ([scripts/release.ts](scripts/release.ts)).
Le build sono **locali**: la firma Windows usa un token USB, quindi niente CI.

### Come funziona l'updater

- All'avvio l'app ([apps/client/src/lib/updater.ts](apps/client/src/lib/updater.ts))
  scarica `https://github.com/Intycode/localmail/releases/latest/download/latest.json`,
  verifica la firma con la `pubkey` in `src-tauri/tauri.conf.json → plugins.updater`
  e, se l'utente accetta, installa e riavvia. Una volta per sessione, silenzioso se offline.
- `latest.json` è un asset della GitHub Release: contiene versione, URL e firma
  per `darwin-aarch64`, `darwin-x86_64` (stesso `.app.tar.gz` universal) e
  `windows-x86_64` (installer NSIS `-setup.exe`).
- "latest" = ultima release **pubblicata**, non draft e non prerelease. Finché la
  release è draft nessun utente la vede.
- Le installazioni fatte prima della prima versione con updater non si aggiornano
  da sole: vanno reinstallate a mano una volta.

### Versione

- Unica fonte di verità: `version` nel `package.json` di root (letta da
  `tauri.conf.json` e da Astro per `__APP_VERSION__`). `release bump` la allinea
  anche in `apps/client/package.json`, `src-tauri/Cargo.toml` e `Cargo.lock`.
- SemVer `x.y.z` senza suffissi. Ogni rilascio **deve** avere una versione
  maggiore della precedente: l'updater installa solo versioni più nuove.
- Non modificare la versione a mano: usa sempre `release bump`.
- Scelta del numero: patch = fix, minor = funzionalità, major = cambi
  incompatibili (es. formato DB senza migrazione).

### Setup una tantum

1. **Chiave di firma updater** (sul Mac):
   ```sh
   bunx tauri signer generate -w ~/.tauri/localmail.key
   ```
   Scegli una password. Copia il contenuto di `~/.tauri/localmail.key.pub` in
   `src-tauri/tauri.conf.json → plugins.updater.pubkey` (al posto di
   `REPLACE_WITH_UPDATER_PUBKEY`) e committa.
   - Copia `localmail.key` anche sulla macchina Windows, in `%USERPROFILE%\.tauri\`.
   - Fai un backup della chiave privata e della password (password manager).
     **Se la perdi, le app installate non potranno mai più aggiornarsi.**
   - Non cambiare mai la `pubkey` dopo il primo rilascio.
2. **Variabili d'ambiente** (in entrambe le macchine, es. nel profilo della shell):
   - `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` — obbligatoria.
   - `TAURI_SIGNING_PRIVATE_KEY` — facoltativa; di default si legge
     `~/.tauri/localmail.key`.
3. **macOS – firma e notarizzazione** (Apple Developer Program, vedi
   [docs/signing.md](docs/signing.md)): `APPLE_SIGNING_IDENTITY`
   (`Developer ID Application: …`), `APPLE_ID`, `APPLE_PASSWORD`
   (app-specific), `APPLE_TEAM_ID`. Senza, lo script avvisa e produce un'app non
   firmata che Gatekeeper blocca al primo avvio.
4. **Windows**: token USB inserito (thumbprint in `tauri.conf.json`), Windows SDK
   (signtool), Git Bash, Go, Rust, Bun.
5. `gh` autenticato con permessi di scrittura su `Intycode/localmail`, su
   entrambe le macchine.

### Procedura di rilascio

Dal branch `main`, working tree pulito, check verdi (`bash scripts/check-all.sh`).

1. **Mac** — bump, commit, tag annotato, push:
   ```sh
   bun run release bump 0.2.0
   ```
2. **Mac** — build universal (arm64+x64), crea la release **draft** con note
   generate, carica `.dmg`, `.app.tar.gz`, `.sig` e `latest.json`:
   ```sh
   bun run release build
   ```
3. **Windows** — allinea e builda (stesso comando, rileva l'OS):
   ```sh
   git pull --tags
   bun run release build
   ```
   Carica `-setup.exe` + `.sig` e unisce `windows-x86_64` in `latest.json`.
4. Controlla la draft su GitHub (note di rilascio, asset), poi:
   ```sh
   bun run release publish
   ```
   Rifiuta di pubblicare se `latest.json` non ha tutte e tre le piattaforme.

Da quel momento le app installate propongono l'aggiornamento al prossimo avvio.

### Regole per Claude

- Non eseguire `release bump`, `build` o `publish` senza richiesta esplicita
  dell'utente: fanno push e pubblicano verso gli utenti.
- Non leggere, stampare o committare la chiave privata o le password.
- Se una build fallisce a metà, rilancia `release build`: è idempotente
  (`--clobber` sugli asset, merge di `latest.json`). Non rifare il bump.
- Per ritirare una release difettosa: rilascia subito una patch più nuova.
  Non cancellare la release "latest" senza averne pubblicata un'altra, o
  l'endpoint dell'updater restituisce 404.
- Nomi degli artefatti attesi (se Tauri li cambia, aggiorna lo script):
  `target/universal-apple-darwin/release/bundle/macos/LocalMail.app.tar.gz`,
  `…/dmg/LocalMail_<v>_universal.dmg`,
  `target/release/bundle/nsis/LocalMail_<v>_x64-setup.exe`.
