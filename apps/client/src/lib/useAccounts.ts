import * as React from "react"

import type { Account } from "../types"
import { accountStore } from "../stores/accountStore"

/**
 * useAccounts — reactive accounts list backed by accountStore, seeded with
 * the (static, build-time) props and refreshed by the runtime hydration:
 * MailApp fetches the real accounts from the engine under Tauri, and
 * syncService keeps the store fresh on engine events.
 */
export function useAccounts(fallback: Account[]): Account[] {
  const initial = accountStore.get().loaded ? accountStore.get().accounts : fallback
  const [accounts, setLocal] = React.useState<Account[]>(initial)

  React.useEffect(
    () =>
      accountStore.subscribe((s) => {
        if (s.loaded) setLocal(s.accounts)
      }),
    [],
  )

  return accounts
}
