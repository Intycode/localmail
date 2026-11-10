import { fileURLToPath } from "node:url"
import { readFileSync } from "node:fs"
import { defineConfig } from "astro/config"
import react from "@astrojs/react"
import tailwindcss from "@tailwindcss/vite"

/**
 * Astro is configured as a static, client-side UI shell for a Tauri desktop
 * application:
 *  - output: "static" -> build produces plain static assets, no SSR, no remote
 *    web server dependency.
 *
 * Tauri serves the built `dist/` from its own origin (dev uses
 * http://localhost:4321), not from file://, so route and asset URLs are
 * root-absolute ("/accounts", "/_astro/app.js") — Astro's default base, and
 * what the app's own links already use. Astro 7 no longer emits relative
 * asset URLs for `base: "./"` (it prefixes them with "/./" and breaks them),
 * so a relative base is deliberately not configured.
 */
// Single source of truth: the root package.json version.
const { version } = JSON.parse(readFileSync(new URL("../../package.json", import.meta.url), "utf8"))

export default defineConfig({
  output: "static",
  integrations: [react()],
  vite: {
    plugins: [tailwindcss()],
    define: { __APP_VERSION__: JSON.stringify(version) },
    resolve: {
      alias: {
        "@": fileURLToPath(new URL("./src", import.meta.url)),
      },
    },
  },
})
