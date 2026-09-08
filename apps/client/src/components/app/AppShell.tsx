import * as React from "react"
import { LucideProvider, PenLine, Search } from "lucide-react"

import { cn } from "@/lib/utils"
import { navigate } from "@/lib/router"
import { keyboardEnabled } from "@/lib/settings"
import { useT, watchLocale } from "@/lib/i18n"
import { maybeRunAutoBackup } from "@/lib/autoBackup"
import { watchThemeMode } from "@/lib/theme"
import { Button } from "@/components/ui/button"
import { Toaster } from "@/components/ui/sonner"
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { TooltipProvider } from "@/components/ui/tooltip"
import type { Account, Folder } from "@/types"
import { AppSidebar } from "./AppSidebar"
import { OfflineBanner } from "./OfflineBanner"
import { TitleBar } from "./TitleBar"

interface AppShellProps {
  accounts: Account[]
  folders: Folder[]
  activeFolderId: string
  /** the unified inbox (?account=all) is the active view */
  unifiedActive?: boolean
  /** "fill" → the page owns its scroll (inbox/thread/search); "page" → the shell scrolls. */
  contentMode?: "fill" | "page"
  children: React.ReactNode
}

/**
 * AppShell — the persistent application frame: shadcn Sidebar (with the
 * account switcher + folder nav) on the left, a slim topbar, and the page
 * content. Also owns the global keyboard shortcuts and the sonner Toaster.
 */
export function AppShell({
  accounts,
  folders,
  activeFolderId,
  unifiedActive,
  contentMode = "fill",
  children,
}: AppShellProps) {
  const t = useT()
  React.useEffect(() => watchThemeMode(), [])
  React.useEffect(() => watchLocale(), [])
  useGlobalShortcuts()
  // daily automatic backup of the encrypted DB (Settings → Backup): fire
  // and forget, silent on failure (retried on the next start)
  React.useEffect(() => {
    void maybeRunAutoBackup()
  }, [])

  return (
    // Design system: every lucide icon defaults to a 1.75px stroke on the
    // 24×24 grid (see "Mail Design System" → Icone). Call sites still set
    // their own render size via className/size; strokeWidth stays uniform
    // unless a call site overrides it (e.g. TitleBar's tiny traffic-light
    // glyphs, which need a heavier stroke to read at 8px).
    <LucideProvider strokeWidth={1.75}>
      <TooltipProvider>
        {/* Window frame: fills the transparent Tauri window and — on macOS —
            draws its own rounded corners (see global.css). */}
        <div className="app-frame flex h-dvh min-h-0 flex-col overflow-hidden bg-background">
          <TitleBar />
          <SidebarProvider className="min-h-0 flex-1">
            {/* The sidebar is a normal in-flow flex item (see ui/sidebar.tsx):
                it reserves its own space instead of floating fixed above the
                content, so the mail list can never cover it (WKWebView
                compositing bug in the transparent Tauri window).
                Height: stretched by the flex row — no h-svh/offsets needed. */}
            <AppSidebar
              accounts={accounts}
              folders={folders}
              activeFolderId={activeFolderId}
              unifiedActive={unifiedActive}
            />
            <SidebarInset className="flex min-h-0 min-w-0 flex-col">
              <header className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-3">
                <SidebarTrigger className="-ml-1" />
                <div className="ml-auto flex items-center gap-1">
                  <Button
                    asChild
                    variant="ghost"
                    size="icon"
                    aria-label={t("nav.search")}
                    title={t("nav.search")}
                  >
                    <a href="/search">
                      <Search />
                    </a>
                  </Button>
                  <Button asChild size="sm">
                    <a href="/compose">
                      <PenLine data-icon="inline-start" />
                      {t("nav.compose")}
                    </a>
                  </Button>
                </div>
              </header>
              <OfflineBanner />
              <main
                className={cn(
                  "flex min-h-0 flex-1 flex-col",
                  contentMode === "page" && "overflow-y-auto",
                )}
              >
                {children}
              </main>
            </SidebarInset>
            <Toaster position="bottom-right" richColors closeButton />
          </SidebarProvider>
        </div>
      </TooltipProvider>
    </LucideProvider>
  )
}

/**
 * Global keyboard shortcuts (Settings → Tastiera can disable them):
 *   j / ↓ , k / ↑  move focus between message rows
 *   Enter          open the focused row (native anchor activation)
 *   r              compose
 *   a              archive the focused thread (dispatches mail:archive-thread)
 */
function useGlobalShortcuts(): void {
  React.useEffect(() => {
    function rows(): HTMLElement[] {
      return Array.from(document.querySelectorAll<HTMLElement>("[data-thread-row]"))
    }

    function focusedRowIndex(list: HTMLElement[]): number {
      const active = document.activeElement
      return list.findIndex((r) => r === active)
    }

    function onKeyDown(e: KeyboardEvent): void {
      if (!keyboardEnabled()) return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const target = e.target as HTMLElement | null
      if (target?.closest("input, textarea, select, [contenteditable]")) return

      const list = rows()
      const idx = focusedRowIndex(list)

      switch (e.key) {
        case "j":
        case "ArrowDown": {
          if (list.length === 0) return
          e.preventDefault()
          list[Math.min(idx + 1, list.length - 1)]?.focus()
          break
        }
        case "k":
        case "ArrowUp": {
          if (list.length === 0) return
          e.preventDefault()
          list[Math.max(idx - 1, 0)]?.focus()
          break
        }
        case "r":
          e.preventDefault()
          navigate("/compose")
          break
        case "a": {
          const row = list[idx]
          const threadId = row?.dataset["thread"]
          if (threadId) {
            e.preventDefault()
            window.dispatchEvent(new CustomEvent("mail:archive-thread", { detail: { threadId } }))
          }
          break
        }
      }
    }

    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [])
}
