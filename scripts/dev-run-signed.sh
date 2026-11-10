#!/usr/bin/env bash
# Cargo runner (see src-tauri/.cargo/config.toml): signs the debug binary with
# a stable local identity, then runs it. An unsigned binary changes identity on
# every build, so macOS re-asks for the keychain password each launch.
#
# One-time setup: Keychain Access > Certificate Assistant > Create a
# Certificate... > Name "Mail Dev", Type "Code Signing". Then click
# "Always Allow" on the first keychain prompt.
# Override the name with MAIL_DEV_SIGN_IDENTITY.
set -u
ID="${MAIL_DEV_SIGN_IDENTITY:-Mail Dev}"
BIN="$1"
if security find-certificate -c "$ID" >/dev/null 2>&1; then
  codesign --force --sign "$ID" --identifier dev.mail.client "$BIN" >/dev/null 2>&1 \
    || echo "dev-run-signed: signing failed, running unsigned" >&2
else
  echo "dev-run-signed: identity '$ID' not found, running unsigned (keychain will re-prompt)" >&2
fi
exec "$@"
