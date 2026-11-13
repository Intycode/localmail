import * as React from "react"
import { Search } from "lucide-react"

import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useT } from "@/lib/i18n"
import { getMailService } from "@/services/serviceRegistry"
import type { SearchResult } from "@/types"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { HighlightedSnippet } from "@/components/app/pages/SearchPage"

const MAX_SUGGESTIONS = 8

/**
 * SearchBox — inline search with live suggestions (keyword mode, all
 * accounts). Typing queries the engine; ↑/↓ move through the suggestions,
 * Enter or a click opens the thread via onPick, Esc closes the list.
 */
export function SearchBox({
  onPick,
  className,
}: {
  onPick: (threadId: string) => void
  className?: string
}) {
  const t = useT()
  const [query, setQuery] = React.useState("")
  const [hits, setHits] = React.useState<SearchResult[] | null>(null)
  const [open, setOpen] = React.useState(false)
  const [active, setActive] = React.useState(0)
  const root = React.useRef<HTMLDivElement>(null)
  const listId = React.useId()

  React.useEffect(() => {
    const q = query.trim()
    if (!q) {
      setHits(null)
      return
    }
    let alive = true
    const id = window.setTimeout(async () => {
      try {
        const res = await (await getMailService()).search({ query: q })
        if (alive) {
          setHits(res.slice(0, MAX_SUGGESTIONS))
          setActive(0)
        }
      } catch (e: unknown) {
        console.error("search", e)
        if (alive) setHits([])
      }
    }, 150)
    return () => {
      alive = false
      window.clearTimeout(id)
    }
  }, [query])

  // close when focus or a click leaves the box
  React.useEffect(() => {
    if (!open) return
    const away = (e: Event) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener("pointerdown", away)
    return () => document.removeEventListener("pointerdown", away)
  }, [open])

  function pick(h: SearchResult): void {
    setOpen(false)
    onPick(h.threadId)
  }

  function onKeyDown(e: React.KeyboardEvent): void {
    if (e.key === "Escape") {
      if (open) e.stopPropagation()
      setOpen(false)
    } else if (!hits?.length) {
      return
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault()
      setOpen(true)
      setActive((i) => (i + (e.key === "ArrowDown" ? 1 : -1) + hits.length) % hits.length)
    } else if (e.key === "Enter" && open) {
      e.preventDefault()
      pick(hits[active])
    }
  }

  const showList = open && hits !== null
  return (
    <div ref={root} className={cn("relative", className)}>
      <InputGroup className="h-8">
        <InputGroupInput
          type="search"
          role="combobox"
          aria-expanded={showList}
          aria-controls={listId}
          aria-activedescendant={showList && hits.length ? `${listId}-${active}` : undefined}
          aria-autocomplete="list"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setOpen(true)
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={onKeyDown}
          placeholder={t("search.boxPlaceholder")}
          aria-label={t("search.title")}
        />
        <InputGroupAddon align="inline-start">
          <Search />
        </InputGroupAddon>
      </InputGroup>
      {showList && (
        <ul
          id={listId}
          role="listbox"
          aria-label={t("search.title")}
          className="absolute inset-x-0 top-full z-30 mt-1 max-h-[min(24rem,70dvh)] overflow-y-auto rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg"
        >
          {hits.length === 0 ? (
            <li role="status" className="px-3 py-4 text-center text-sm text-muted-foreground">
              {t("search.noResults", { query: query.trim() })}
            </li>
          ) : (
            hits.map((h, i) => (
              <li
                key={`${h.threadId}-${h.date}`}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === active}
                onMouseEnter={() => setActive(i)}
                onClick={() => pick(h)}
                className={cn(
                  "flex cursor-pointer flex-col gap-0.5 rounded-md px-3 py-2",
                  i === active && "bg-accent",
                )}
              >
                <span className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-sm font-medium">{h.subject}</span>
                  <time className="shrink-0 text-[11px] text-muted-foreground tabular-nums">
                    {formatDate(h.date)}
                  </time>
                </span>
                <span className="truncate text-xs text-muted-foreground">
                  <HighlightedSnippet text={h.snippet} />
                </span>
              </li>
            ))
          )}
        </ul>
      )}
    </div>
  )
}
