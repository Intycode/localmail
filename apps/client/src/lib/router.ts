import { Store } from "./store"

/**
 * Mini client-side router — gives the static Astro shells a SPA feel.
 *
 * Clicks on internal links are intercepted: the URL is pushed via the History
 * API and only the island's content re-renders (the sidebar never reloads).
 * External URLs, modifier-clicks and target=_blank fall through to the
 * browser. Deep links keep working because every route is also a real URL.
 */

export type AppRoute =
  | { name: "inbox"; folderId?: string; accountId?: string }
  | { name: "thread"; threadId?: string; folderId?: string }
  | { name: "compose"; replySubject?: string }
  | { name: "search" }
  | { name: "accounts" }
  | { name: "settings" }

export interface RouterState {
  route: AppRoute
  /** bumps on every navigation so pages can key-remount and refetch */
  navKey: number
}

export const routerStore = new Store<RouterState>({ route: { name: "inbox" }, navKey: 0 })

const ROUTES = new Set(["/", "/thread", "/compose", "/search", "/accounts", "/settings"])

function cleanPath(pathname: string): string {
  const p = pathname.replace(/\/+$/, "")
  return p === "" ? "/" : p
}

export function parseUrl(url: URL): AppRoute {
  const path = cleanPath(url.pathname)
  const params = url.searchParams
  switch (path) {
    case "/thread":
      return {
        name: "thread",
        threadId: params.get("id") ?? undefined,
        folderId: params.get("folder") ?? undefined,
      }
    case "/compose":
      return { name: "compose", replySubject: params.get("subject") ?? undefined }
    case "/search":
      return { name: "search" }
    case "/accounts":
      return { name: "accounts" }
    case "/settings":
      return { name: "settings" }
    default:
      return {
        name: "inbox",
        folderId: params.get("folder") ?? undefined,
        // ?account=all is the unified inbox; ?account=<id> scopes one account
        accountId: params.get("account") ?? undefined,
      }
  }
}

/** Push a route client-side; falls back to a full load for foreign URLs. */
export function navigate(href: string): void {
  if (typeof window === "undefined") return
  const url = new URL(href, window.location.origin)
  if (url.origin !== window.location.origin || !ROUTES.has(cleanPath(url.pathname))) {
    window.location.href = href
    return
  }
  window.history.pushState({}, "", url.pathname + url.search)
  routerStore.set({
    route: parseUrl(url),
    navKey: routerStore.get().navKey + 1,
  })
}

let installed = false

/** Wire global click interception + popstate. Idempotent. */
export function installRouter(): void {
  if (installed || typeof window === "undefined") return
  installed = true

  document.addEventListener(
    "click",
    (e) => {
      if (e.defaultPrevented || e.button !== 0) return
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
      const anchor = (e.target as HTMLElement | null)?.closest<HTMLAnchorElement>("a[href]")
      if (!anchor || anchor.target === "_blank") return
      const url = new URL(anchor.href, window.location.origin)
      if (url.origin !== window.location.origin || !ROUTES.has(cleanPath(url.pathname))) return
      e.preventDefault()
      navigate(anchor.href)
    },
    true,
  )

  window.addEventListener("popstate", () => {
    routerStore.set({
      route: parseUrl(new URL(window.location.href)),
      navKey: routerStore.get().navKey + 1,
    })
  })
}
