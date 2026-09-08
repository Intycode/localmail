#!/usr/bin/env bash
# Compila il motore Go (backend/cmd/mail) per la piattaforma host e lo copia
# in src-tauri/binaries con i suffissi attesi da bundle.externalBin.
#
# Su Windows serve CGO_ENABLED=1: go-fitz (MuPDF per renderizzare i PDF)
# linka staticamente le librerie vendute nel modulo (libmupdf_windows_amd64.a)
# e con CGO_ENABLED=0 ricade su purego, che richiede libmupdf.dll a runtime
# e fa panicare il motore all'avvio se la DLL manca.
# Compilatore: mingw-w64 (winget install BrechtSanders.WinLibs.POSIX.UCRT).
set -euo pipefail

cd backend

# mingw-w64 installato via winget (fuori PATH di default in Git Bash)
MINGW_BIN="${LOCALAPPDATA:-$HOME/AppData/Local}/Microsoft/WinGet/Packages/BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe/mingw64/bin"
if [ -d "$MINGW_BIN" ]; then
  export PATH="$MINGW_BIN:$PATH"
fi

case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*)
    export CGO_ENABLED=1
    ;;
  *)
    # macOS/Linux: clang/gcc presente di default, link statico venduto nel modulo
    export CGO_ENABLED=1
    ;;
esac

if ! command -v gcc >/dev/null 2>&1; then
  echo "gcc non trovato: installa mingw-w64 -> winget install BrechtSanders.WinLibs.POSIX.UCRT" >&2
  exit 1
fi

GOPATH="$PWD/.go-home" GOCACHE="$PWD/.go-home/build" go build -o ../src-tauri/binaries/mail-engine ./cmd/mail

HOST_TRIPLE=$(rustc -vV | sed -n 's/host: //p')
cp ../src-tauri/binaries/mail-engine "../src-tauri/binaries/mail-engine-$HOST_TRIPLE"
# Tauri su Windows si aspetta anche la variante .exe
case "$HOST_TRIPLE" in
  *windows*)
    cp ../src-tauri/binaries/mail-engine "../src-tauri/binaries/mail-engine-$HOST_TRIPLE.exe"
    ;;
esac

echo "motore compilato per $HOST_TRIPLE"
