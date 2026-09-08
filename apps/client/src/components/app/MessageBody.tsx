import * as React from "react"
import DOMPurify from "dompurify"
import { ImageOff, ShieldCheck } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { Message } from "@/types"

/** Link tracking parameters stripped at render time too (legacy messages
 *  synced before the M6 ingest sanitizer stored clean bodies). */
const TRACKING_PARAMS =
  /^(utm_|fbclid|gclid|gclsrc|dclid|gbraid|wbraid|msclkid|twclid|yclid|mc_eid|mc_cid|_hsenc|_hsmi|vero_id|wickedid|trk_|igshid|ref_src|ref_url|spm|scm|snr|pk_|mtm_|ck_campaign|s_kwcid|elq|_openstat)/i

function stripLinkTrackers(html: string): string {
  try {
    const doc = new DOMParser().parseFromString(html, "text/html")
    for (const a of doc.querySelectorAll("a[href]")) {
      const href = a.getAttribute("href") ?? ""
      let url: URL
      try {
        url = new URL(href)
      } catch {
        continue
      }
      if (url.protocol !== "http:" && url.protocol !== "https:") continue
      const params = Array.from(url.searchParams.keys())
      for (const key of params) {
        if (TRACKING_PARAMS.test(key)) url.searchParams.delete(key)
      }
      const cleaned = url.toString()
      if (cleaned !== href) a.setAttribute("href", cleaned)
    }
    return doc.body.innerHTML
  } catch {
    return html
  }
}

/**
 * MessageBody — how a message is rendered everywhere in the app: plain text
 * first; sanitized HTML only when a real html part exists (body !== bodyText).
 * Remote content is blocked by default: the ingest sanitizer already removed
 * tracking pixels permanently, and the remaining remote images load only
 * after explicit consent. Links are restricted to http/https, lose tracking
 * parameters, and open via the OS browser.
 */
export function MessageBody({ message, whiteBg }: { message: Message; whiteBg: boolean }) {
  const [allowRemote, setAllowRemote] = React.useState(false)

  const html = React.useMemo(() => {
    if (typeof window === "undefined") return null // prerender: no DOM
    const body = message.body?.trim() ?? ""
    const text = message.bodyText?.trim() ?? ""
    if (!body) return null
    // Render the HTML part (layout + signature live there). mime.Parse falls
    // back Body = plain text when the mail has no HTML part: in that case
    // body === bodyText and we render text with preserved line breaks.
    if (text && body === text) return null
    const tags = [
      "p",
      "br",
      "div",
      "span",
      "a",
      "b",
      "i",
      "em",
      "strong",
      "u",
      "s",
      "ul",
      "ol",
      "li",
      "h1",
      "h2",
      "h3",
      "h4",
      "h5",
      "h6",
      "blockquote",
      "code",
      "pre",
      "table",
      "thead",
      "tbody",
      "tr",
      "td",
      "th",
      "hr",
      "font",
      "center",
    ]
    // tracking pixels are already gone (removed at ingest); with explicit
    // consent the remaining remote images may load
    if (allowRemote) tags.push("img")
    const clean = DOMPurify.sanitize(body, {
      ALLOWED_TAGS: tags,
      FORBID_ATTR: allowRemote
        ? ["srcset", "onerror", "onload", "onclick"]
        : ["src", "srcset", "onerror", "onload", "onclick"],
    })
    return stripLinkTrackers(clean)
  }, [message.body, message.bodyText, allowRemote])

  React.useEffect(() => {
    if (!html) return
    const root = document.getElementById(`msg-body-${message.id}`)
    if (!root) return
    function onClick(e: MouseEvent): void {
      const anchor = (e.target as HTMLElement | null)?.closest("a")
      if (!anchor) return
      const href = anchor.getAttribute("href") ?? ""
      if (!/^https?:\/\//i.test(href)) {
        e.preventDefault()
        return
      }
      e.preventDefault()
      anchor.setAttribute("target", "_blank")
      anchor.setAttribute("rel", "noopener noreferrer")
      window.open(href, "_blank", "noopener,noreferrer")
    }
    root.addEventListener("click", onClick)
    return () => root.removeEventListener("click", onClick)
  }, [html, message.id])

  if (html) {
    return (
      <div>
        {allowRemote ? (
          <p className="mb-1.5 flex items-center gap-1 text-[11px] text-muted-foreground/80">
            <ShieldCheck className="size-3" />
            Immagini remote caricate su richiesta · link ripuliti dai tracker
          </p>
        ) : (
          <div className="mb-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground/80">
            <span className="flex items-center gap-1">
              <ShieldCheck className="size-3" />
              Contenuti esterni bloccati
            </span>
            {message.remoteImages > 0 && (
              <>
                <span aria-hidden="true">·</span>
                <span>{message.remoteImages} immagini remote</span>
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  className="h-5 px-1.5 text-[11px]"
                  onClick={() => setAllowRemote(true)}
                >
                  <ImageOff className="size-3" aria-hidden="true" />
                  Carica immagini
                </Button>
              </>
            )}
          </div>
        )}
        <div
          id={`msg-body-${message.id}`}
          className={cn(
            "break-words text-sm [&_a]:underline [&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-muted-foreground [&_img]:max-w-full [&_table]:max-w-full [&_td]:align-top",
            whiteBg && "rounded-md bg-white p-3 text-[#1c1c1c]",
          )}
          dangerouslySetInnerHTML={{ __html: html }}
        />
      </div>
    )
  }
  return (
    <div
      className={cn(
        "break-words whitespace-pre-wrap text-sm",
        whiteBg && "rounded-md bg-white p-3 text-[#1c1c1c]",
      )}
    >
      {message.bodyText || message.body}
    </div>
  )
}
