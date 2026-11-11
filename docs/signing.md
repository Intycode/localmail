# Firma e distribuzione (fase 5)

LocalMail si distribuisce tramite GitHub Releases, con **auto-update firmato**
(plugin updater di Tauri). Le build si fanno in locale con `scripts/release.ts`
(macOS sul Mac, Windows sulla macchina con il token USB): la procedura
completa è in `CLAUDE.md` → "Rilasci".

## macOS (codesign + notarization)

Il pacchetto `.app`/`.dmg` non firmato viene bloccato da Gatekeeper ("app
di sviluppatore non identificato"). Per una distribuzione pulita servono:

1. **Apple Developer Program** (99 $/anno).
2. Un **Developer ID Application** certificate (esportata come `.p12`).
3. Un **app-specific password** per il notarization (appleid.apple.com).

### Segreti GitHub (repo Settings → Secrets → Actions)

| Segreto | Contenuto |
|---|---|
| `APPLE_CERTIFICATE` | certificato `.p12` in base64 (`base64 -i cert.p12`) |
| `APPLE_CERTIFICATE_PASSWORD` | password del `.p12` |
| `APPLE_SIGNING_IDENTITY` | es. `Developer ID Application: Nome (TEAMID)` |
| `APPLE_ID` | Apple ID per il notarization |
| `APPLE_PASSWORD` | app-specific password |
| `APPLE_TEAM_ID` | Team ID (10 caratteri) |

Con i segreti presenti il workflow firma **e** notarizza automaticamente
(tauri-action usa l'API di notarization di Apple); senza segreti produce
artefatti non firmati, avviabili con `xattr -cr Mail.app` per il test.

## Windows

Il pacchetto `.msi`/`.exe` firmato richiede un **code-signing certificate**
(OV da DigiCert/Sectigo o Azure Trusted Signing). Con Trusted Signing si
usa l'integrazione di Tauri (`tauri.conf.json` → `bundle > windows >
certificateThumbprint` ecc.); con un `.pfx` basta esporlo nel workflow come
segreto e impostare `WINDOWS_PFX`/`WINDOWS_PFX_PASSWORD`. Senza firma,
SmartScreen avvisa l'utente ("Esegui comunque").

## Linux

AppImage e `.deb` non richiedono firma per il download manuale; eventuali
repository firmati (apt/rpm + GPG) restano fuori scope.

## Distribuzione

- I bundle firmati vengono allegati alla GitHub Release del tag.
- Auto-update: all'avvio l'app legge
  `releases/latest/download/latest.json`, verifica la firma minisign
  (chiave pubblica in `tauri.conf.json → plugins.updater.pubkey`) e propone
  l'installazione. La release resta draft finché non ci sono tutte le piattaforme, poi
  `release build` la pubblica come "latest".
- Verifica consigliata agli utenti: confrontare lo SHA-256 dell'artefatto
  con quello pubblicato nella release.
