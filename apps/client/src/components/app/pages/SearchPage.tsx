import * as React from "react"
import { Search, Sparkles } from "lucide-react"

import { formatDate } from "@/lib/format"
import { useT } from "@/lib/i18n"
import { getMailService } from "@/services/serviceRegistry"
import { isTauri, semanticSearchService } from "@/services/tauriMailService"
import type { SearchResult } from "@/types"
import { ThreadContextMenu } from "@/components/app/ThreadContextMenu"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
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
function HighlightedSnippet({ text }: { text: string }) {
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
          <mark key={key++} className="bg-transparent font-semibold text-foreground">
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
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="border-b border-border px-4 py-3">
        <div className="mx-auto w-full max-w-xl">
          <InputGroup className="h-9">
            <InputGroupInput
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("search.placeholder")}
              autoFocus
            />
            <InputGroupAddon align="inline-start">
              <Search />
            </InputGroupAddon>
          </InputGroup>
          {semanticReady && (
            <div className="mt-2 flex items-center gap-1.5">
              <Button
                variant={mode === "keyword" ? "default" : "outline"}
                size="xs"
                onClick={() => setMode("keyword")}
              >
                {t("search.keyword")}
              </Button>
              <Button
                variant={mode === "semantic" ? "default" : "outline"}
                size="xs"
                onClick={() => setMode("semantic")}
              >
                <Sparkles data-icon="inline-start" />
                {t("search.semantic")}
              </Button>
            </div>
          )}
        </div>
      </div>

      {results === null ? (
        <SearchHint>{t("search.hint")}</SearchHint>
      ) : results.length === 0 ? (
        <SearchHint>{t("search.noResults", { query: query.trim() })}</SearchHint>
      ) : (
        <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto">
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
                className="grid grid-cols-[1fr_auto] items-start gap-4 border-b border-border px-4 py-3 outline-none hover:bg-accent focus-visible:bg-accent focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
              >
                <span className="min-w-0">
                  <span className="block truncate font-medium">{h.subject}</span>
                  <span className="block truncate text-[13px] text-muted-foreground">
                    <HighlightedSnippet text={h.snippet} />
                  </span>
                </span>
                <span className="flex shrink-0 flex-col items-end gap-1">
                  <time className="text-xs tabular-nums text-muted-foreground">
                    {formatDate(h.date)}
                  </time>
                  <Badge
                    variant="outline"
                    className="font-mono text-[10px] font-normal text-muted-foreground"
                  >
                    {h.matchedOn in matchedOnKeys ? t(matchedOnKeys[h.matchedOn]) : h.matchedOn}
                  </Badge>
                </span>
              </a>
            </ThreadContextMenu>
          ))}
        </div>
      )}
    </div>
  )
}

function SearchHint({ children }: { children: React.ReactNode }) {
  const t = useT()
  return (
    <div className="flex flex-1 items-center justify-center p-8">
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <Search />
          </EmptyMedia>
          <EmptyTitle>{t("search.title")}</EmptyTitle>
          <EmptyDescription>{children}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    </div>
  )
}
