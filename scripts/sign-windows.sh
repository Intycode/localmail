#!/usr/bin/env bash
# Firma Authenticode dei binari Windows che Tauri non firma dopo il patching
# (mail.exe e mail-engine.exe vengono riscritti da WiX/NSIS dopo la firma,
#  invalidando la firma — gli installer invece restano firmati da Tauri).
# Richiede: token USB inserito, certificato nello store Windows.
set -euo pipefail

THUMBPRINT="6D1F281F5D7BC0432ABDDD9B426103441FB7AC33"
TIMESTAMP_URL="http://timestamp.sectigo.com"
REL="src-tauri/target/release"

# ultimo Windows Kit installato
ST=$(ls "/c/Program Files (x86)/Windows Kits/10/bin/"*/x64/signtool.exe 2>/dev/null | sort -V | tail -1)
if [ -z "$ST" ]; then
  echo "signtool non trovato: installa Windows SDK" >&2
  exit 1
fi

export MSYS_NO_PATHCONV=1

FILES=(
  "$REL/mail.exe"
  "$REL/mail-engine.exe"
)

for f in "${FILES[@]}"; do
  if [ ! -f "$f" ]; then
    echo "SKIP: $f non esiste" >&2
    continue
  fi
  if "$ST" verify /pa "$f" >/dev/null 2>&1; then
    echo "OK (già firmato): $f"
    continue
  fi
  echo "Firmando: $f"
  "$ST" sign /sha1 "$THUMBPRINT" /fd SHA256 /tr "$TIMESTAMP_URL" /td SHA256 "$f"
  "$ST" verify /pa "$f" >/dev/null && echo "Verificato: $f"
done
