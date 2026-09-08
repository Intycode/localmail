import type { Route, UiState } from "../types"
import { Store } from "../lib/store"

export interface UiStateWithNav extends UiState {
  /** keyboard-focused list index */
  focusIndex: number
}

export const uiStore = new Store<UiStateWithNav>({
  /** empty until a real account is connected */
  activeAccountId: "",
  activeFolderId: "f-inbox",
  route: { name: "inbox", folderId: "f-inbox" },
  sidebarOpen: true,
  compact: false,
  focusIndex: 0,
})

export function setRoute(route: Route): void {
  uiStore.update((s) => ({ ...s, route }))
}

export function setActiveFolder(folderId: string): void {
  uiStore.update((s) => ({ ...s, activeFolderId: folderId }))
}

export function setSidebarOpen(open: boolean): void {
  uiStore.update((s) => ({ ...s, sidebarOpen: open }))
}

export function setFocusIndex(i: number): void {
  uiStore.update((s) => ({ ...s, focusIndex: i }))
}
