import * as React from "react"
import { getCurrentWindow } from "@tauri-apps/api/window"
import { Copy, Minus, Plus, Square, X } from "lucide-react"

import { detectOs, type DesktopOs } from "@/lib/platform"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"
import { isTauri } from "@/services/tauriMailService"

/**
 * TitleBar — the custom window title bar.
 *
 * The Tauri window ships with `decorations: false` (tauri.conf.json), so this
 * bar replaces the native chrome: a slim draggable strip (the header itself is
 * the data-tauri-drag-region; double-click on it toggles maximize natively)
 * with the app identity on the left and platform-appropriate window controls —
 * traffic lights on macOS, minimize/maximize/close on Windows and Linux.
 *
 * In a plain browser (bun run dev) the bar renders without controls, so the
 * layout stays identical while developing.
 *
 * The window-control commands are allowed in
 * src-tauri/capabilities/default.json.
 */
export function TitleBar() {
  const t = useT()
  // Decided post-mount so the SSR output and the first client render match;
  // the controls then appear before the window is even visible.
  const [chrome, setChrome] = React.useState<DesktopOs | null>(null)
  const [active, setActive] = React.useState(false)
  const [focused, setFocused] = React.useState(true)

  React.useEffect(() => {
    if (isTauri()) setChrome(detectOs())
  }, [])

  // Maximized/fullscreen flatten the macOS corner radius, and an unfocused
  // window dims the traffic lights. Both come from the same event wiring.
  React.useEffect(() => {
    if (!chrome) return
    let disposed = false
    const unlisteners: Array<() => void> = []
    void (async () => {
      const win = getCurrentWindow()
      const syncState = async () => {
        const [isMaximized, isFullscreen] = await Promise.all([
          win.isMaximized(),
          win.isFullscreen(),
        ])
        if (disposed) return
        setActive(isMaximized || isFullscreen)
      }
      await syncState()
      unlisteners.push(await win.onResized(() => void syncState()))
      unlisteners.push(
        await win.onFocusChanged(({ payload }) => {
          if (!disposed) setFocused(payload)
        }),
      )
    })()
    return () => {
      disposed = true
      for (const unlisten of unlisteners) unlisten()
    }
  }, [chrome])

  React.useEffect(() => {
    if (!chrome) return
    document.documentElement.dataset.winMax = active ? "true" : "false"
  }, [chrome, active])

  return (
    <header
      data-tauri-drag-region
      className="relative z-20 flex h-(--titlebar-h) shrink-0 select-none items-center border-b border-border bg-sidebar"
    >
      {chrome === "macos" && (
        <div data-tauri-drag-region className="flex items-center gap-2 px-3">
          <TrafficDot
            label={t("window.close")}
            colorClass="bg-[#ff5f57]"
            focused={focused}
            onClick={() => void getCurrentWindow().close()}
          >
            <X
              strokeWidth={3}
              className="size-2 text-black/55 opacity-0 group-hover/dot:opacity-100"
            />
          </TrafficDot>
          <TrafficDot
            label={t("window.minimize")}
            colorClass="bg-[#febc2e]"
            focused={focused}
            onClick={() => void getCurrentWindow().minimize()}
          >
            <Minus
              strokeWidth={3}
              className="size-2 text-black/55 opacity-0 group-hover/dot:opacity-100"
            />
          </TrafficDot>
          <TrafficDot
            label={t("window.maximize")}
            colorClass="bg-[#28c840]"
            focused={focused}
            // toggleMaximize is a no-op on borderless macOS windows: use fullscreen.
            onClick={async () => {
              const win = getCurrentWindow()
              await win.setFullscreen(!(await win.isFullscreen()))
            }}
          >
            <Plus
              strokeWidth={3}
              className="size-2 text-black/55 opacity-0 group-hover/dot:opacity-100"
            />
          </TrafficDot>
        </div>
      )}

      {/* Centered on the window, not the free space; pointer-events-none keeps the header draggable. */}
      <span className="pointer-events-none absolute inset-x-0 text-center text-xs font-medium text-foreground/80">
        LocalMail
      </span>

      {chrome && chrome !== "macos" && (
        <div className="ml-auto flex h-full items-stretch">
          <WindowButton
            label="Minimizza finestra"
            onClick={() => void getCurrentWindow().minimize()}
          >
            <Minus className="size-3" />
          </WindowButton>
          <WindowButton
            label={active ? t("window.restore") : t("window.maximize")}
            onClick={() => void getCurrentWindow().toggleMaximize()}
          >
            {active ? <Copy className="size-2.5" /> : <Square className="size-2.5" />}
          </WindowButton>
          <WindowButton
            danger
            label="Chiudi finestra"
            onClick={() => void getCurrentWindow().close()}
          >
            <X className="size-3" />
          </WindowButton>
        </div>
      )}
    </header>
  )
}

/** macOS traffic light: a small dot that reveals its glyph on hover and dims when the window loses focus. */
function TrafficDot(props: {
  label: string
  colorClass: string
  focused: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={props.label}
      title={props.label}
      onClick={props.onClick}
      className={cn(
        "group/dot flex size-3 items-center justify-center rounded-full outline-none focus-visible:outline-2 focus-visible:outline-ring",
        props.focused ? props.colorClass : "bg-[#c9c9c7] dark:bg-[#55555a]",
      )}
    >
      {props.children}
    </button>
  )
}

/** Windows/Linux caption button: a full-height strip with hover/active states. */
function WindowButton(props: {
  label: string
  danger?: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={props.label}
      title={props.label}
      onClick={props.onClick}
      className={cn(
        "flex w-12 items-center justify-center text-foreground/80 outline-none",
        "focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring",
        props.danger
          ? "hover:bg-[#e81123] hover:text-white active:bg-[#c50f1f]"
          : "hover:bg-accent hover:text-foreground active:bg-accent/70",
      )}
    >
      {props.children}
    </button>
  )
}
