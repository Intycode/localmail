# OAuth2 con Google (Gmail) — M4

Mail supporta Gmail via **OAuth2 + XOAUTH2** (IMAP e SMTP): nessuna
password applicativa, nessuna password memorizzata da Google per l'app.

## Come funziona

1. Account → **Aggiungi** → "Collega Google": Mail apre il **browser di
   sistema** sulla schermata di consenso Google (authorization code +
   **PKCE**).
2. Il redirect torna su un **server loopback** locale:
   `http://127.0.0.1:8734/callback` (nessun traffico verso terzi).
3. Mail scambia il codice con Google e ottiene `refresh_token` +
   `access_token`; l'account viene creato con provider `gmail-oauth`.
4. Il **refresh token** vive nella keychain di sistema (voce
   `account/<id>`); l'**access token** (~1 h) è **solo in memoria** del
   processo, con una cache criptata-dalla-keychain (`oauth/<id>`): al bisogno
   Mail lo rinnova automaticamente prima di parlare con IMAP/SMTP.
5. Scope richiesto: `https://mail.google.com/` (lettura/scrittura posta) +
   `email` (per ricavare l'indirizzo).

## Setup una tantum (account Google sviluppatore)

Mail non integra credenziali Google di terze parti: usi le **tue**.

1. [Google Cloud Console](https://console.cloud.google.com/) → crea un
   progetto (es. "Mail").
2. **API & Services → Library**: abilita **Gmail API**.
3. **OAuth consent screen**: tipo *External* (o *Internal* se Workspace),
   aggiungi il tuo account come *test user*.
4. **Credentials → Create credentials → OAuth client ID**:
   - Application type: **Desktop app**
   - Scarica il client: copia `client_id` e `client_secret`.
5. Registra la redirect: nel client desktop di tipo "Desktop app" Google
   accetta `http://127.0.0.1:<porta>`; di default Mail usa la porta **8734**
   (`http://127.0.0.1:8734/callback`).
6. Nel tuo **account Google** (myaccount.google.com → Security) verifica che
   **IMAP sia abilitato** (Gmail → Settings → Forwarding and POP/IMAP).
7. Scrivi `oauth.json` nella cartella dati dell'app
   (`~/Library/Application Support/dev.mail.client/oauth.json` su macOS):

```json
{
  "google": {
    "client_id": "1234-abc.apps.googleusercontent.com",
    "client_secret": "GOCSPX-...",
    "port": 8734
  }
}
```

Riavvia l'app: il pulsante "Collega Google" sarà attivo.

# OAuth2 con Microsoft (Outlook)

Mail supporta Outlook/Office365 via **Microsoft Graph + OAuth2 device-code**
(provider `microsoft`). Nessuna password: solo token nella keychain.

## Come funziona

1. Account → **Aggiungi** → "Collega Microsoft": Mail apre il browser su
   `microsoft.com/devicelogin` e mostra il **codice da digitare**.
2. L'app interroga il token endpoint finché l'utente completa (device-code
   flow, RFC 8628) e ottiene `refresh_token` + `access_token`.
3. L'account viene creato con provider `microsoft`; le cartelle sono il set
   well-known di Outlook (Inbox, Sent, Drafts, Deleted, Archive, Junk).
4. Il refresh token vive nella keychain; l'access token è rinnovato
   automaticamente e mai persistito.

## Setup una tantum (registrazione app Microsoft Entra)

1. [Azure Portal → App registrations](https://portal.azure.com/#blade/Microsoft_Azure_RegisteredApps) →
   **New registration** (tipo *Single tenant* o *Multitenant*).
2. Nessuna redirect URI necessaria (device-code flow, client pubblico).
3. **API permissions → Microsoft Graph → Delegated**: `Mail.ReadWrite`,
   `Mail.Send`, `offline_access`, `openid`.
4. Copia l'**Application (client) ID** in `oauth.json`:

```json
{
  "microsoft": {
    "client_id": "<application-client-id>"
  }
}
```

Per tenant con policy restrittive può servire anche `"client_secret"`
(client confidenziale). Riavvia l'app: il pulsante "Collega Microsoft" sarà
attivo.

## Limiti v1 (documentati)

- Push in tempo reale non disponibile (niente IDLE su Graph): sync
  periodica ogni 5 minuti.
- La riconciliazione delle cancellazioni copre la finestra sincronizzata;
  una resync completa allinea tutto.
- Le bozze restano solo locali (Graph richiede un endpoint dedicato).

## Sicurezza

- PKCE `S256` + `state` casuale verificato sul redirect (anti-CSRF).
- Il client secret non lascia mai il processo Rust; il refresh token vive
  solo nella keychain; l'access token solo in memoria (cache nella keychain,
  mai su disco in chiaro).
- `refresh_token` viene revocato revocando l'accesso dall'account Google
  (myaccount.google.com → Security → Third-party access).
