#!/usr/bin/env bash
# Compila il motore Go (backend/cmd/mail) per la piattaforma host e lo copia
# in src-tauri/binaries con i suffissi attesi da bundle.externalBin.
set -euo pipefail

# shortcut: percorso Go di default su Windows, per terminali con PATH vecchio; togliere se Go è sempre nel PATH
command -v go >/dev/null || export PATH="$PATH:/c/Program Files/Go/bin"

cd backend

# Motore puro Go (sqlite wasm): niente CGO, cross-compila senza toolchain C.
export CGO_ENABLED=0

GOPATH="$PWD/.go-home" GOCACHE="$PWD/.go-home/build" go build -trimpath -o ../src-tauri/binaries/mail-engine ./cmd/mail

HOST_TRIPLE=$(rustc -vV | sed -n 's/host: //p')
cp ../src-tauri/binaries/mail-engine "../src-tauri/binaries/mail-engine-$HOST_TRIPLE"
# Tauri su Windows si aspetta anche la variante .exe
case "$HOST_TRIPLE" in
  *windows*)
    cp ../src-tauri/binaries/mail-engine "../src-tauri/binaries/mail-engine-$HOST_TRIPLE.exe"
    ;;
esac

echo "motore compilato per $HOST_TRIPLE"
