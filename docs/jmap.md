# JMAP (Fastmail / Stalwart) — M5

Oltre a IMAP, Mail parla **JMAP** (RFC 8620/8621): JSON su HTTPS, push nativo
via EventSource, invio tramite *EmailSubmission*.

## Aggiungere un account JMAP

Account → Aggiungi → attiva **JMAP** nel selettore a destra di "configurazione
manuale":

| Campo | Valore |
|---|---|
| URL di sessione | `https://api.fastmail.com/jmap/session` (Fastmail) o `https://<server>/jmap/session` (Stalwart) |
| Username | l'indirizzo email |
| Token | API token / app password (mai la password principale, quando il server lo consente) |

L'invio non ha SMTP separato: passa per `EmailSubmission/set` del server
stesso.

## Come il provider adatta JMAP al motore di sync

- **UID stabili**: ogni Email id riceve un uid numerico persistente
  (tabella `jmap_map`, migration 0009); le email che si spostano tra
  cartelle non vengono riscaricate (lookup account-wide dell'id).
- **Corpi**: scaricati come blob RFC 5322 grezzi dal blob store del server —
  parsing e persistenza identici all'IMAP.
- **Push**: EventSource (`types=Email`) con fallback al poll di 5 minuti.
- **Flag** (`$seen`, `$flagged`, `$draft`, `$answered`) mappati sui flag IMAP.

Nota (M5): un'email spostata sul server in una cartella non ancora sincronizzata
può comparire due volte in locale fino alla sync successiva della cartella di
destinazione.

## Token e sicurezza

Il token vive nella keychain di sistema (voce `account/<id>`), come le
password; non raggiunge mai il webview. Il trasporto è sempre HTTPS.
