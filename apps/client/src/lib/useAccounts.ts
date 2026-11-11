import * as React from "react"

import type { Account } from "../types"
import { accountStore, setActiveAccount } from "../stores/accountStore"
import { getMailService } from "../services/serviceRegistry"
import { navigate } from "./router"

/** Makes `id` the active account (store + engine) and opens its inbox. */
export async function switchAccount(id: string): Promise<void> {
  setActiveAccount(id)
  const svc = (await getMailService()) as { setActive?: (id: string) => Promise<void> }
  await svc.setActive?.(id)
  navigate("/")
}

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
