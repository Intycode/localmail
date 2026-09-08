import * as React from "react"
import { Check, Copy } from "lucide-react"

import { useT } from "@/lib/i18n"
import { Button } from "@/components/ui/button"

type CopyButtonProps = Omit<
  React.ComponentProps<typeof Button>,
  "onClick" | "children" | "value" | "asChild"
> & {
  /** Text placed on the clipboard. */
  value: string
  /** Accessible name and native tooltip, e.g. "Copia indirizzo email". */
  label: string
  /** How long the confirm state (check icon) stays visible. */
  confirmMs?: number
}

/**
 * CopyButton — quiet ghost icon button that copies `value` to the clipboard.
 * Feedback is the icon swap Copy → Check (with toast-free, local semantics):
 * the button is meant to sit inline next to emails and message text without
 * adding visual noise. Stops propagation so it is safe inside clickable rows.
 */
export function CopyButton({
  value,
  label,
  confirmMs = 1600,
  className,
  size = "icon-sm",
  ...props
}: CopyButtonProps) {
  const t = useT()
  const [copied, setCopied] = React.useState(false)
  const timer = React.useRef<number | undefined>(undefined)

  React.useEffect(() => () => window.clearTimeout(timer.current), [])

  async function copy(): Promise<void> {
    const ok = await writeClipboard(value)
    if (!ok) return
    setCopied(true)
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setCopied(false), confirmMs)
  }

  return (
    <Button
      type="button"
      variant="ghost"
      size={size}
      className={className}
      onClick={(e) => {
        e.preventDefault()
        e.stopPropagation()
        void copy()
      }}
      aria-label={copied ? t("copy.copied") : label}
      title={copied ? t("copy.copied") : label}
      data-copied={copied || undefined}
      {...props}
    >
      {copied ? <Check className="text-primary" /> : <Copy className="text-muted-foreground" />}
    </Button>
  )
}

/** navigator.clipboard when available, with an execCommand fallback. */
async function writeClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through to the legacy path (permissions, non-secure context…)
  }
  try {
    const ta = document.createElement("textarea")
    ta.value = text
    ta.setAttribute("readonly", "")
    ta.style.position = "fixed"
    ta.style.opacity = "0"
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand("copy")
    ta.remove()
    return ok
  } catch {
    return false
  }
}
