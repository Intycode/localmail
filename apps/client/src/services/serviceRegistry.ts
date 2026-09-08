import type { MailService } from "./mailService"
import { LocalMailService } from "./localMailService"
import { TauriMailService, isTauri } from "./tauriMailService"

/**
 * serviceRegistry — lazy singleton for the active MailService.
 * Inside the Tauri webview the Go-backed TauriMailService is used; in the
 * plain browser (bun dev / tests) the empty LocalMailService keeps working.
 */
let instance: MailService | null = null

export async function getMailService(): Promise<MailService> {
  if (!instance) {
    instance = isTauri() ? new TauriMailService() : new LocalMailService()
  }
  return instance
}

/** test hook */
export function _resetMailService(): void {
  instance = null
}
