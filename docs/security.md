# Security

Come Mail protegge i dati. Principio guida: **il dispositivo dell'utente è il
confinamento**; l'unico traffico di rete è IMAP cifrato verso i server di
posta configurati dall'utente. Niente cloud, niente telemetry, niente AI.

## Anti-tracking (M6)

- **Pixel di tracciamento rimossi a monte**: all'ingesto il corpo HTML viene
  sanitizzato dal motore: le immagini di tracciamento (dimensione 0/1 px,
  nascoste via CSS, host noti) sono eliminate **dal corpo salvato**: non
  vengono mai caricate, nemmeno dopo il consenso, e restano fuori da citazioni
  e ricerche.
- **Immagini remote dietro consenso**: le immagini di contenuto restanti sono
  contate (campo remoteImages) e la UI non le carica finche l'utente non preme
  "Carica immagini" (scelta per messaggio, non persistita: il default resta
  privacy-friendly). Il mittente non vede aperture automatiche.
- **Link ripuliti**: i parametri di tracciamento (utm_*, fbclid, gclid, ...) sono
  tolti dagli URL sia a ingest sia al render; i parametri funzionali (es.
  token di disiscrizione) sono preservati.
- **Sanitizzazione a doppio strato**: il motore rimuove script e base href, e
  il webview renderizza solo una whitelist di tag (DOMPurify) senza attributi
  evento; i link cliccati aprono il browser di sistema solo se http/https.

## Spam e phishing locali (M7)

- **Valutazione on-device**: a ogni ingest il motore assegna un punteggio spam
  0..100 con euristiche locali (parole cliché pesate, forma dell'oggetto, TLD
  a rischio, newsletter solo-immagini, numero di link). Nessun contenuto
  lascia il dispositivo: niente AI remota, niente liste di terze parti.
- **Avvisi phishing espliciti**: link mascherati (testo diverso dalla
  destinazione), link a IP diretti, shortener, domini IDN (possibili
  homoglyph), impersonamento brand (paypal, poste, dhl, ...) con dominio
  esterno, richieste di credenziali, allegati eseguibili (.exe, .scr, .js,
  ...). I flag compaiono come banner rosso nel messaggio.
- **Nessuna azione automatica**: un punteggio alto non sposta e non cancella
  nulla; solo l'utente decide. Soglia spam 60/100, avviso sospetto 35/100.

## Ricerca semantica (M10)

- **Opzionale e locale**: gli embedding esistono solo se un Ollama locale e in esecuzione
  (default nomic-embed-text, URL sovrascrivibile con OLLAMA_URL). Nessun contenuto di mail
  lascia il dispositivo; senza Ollama la ricerca resta a parole chiave (FTS5).
- I vettori (float32 LE, un BLOB per messaggio) vivono nella **database cifrata** (M3) con
  modello e dimensione; similarita coseno calcolata in Go, nessuna estensione SQLite.
- Cambiare modello invalida tutti i vettori (modelli diversi non sono confrontabili):
  wipe automatico e nuovo conteggio di indicizzazione.

## Crittografia OpenPGP (M9)

- **Chiavi private mai nel webview**: generate dal motore sul dispositivo e
  custodite nel keychain di sistema (voci pgp/<account>). Il webview riceve
  solo la chiave pubblica e il fingerprint; il database non contiene mai
  materiale segreto.
- **Composizione**: toggle Firma (PGP/Inline clearsign) e Cifra (per ogni
  destinatario con chiave pubblica salvata, piu encrypt-to-self). La mail
  cifrata parte in solo testo: niente HTML in chiaro, allegati vietati.
- **Lettura**: il corpo cifrato resta cifrato nel DB; la decifratura avviene
  al momento della visualizzazione e la firma (clearsign) viene verificata
  contro chiavi private e pubbliche locali. Stati: valida / non valida /
  non verificata. Per multipart/signed la verifica e' differita.
- **Chiavi dei contatti**: pubbliche, in tabelle cifrate locali; servono per
  cifrare le mail in uscita e verificare le firme in arrivo.

## Database cifrata a riposo (M3)

- Il file SQLite locale è **cifrato a riposo** (VFS `adiantum`: Adiantum
  XChaCha12+AES, pagine da 4 KiB, key derivation per chiavi raw): il file non
  inizia più con `SQLite format 3` e non è leggibile senza la chiave.
- La **chiave a 256 bit** viene generata al primo avvio, custodita nella
  keychain di sistema (voce `db/master`, service `dev.mail.client`) e
  passata all'engine via variabile d'ambiente `MAIL_DB_KEY` allo spawn:
  la chiave non è mai scritta su disco accanto al database e non attraversa
  mai il webview.
- Senza chiave (perde la keychain, keychain negata) l'engine **non parte**:
  fallisce esplicitamente invece di creare un secondo database in chiaro.
  Se il file del database cifrato esiste ma la voce `db/master` manca (nuovo
  computer, keychain azzerata), l'app non genera una chiave nuova: mostra
  "chiave del database non trovata nel portachiavi" e chiede di importare la
  chiave di recupero.
