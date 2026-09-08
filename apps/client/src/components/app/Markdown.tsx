import * as React from "react"
import ReactMarkdown, { type Components } from "react-markdown"
import remarkGfm from "remark-gfm"

import { cn } from "@/lib/utils"

/**
 * Markdown — the single renderer for LLM output in the app (thread summary
 * today, any future AI response surface). react-markdown escapes raw HTML by
 * default, so model output can never inject markup; GFM (remark-gfm) adds
 * tables, strikethrough and task lists the local models like to emit.
 *
 * Styling is element-mapped with semantic tokens instead of the typography
 * plugin, so it inherits the surrounding text size/color (bubble, card…).
 *
 * Links are intentionally rendered as non-interactive text: a click would
 * navigate the whole Tauri webview away from the app; the URL stays visible
 * as the title tooltip for transparency.
 */

const components: Components = {
  a: ({ href, children }) => (
    <span title={href} className="underline decoration-dotted underline-offset-2">
      {children}
    </span>
  ),
}

export function Markdown({
  content,
  className,
}: {
  content: string
  className?: string
}): React.JSX.Element {
  return (
    <div
      className={cn(
        // vertical rhythm between markdown blocks, zeroed at the edges so the
        // container (bubble padding) owns the outer spacing
        "[&>*:not(:first-child)]:mt-2 [&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-3 [&_code]:rounded [&_code]:bg-muted-foreground/10 [&_code]:px-1 [&_code]:py-0.5 [&_code]:text-[0.9em] [&_h1]:text-sm [&_h1]:font-semibold [&_h2]:text-sm [&_h2]:font-semibold [&_h3]:font-semibold [&_h3]:text-[13px] [&_hr]:border-border [&_li]:my-0.5 [&_li]:marker:text-muted-foreground [&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-5 [&_strong]:font-semibold [&_table]:text-xs [&_td]:border-border [&_td]:border-t [&_td]:px-2 [&_td]:py-1 [&_th]:border-b [&_th]:border-border [&_th]:px-2 [&_th]:py-1 [&_th]:text-left [&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-5 [&>p]:my-1 text-inherit",
        className,
      )}
    >
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {content}
      </ReactMarkdown>
    </div>
  )
}
