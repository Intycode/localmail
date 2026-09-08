/**
 * Desktop platform detection.
 *
 * The user agent inside the Tauri webviews is stable per OS (WKWebView reports
 * "Macintosh", WebView2 reports "Windows"), so this is enough to pick the
 * window-controls style for the custom title bar. Mirrors the pre-paint
 * bootstrap in BaseLayout.astro.
 */

export type DesktopOs = "macos" | "windows" | "linux" | "unknown"

export function detectOs(): DesktopOs {
  if (typeof navigator === "undefined") return "unknown"
  const ua = navigator.userAgent
  if (ua.includes("Macintosh") || ua.includes("Mac OS X")) return "macos"
  if (ua.includes("Windows")) return "windows"
  if (ua.includes("Linux") || ua.includes("X11")) return "linux"
  return "unknown"
}
