import type { Account } from "../types"
import { Store } from "../lib/store"

export interface AccountState {
  accounts: Account[]
  /** id of the currently active account */
  activeAccountId: string
  loaded: boolean
}

export const accountStore = new Store<AccountState>({
  accounts: [],
  /** empty until a real account is connected */
  activeAccountId: "",
  loaded: false,
})

export function setAccounts(accounts: Account[]): void {
  accountStore.update((s) => ({
    ...s,
    accounts,
    loaded: true,
    activeAccountId: accounts.find((a) => a.isActive)?.id ?? accounts[0]?.id ?? s.activeAccountId,
  }))
}

export function setActiveAccount(id: string): void {
  accountStore.update((s) => ({
    ...s,
    activeAccountId: id,
    accounts: s.accounts.map((a) => ({ ...a, isActive: a.id === id })),
  }))
}

export function accountById(id: string): Account | undefined {
  return accountStore.get().accounts.find((a) => a.id === id)
}