- **Chiave di recupero** (Impostazioni → Backup): "Esporta" scrive la chiave
  (64 caratteri esadecimali) in un file scelto dall'utente; "Importa" la legge
  da un file, la valida, conserva la chiave precedente nel keychain come
  `db/master.prev` e ferma il motore (riparte con la nuova chiave alla
  richiesta successiva). Entrambe le operazioni avvengono in Rust: la chiave
  non attraversa mai il webview.
  **Limite**: il file è in **chiaro**. Chi ha il file e un backup del database
  può leggere la posta, quindi va tenuto separato dal backup (password
  manager, non nella stessa cartella/cloud). Cifrarlo con una passphrase
  richiede una KDF (nuova dipendenza): da fare se il file dovrà viaggiare
  insieme al backup.
- Un database pre-esistente **in chiaro** (pre-M3) viene archiviato come
  `mail.db.plain.bak` e ricreato cifrato: l'upgrade non rompe l'app e non
  lascia il vecchio file attivo. Cancellare il `.plain.bak` dopo
  l'aggiornamento per eliminare i residui in chiaro.
- Modello: è cifratura a riposo (protegge file rubati/backup), non contro
  un attaccante con il dispositivo acceso e la keychain sbloccata — stesso
  perimetro dei segreti account.

## Segreti (password / app-password)

- Vivono **solo nell'OS keychain** (macOS Keychain, Windows Credential
  Manager, Linux Secret Service), scritte da Tauri via crate `keyring`
  (service `dev.mail.client`, voce `account/<id>`).
- Il webview non riceve mai i segreti: la password digitata nel form vive nel
  campo input fino alla submit, poi passa al comando Rust e viene dimenticata.
- Il motore Go riceve i segreti **solo in memoria** (RuntimeSecrets) al
  momento di connettersi; dopo un riavvio del processo Tauri li re-inietta
  dalla keychain. Il tipo `auth.Secret` redact se stesso in String/JSON:
  un log o una risposta IPC non possono contenerlo per costruzione.
- **SQLite non contiene colonne password.** Verifica: `sqlite3 mail.db .dump | grep -i pass`
  non produce risultati.

## Token OAuth (Gmail, M4)

- Il **refresh token** vive nella keychain (voce `account/<id>`, come le
  password); l'**access token** (~1 h) non è mai scritto su disco in chiaro:
  una cache vive in keychain (`oauth/<id>`) e viene rinnovata dal processo
  Rust prima di ogni uso.
- Il flusso usa **PKCE S-256** + `state` con redirect su **loopback**
  (`127.0.0.1`): il codice di autorizzazione non attraversa il webview.
- Le credenziali OAuth client non sono incluse nel binario: l'utente usa le
  proprie (docs/oauth.md).
- Autenticazione di rete: **XOAUTH2** su TLS (mai token in URL o log; il
  tipo `auth.Secret` redact i token come le password).

## Trasporto IMAP

- TLS implicito (993) o STARTTLS, certificati verificati contro i root di
  sistema (`InsecureSkipVerify` non è mai impostato), minimo TLS 1.2, SNI.
- Connessioni in chiaro: rifiutate dal motore a meno che l'utente non abbia
  scelto esplicitamente "Non cifrata" nella UI (opt-in con warning).
- Il comando IMAP `ID` non viene inviato: non si informazioni sul client al
  server. Le fetch usano `BODY.PEEK` (mai \Seen implicito).

## IPC Tauri <-> Go

- Il canale è **stdio JSON-lines** del processo figlio: nessuna porta locale,
  nessun socket: un attaccante locale non può connettersi.
- Allowlist statica di operazioni a due livelli (Rust e Go); op sconosciuta →
  errore, mai panic.
- Timeout per operazione e cap sulla dimensione delle righe (1 MB).

## Contenuto delle email (hostile)

- CSP Tauri: `default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'`
  — script e risorse remote bloccati a monte dal webview.
- Rendering **testuale di default**; l'HTML viene sanificato (DOMPurify) e
  mostrato solo in assenza di versione testuale, con banner "Contenuti
  esterni bloccati".
- Link aperti solo con schema http/https.

## Server ostile / risposte anomale

- Timeout di connessione (15 s) e I/O; cap sulla dimensione dei messaggi;
  parsing MIME con limiti di annidamento e lettura limitata delle parti.
- Un server che risponde troppo o in modo malformato non può bloccare il
  processo né riempire la memoria.

## Rimozione dati

- Eliminare un account fa purge completo locale (messages, threads, folders,
  FTS via trigger) + checkpoint WAL + voce keychain cancellata. Schema con
  `ON DELETE CASCADE` per percorsi di purge FK-safe.

## Supply chain

- Dipendenze minime: `emersion/go-imap/v2`, `emersion/go-message`,
  `emersion/go-sasl`, `golang.org/x/text` (tutte pure-Go, MIT), crate
  `keyring`. Versioni pinnate in go.sum / Cargo.lock.
- `scripts/security-check` esegue `govulncheck ./...` (Go) e
  `cargo audit` (Rust) quando installati.

## Cosa NON protegge Mail (modello di minacce esplicito)

- Un keychain sbloccato è accessibile ai processi dello stesso utente: è il
  modello di sicurezza dell'OS. Chi può leggere la keychain può decifrare la
  database (è la minaccia che la cifratura a riposo può attenuare solo in
  parte: file copiati senza accesso alla keychain restano illeggibili).
