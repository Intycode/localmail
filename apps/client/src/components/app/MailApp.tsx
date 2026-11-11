import * as React from "react"

import { installRouter, parseUrl, routerStore, type AppRoute, type RouterState } from "@/lib/router"
import { useAccounts } from "@/lib/useAccounts"
import { onboardingDone, setOnboardingDone } from "@/lib/settings"
import { shouldShowOnboarding } from "@/lib/onboarding"
import { accountStore, setAccounts } from "@/stores/accountStore"
import { getMailService } from "@/services/serviceRegistry"
import { startAutoSync, wireEventStream } from "@/services/syncService"
import { isTauri } from "@/services/tauriMailService"
import type { Account, Folder, Thread } from "@/types"
import { AppShell } from "./AppShell"
import { OnboardingWizard } from "./onboarding/OnboardingWizard"
import { InboxPage } from "./pages/InboxPage"
import { ThreadPageLoader } from "./pages/ThreadPageLoader"
import { ComposePage } from "./pages/ComposePage"
import { SearchPage } from "./pages/SearchPage"
import { AccountsPage } from "./pages/AccountsPage"
import { SettingsPage } from "./pages/SettingsPage"

export type MailRoute = "inbox" | "thread" | "compose" | "search" | "accounts" | "settings"

export interface MailAppProps {
  route: MailRoute
  accounts: Account[]
  folders: Folder[]
  activeFolderId: string
  /** thread route (legacy prerendered payload; runtime threads use ?id=) */
  thread?: Thread
}

/**
 * MailApp — the single React island mounted by every Astro page.
 *
 * The Astro shells are static, so this island owns client-side routing: the
 * mini router swaps the page content in place (no full reload, the sidebar
 * stays stable) and keeps the URL in sync via the History API. Deep links
 * keep working because every route is also a real URL.
 */
export default function MailApp(props: MailAppProps) {
  // The Astro shells are static: their props are frozen at build time.
  // Under Tauri we hydrate the real accounts from the engine at mount and
  // keep them fresh via the event stream; in the browser the props stand in.
  const accounts = useAccounts(props.accounts)
  const active = accounts.find((a) => a.isActive) ?? accounts[0]
  const folders: Folder[] = active?.folders?.length ? active.folders : props.folders
  const fallbackFolderId = folders[0]?.id ?? props.activeFolderId

  // router state: seeded from the SSR route name, synced to the real URL at
  // mount (window is not available while Astro prerenders)
  const [routerState, setRouterState] = React.useState<RouterState>(() => ({
    route: { name: props.route } as AppRoute,
    navKey: 0,
  }))

  // first-run onboarding: as soon as the accounts are hydrated, open the
  // wizard once (zero accounts + flag never set). Any account from that
  // moment on marks the onboarding as done, so it never shows again.
  const [onboardingOpen, setOnboardingOpen] = React.useState(false)
  React.useEffect(() => {
    if (!isTauri()) return
    return accountStore.subscribe((s) => {
      if (!s.loaded) return
      if (s.accounts.length > 0) {
        if (!onboardingDone()) setOnboardingDone()
      } else if (shouldShowOnboarding({ isTauri: true, loaded: true, accountCount: 0 })) {
        setOnboardingOpen(true)
      }
    })
  }, [])

  React.useEffect(() => {
    installRouter()
    const unsubscribe = routerStore.subscribe(setRouterState)
    routerStore.set({
      route: parseUrl(new URL(window.location.href)),
      navKey: routerStore.get().navKey + 1,
    })
    if (isTauri()) {
      void wireEventStream()
      startAutoSync()
      void (async () => {
        try {
          const svc = await getMailService()
          setAccounts(await svc.getAccounts())
        } catch (err) {
          console.error("errore caricamento account", err)
        }
      })()
    }
    return unsubscribe
  }, [])

  const { route, navKey } = routerState

  // the selected folder drives both the sidebar highlight and the inbox list
  const routeFolderId =
    route.name === "inbox" || route.name === "thread" ? route.folderId : undefined
  const activeFolderId = routeFolderId ?? fallbackFolderId

  return (
    <>
      <AppShell
        accounts={accounts}
        folders={folders}
        activeFolderId={activeFolderId}
        unifiedActive={route.name === "inbox" && route.accountId === "all"}
        contentMode={pageMode(route) ? "page" : "fill"}
      >
        {renderPage(route, navKey, accounts, activeFolderId)}
      </AppShell>
      {onboardingOpen && <OnboardingWizard onClose={() => setOnboardingOpen(false)} />}
    </>
  )
}

function pageMode(route: AppRoute): boolean {
  return route.name === "compose" || route.name === "accounts" || route.name === "settings"
}

function renderPage(
  route: AppRoute,
  navKey: number,
  accounts: Account[],
  activeFolderId: string,
): React.ReactNode {
  // navKey in the key makes each navigation remount the page component so it
  // refetches its data fresh.
  switch (route.name) {
    case "thread":
      return (
        <ThreadPageLoader
          key={`thread:${route.threadId ?? ""}:${navKey}`}
          threadId={route.threadId}
        />
      )
    case "compose":
      return <ComposePage accounts={accounts} />
    case "search":
      return <SearchPage />
    case "accounts":
      return <AccountsPage accounts={accounts} />
    case "settings":
      return <SettingsPage />
    case "inbox":
    default:
      return (
        <InboxPage
          key={`inbox:${route.accountId ?? ""}:${route.folderId ?? activeFolderId}:${navKey}`}
          threads={[]}
          folderId={route.folderId ?? activeFolderId}
          accountId={route.accountId}
          accounts={accounts}
        />
      )
  }
}
