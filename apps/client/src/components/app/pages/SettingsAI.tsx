import * as React from "react"
import { Loader2, PlugZap, Sparkles } from "lucide-react"
import { toast } from "sonner"

import {
  aiService,
  isTauri,
  semanticSearchService,
  type AIStatus,
} from "@/services/tauriMailService"
import { useT } from "@/lib/i18n"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

interface SearchStatus {
  ollama: boolean
  model: string
  indexed: number
  total: number
}

/** Loopback hosts accepted without the explicit remote opt-in (the engine
 *  enforces the same list — this copy only drives the UI affordance). */
function isLoopbackEndpoint(url: string): boolean {
  try {
    const host = new URL(url.includes("://") ? url : "http://" + url).hostname
    return ["127.0.0.1", "localhost", "::1"].includes(host)
  } catch {
    return true // unparsable input: keep the toggle hidden, engine will judge
  }
}

/** AISection — Impostazioni → AI locale (M12): the user's own local server
 *  (Ollama natively, or any OpenAI-compatible server like LM Studio).
 *  Endpoint + kind + models live in the engine (app_config); everything
 *  degrades gracefully when the server is off. */
export function AISection(): React.JSX.Element {
  const t = useT()
  const [st, setSt] = React.useState<AIStatus | null>(null)
  const [search, setSearch] = React.useState<SearchStatus | null>(null)
  const [url, setUrl] = React.useState("")
  const [kind, setKind] = React.useState<"ollama" | "openai">("ollama")
  const [allowRemote, setAllowRemote] = React.useState(false)
  const [models, setModels] = React.useState<string[] | null>(null)
  const [chatModel, setChatModel] = React.useState("")
  const [embedModel, setEmbedModel] = React.useState("")
  const [probing, setProbing] = React.useState(false)
  const [indexing, setIndexing] = React.useState(false)

  const load = React.useCallback(async (): Promise<AIStatus | null> => {
    try {
      const s = await aiService.status()
      setSt(s)
      setUrl(s.url)
      setKind(s.kind)
      setAllowRemote(s.allowRemote)
      setChatModel(s.chatModel)
      setEmbedModel(s.embedModel)
      try {
        setSearch(await semanticSearchService.status())
      } catch {
        setSearch(null)
      }
      return s
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("ai.statusFailed"))
      return null
    }
  }, [])

  React.useEffect(() => {
    if (!isTauri()) return
    void load()
  }, [load])

  /** Saves endpoint+kind+consent, then probes and loads the model list. */
  async function saveAndProbe(): Promise<void> {
    setProbing(true)
    try {
      const s = await aiService.config({ url: url.trim(), kind, allowRemote })
      setSt(s)
      setUrl(s.url)
      const { models: list } = await aiService.models()
      setModels(list)
      toast.success(list.length > 0 ? t("ai.probeOk", { n: list.length }) : t("ai.probeNoModels"))
    } catch (err: unknown) {
      setModels(null)
      toast.error(err instanceof Error ? err.message : t("ai.probeFailed"))
      await load()
    } finally {
      setProbing(false)
    }
  }

  async function saveAllowRemote(next: boolean): Promise<void> {
    setAllowRemote(next)
    try {
      const s = await aiService.config({ allowRemote: next })
      setSt(s)
    } catch (err: unknown) {
      setAllowRemote(!next)
      toast.error(err instanceof Error ? err.message : t("ai.saveFailed"))
    }
  }

  async function saveChatModel(model: string): Promise<void> {
    setChatModel(model)
    try {
      await aiService.config({ chatModel: model })
      toast.success(t("ai.chatModelSaved"))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : "Salvataggio non riuscito")
    }
  }

  async function saveEmbedModel(model: string): Promise<void> {
    setEmbedModel(model)
    try {
      await aiService.config({ embedModel: model })
      const s = await load()
      toast.success(
        t("ai.embedModelSaved") +
          (s && s.available && search && search.total > 0 ? t("ai.embedModelSavedIndex") : ""),
      )
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : "Salvataggio non riuscito")
    }
  }

  async function reindexAll(): Promise<void> {
    setIndexing(true)
    try {
      let done = 0
      for (let round = 0; round < 100; round++) {
        const res = await semanticSearchService.reindex(200)
        done += res.indexed
        if (res.remaining === 0 || res.indexed === 0) break
      }
      toast.success(t("ai.indexed", { n: done }))
      await load()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("ai.indexFailed"))
      await load()
    } finally {
      setIndexing(false)
    }
  }

  if (!isTauri()) {
    return <p className="px-4 py-3 text-sm text-muted-foreground">{t("ai.appOnly")}</p>
  }

  const available = st?.available ?? false
  const nonLocal = url.trim() !== "" && !isLoopbackEndpoint(url.trim())
  const pct =
    search && search.total > 0
      ? Math.min(100, Math.round((search.indexed / search.total) * 100))
      : 0
  // the pickers must always offer the currently configured model
  const chatOptions =
    models && chatModel && !models.includes(chatModel) ? [chatModel, ...models] : models
  const embedOptions = embedOptionsFor(models, embedModel)

  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-center gap-2 px-4 py-3">
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex items-center gap-2 font-semibold">
            {t("ai.server")}
            <Badge variant={available ? "secondary" : "outline"} className="h-5 px-1.5 text-[11px]">
              {available ? t("ai.connected") : t("ai.notDetected")}
            </Badge>
          </span>
          <span className="text-[13px] text-muted-foreground">
            {st?.error ? st.error : available ? t("ai.availableDesc") : t("ai.unavailableDesc")}
          </span>
        </span>
        {available && search && search.total > 0 && (
          <Button variant="outline" size="sm" onClick={() => void reindexAll()} disabled={indexing}>
            {indexing ? (
              <Loader2 data-icon="inline-start" className="animate-spin" />
            ) : (
              <Sparkles data-icon="inline-start" />
            )}
            {indexing ? t("ai.indexing") : t("ai.indexNow")}
          </Button>
        )}
      </div>

      <div className="flex flex-col gap-2 border-t px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <Select value={kind} onValueChange={(v) => setKind(v as "ollama" | "openai")}>
            <SelectTrigger className="h-8 w-full sm:w-52" aria-label={t("ai.serverType")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="ollama">Ollama</SelectItem>
              <SelectItem value="openai">{t("ai.openaiCompatible")}</SelectItem>
            </SelectContent>
          </Select>
          <Input
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            className="h-8 w-full sm:w-72"
            placeholder={kind === "ollama" ? "http://127.0.0.1:11434" : "http://127.0.0.1:1234"}
            aria-label={t("ai.endpoint")}
          />
          <Button
            variant="outline"
            size="sm"
            onClick={() => void saveAndProbe()}
            disabled={probing || !url.trim()}
          >
            {probing ? (
              <Loader2 data-icon="inline-start" className="animate-spin" />
            ) : (
              <PlugZap data-icon="inline-start" />
            )}
            {probing ? t("ai.probing") : t("ai.probe")}
          </Button>
        </div>
        {nonLocal && (
          <label className="flex cursor-pointer items-center gap-3 text-[13px]">
            <Switch
              checked={allowRemote}
              onCheckedChange={(v) => void saveAllowRemote(v)}
              aria-label={t("ai.allowRemote")}
            />
            <span className="text-muted-foreground">
              {t("ai.allowRemoteDesc", { url: url.trim() })}
            </span>
          </label>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2 border-t px-4 py-3">
        <span className="text-[13px] text-muted-foreground">{t("ai.chatModel")}</span>
        {chatOptions ? (
          <Select value={chatModel} onValueChange={(v) => void saveChatModel(v)}>
            <SelectTrigger className="h-8 w-full sm:w-64" aria-label={t("ai.chatModel")}>
              <SelectValue placeholder={t("ai.pickModel")} />
            </SelectTrigger>
            <SelectContent>
              {chatOptions.map((m) => (
                <SelectItem key={m} value={m}>
                  {m}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Input
            value={chatModel}
            onChange={(e) => setChatModel(e.target.value)}
            onBlur={() =>
              chatModel.trim() !== st?.chatModel && void saveChatModel(chatModel.trim())
            }
            className="h-8 w-full sm:w-64"
            placeholder="es. llama3.2:latest"
            aria-label={t("ai.chatModel")}
          />
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2 border-t px-4 py-3">
        <span className="text-[13px] text-muted-foreground">{t("ai.embedModel")}</span>
        {embedOptions ? (
          <Select value={embedModel} onValueChange={(v) => void saveEmbedModel(v)}>
            <SelectTrigger className="h-8 w-full sm:w-64" aria-label={t("ai.embedModel")}>
              <SelectValue placeholder={t("ai.pickModel")} />
            </SelectTrigger>
            <SelectContent>
              {embedOptions.map((m) => (
                <SelectItem key={m} value={m}>
                  {m}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <Input
            value={embedModel}
            onChange={(e) => setEmbedModel(e.target.value)}
            onBlur={() =>
              embedModel.trim() !== st?.embedModel && void saveEmbedModel(embedModel.trim())
            }
            className="h-8 w-full sm:w-64"
            placeholder="nomic-embed-text"
            aria-label={t("ai.embedModelAria")}
          />
        )}
      </div>

      {available && search && (
        <p className="border-t px-4 pb-3 pt-2 text-xs text-muted-foreground">
          {t("ai.indexProgress", { indexed: search.indexed, total: search.total, pct })}
        </p>
      )}
    </div>
  )
}

/** Embed picker options: server list plus the currently configured model. */
function embedOptionsFor(models: string[] | null, current: string): string[] | null {
  if (!models) return null
  return current && !models.includes(current) ? [current, ...models] : models
}
