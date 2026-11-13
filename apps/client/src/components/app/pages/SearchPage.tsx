import * as React from "react"
import { Search, Sparkles } from "lucide-react"

import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useT } from "@/lib/i18n"
import { getMailService } from "@/services/serviceRegistry"
import { isTauri, semanticSearchService } from "@/services/tauriMailService"
import type { SearchResult } from "@/types"
import { ThreadContextMenu } from "@/components/app/ThreadContextMenu"
import { Badge } from "@/components/ui/badge"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"

const matchedOnKeys: Record<SearchResult["matchedOn"], string> = {
  subject: "search.matchSubject",
  sender: "search.matchSender",
  recipient: "search.matchRecipient",
  body: "search.matchBody",
  semantic: "search.matchSemantic",
}

/** HighlightedSnippet renders the engine's FTS5 snippet, turning the [[ ]]
 *  markers around matched terms into a highlighted <mark>. */
export function HighlightedSnippet({ text }: { text: string }) {
  const parts = text.split(/(\[\[|\]\])/g)
  const out: React.ReactNode[] = []
  let bold = false
  let key = 0
  for (const part of parts) {
    if (part === "[[") {
      bold = true
    } else if (part === "]]") {
      bold = false
    } else if (part !== "") {
      out.push(
        bold ? (
          <mark
            key={key++}
            className="rounded-sm bg-amber-200/70 px-0.5 text-foreground dark:bg-amber-400/25"
          >
            {part}
          </mark>
        ) : (
          <React.Fragment key={key++}>{part}</React.Fragment>
        ),
      )
    }
  }
  return <>{out}</>
}

/** SearchPage — client-side search. Each keystroke queries the engine.
 *  Semantic mode (M10) appears only when a local Ollama has embeddings. */
export function SearchPage() {
  const t = useT()
  const [query, setQuery] = React.useState("")
  const [results, setResults] = React.useState<SearchResult[] | null>(null)
  const [mode, setMode] = React.useState<"keyword" | "semantic">("keyword")
  const [semanticReady, setSemanticReady] = React.useState(false)

  React.useEffect(() => {
    if (!isTauri()) return
    void (async () => {
      try {
        const st = await semanticSearchService.status()
        setSemanticReady(st.ollama && st.indexed > 0)
      } catch {
        setSemanticReady(false)
      }
    })()
  }, [])

  React.useEffect(() => {
    const q = query.trim()
    if (!q) {
      setResults(null)
      return
    }
    let alive = true
    const id = window.setTimeout(async () => {
      const svc = await getMailService()
      try {
        const hits = await svc.search({ query: q, mode })
        if (alive) setResults(hits)
      } catch (e: unknown) {
        console.error("search", e)
        if (alive) setResults([])
      }
    }, 150)
    return () => {
      alive = false
      window.clearTimeout(id)
    }
  }, [query, mode])

  return (
    <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto">
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-5 px-5 pt-8 pb-10">
        <h1 className="text-xl font-semibold tracking-tight">{t("search.title")}</h1>
        <div className="flex flex-col gap-3">
          <InputGroup className="h-12 rounded-xl">
            <InputGroupInput
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("search.placeholder")}
              aria-label={t("search.title")}
              className="text-base"
              autoFocus
            />
            <InputGroupAddon align="inline-start">
              <Search />
            </InputGroupAddon>
          </InputGroup>
          {(semanticReady || results !== null) && (
            <div className="flex flex-wrap items-center gap-3">
              {semanticReady && (
                <div role="group" className="inline-flex rounded-lg bg-muted p-1">
                  {(["keyword", "semantic"] as const).map((m) => (
                    <button
                      key={m}
                      type="button"
                      aria-pressed={mode === m}
                      onClick={() => setMode(m)}
                      className={cn(
                        "inline-flex h-7 items-center gap-1.5 rounded-md px-3 text-[13px] text-muted-foreground transition-colors focus-visible:outline-2 focus-visible:outline-ring",
                        mode === m && "bg-background font-medium text-foreground shadow-sm",
                      )}
                    >
                      {m === "semantic" && <Sparkles className="size-3.5" />}
                      {m === "keyword" ? t("search.keyword") : t("search.semantic")}
                    </button>
                  ))}
                </div>
              )}
              {results !== null && results.length > 0 && (
                <span className="text-[13px] text-muted-foreground">
                  {t("search.resultCount", { n: results.length })}
                </span>
              )}
            </div>
          )}
        </div>

        {results === null ? null : results.length === 0 ? (
          <p role="status" className="py-12 text-center text-sm text-muted-foreground">
            {t("search.noResults", { query: query.trim() })}
          </p>
        ) : (
          <div className="border-t border-border">
            {results.map((h) => (
              <ThreadContextMenu
                key={`${h.threadId}-${h.date}`}
                thread={null}
                threadId={h.threadId}
                onRemoved={() =>
                  setResults((prev) =>
                    prev === null
                      ? prev
                      : prev.filter((x) => x.threadId !== h.threadId || x.date !== h.date),
                  )
                }
              >
                <a
                  href={`/thread?id=${encodeURIComponent(h.threadId)}`}
                  data-thread-row
                  className="flex flex-col gap-1 border-b border-border px-2 py-3.5 outline-none hover:bg-accent/60 focus-visible:bg-accent focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
                >
                  <span className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate font-medium">{h.subject}</span>
                    <Badge
                      variant="outline"
                      className="font-mono text-[10px] font-normal text-muted-foreground"
                    >
                      {h.matchedOn in matchedOnKeys ? t(matchedOnKeys[h.matchedOn]) : h.matchedOn}
                    </Badge>
                    <time className="shrink-0 text-xs text-muted-foreground tabular-nums">
                      {formatDate(h.date)}
                    </time>
                  </span>
                  <span className="truncate text-[13px] text-muted-foreground">
                    <HighlightedSnippet text={h.snippet} />
                  </span>
                </a>
              </ThreadContextMenu>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
