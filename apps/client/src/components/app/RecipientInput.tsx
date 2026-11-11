import * as React from "react"

import type { Contact } from "@/types"
import { getMailService } from "@/services/serviceRegistry"
import { applySuggestion, lastEntry } from "@/lib/recipientSuggest"
import { cn } from "@/lib/utils"
import { Input } from "@/components/ui/input"

interface RecipientInputProps extends Omit<
  React.ComponentProps<typeof Input>,
  "value" | "onChange"
> {
  value: string
  onChange: (value: string) => void
}

/**
 * RecipientInput — comma-separated addresses with autocomplete from the local
 * mail history (the engine matches the entry after the last comma). Arrows
 * move, Enter/Tab pick, Esc closes.
 */
export function RecipientInput({ value, onChange, ...props }: RecipientInputProps) {
  const [items, setItems] = React.useState<Contact[]>([])
  const [active, setActive] = React.useState(0)
  const [open, setOpen] = React.useState(false)
  const listId = React.useId()

  React.useEffect(() => {
    if (!lastEntry(value)) {
      setItems([])
      return
    }
    let alive = true
    const timer = setTimeout(() => {
      void (async () => {
        try {
          const svc = await getMailService()
          const res = (await svc.suggestContacts?.(value)) ?? []
          if (!alive) return
          setItems(res)
          setActive(0)
        } catch {
          // suggestions are best-effort: never disturb typing
        }
      })()
    }, 150)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [value])

  const show = open && items.length > 0

  function pick(c: Contact): void {
    onChange(applySuggestion(value, c))
    setOpen(false)
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>): void {
    if (!show) return
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault()
      const step = e.key === "ArrowDown" ? 1 : -1
      setActive((active + step + items.length) % items.length)
    } else if (e.key === "Enter" || e.key === "Tab") {
      e.preventDefault()
      pick(items[active]!)
    } else if (e.key === "Escape") {
      e.preventDefault()
      setOpen(false)
    }
  }

  return (
    <div className="relative min-w-0 flex-1">
      <Input
        {...props}
        value={value}
        onChange={(e) => {
          onChange(e.target.value)
          setOpen(true)
        }}
        onKeyDown={onKeyDown}
        onBlur={() => setOpen(false)}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={show}
        aria-controls={listId}
        aria-activedescendant={show ? `${listId}-${active}` : undefined}
      />
      {show && (
        <ul
          id={listId}
          role="listbox"
          className="absolute top-full left-0 z-50 w-full max-w-md rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-md"
        >
          {items.map((c, i) => (
            <li
              key={c.email}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              // mousedown, not click: the input must keep the focus
              onMouseDown={(e) => {
                e.preventDefault()
                pick(c)
              }}
              onMouseEnter={() => setActive(i)}
              className={cn(
                "flex cursor-pointer items-baseline gap-2 rounded-md px-2 py-1.5 text-sm",
                i === active && "bg-accent text-accent-foreground",
              )}
            >
              {c.name && c.name !== c.email && <span className="truncate">{c.name}</span>}
              <span className="truncate font-mono text-xs text-muted-foreground">{c.email}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
