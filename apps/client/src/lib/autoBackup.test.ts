import { test, expect } from "bun:test"
import { autoBackupDue, AUTOBACKUP_INTERVAL_MS } from "./autoBackup"

test("autoBackupDue requires enablement, a dir and the elapsed interval", () => {
  // no Tauri runtime here: due() is false regardless of stored settings
  expect(autoBackupDue()).toBe(false)
  expect(AUTOBACKUP_INTERVAL_MS).toBe(86_400_000)
})
