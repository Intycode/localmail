import * as React from "react"
import {
  BellRing,
  Clock,
  DatabaseBackup,
  Filter,
  FolderOpen,
  KeyRound,
  Keyboard,
  Sparkles,
  Monitor,
  Moon,
  Palette,
  Plus,
  ShieldCheck,
  SlidersHorizontal,
  Sun,
  Trash2,
} from "lucide-react"
import { toast } from "sonner"

import { NOTIFICATIONS_KEY } from "@/lib/notifications"
import { AUTOBACKUP_KEY, autoBackupDue } from "@/lib/autoBackup"
import {
  autoBackupDir,
  autoBackupLastRun,
  getSetting,
  sendUndoSeconds,
  setAutoBackupDir,
  setSendUndoSeconds,
  setSetting,
} from "@/lib/settings"
import { getThemeMode, setThemeMode, type ThemeMode } from "@/lib/theme"
import { dateLocale, getLocale, LOCALES, setLocale, useT, type Locale } from "@/lib/i18n"
import { getMailService } from "@/services/serviceRegistry"
import { isTauri, pgpService } from "@/services/tauriMailService"
import { formatFolderName } from "@/lib/format"
import { AISection } from "./SettingsAI"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Kbd } from "@/components/ui/kbd"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { Account, Folder, Rule } from "@/types"
import { PageBody, PageHeader } from "../PageHeader"

interface ToggleDef {
  key: string
  labelKey: string
  descKey: string
  defaultValue: boolean
  hint?: React.ReactNode
}

/** Native names — a language picker should never be in the other language. */
const LOCALE_LABELS: Record<Locale, string> = { it: "Italiano", en: "English" }

const groups: {
  titleKey: string
  icon: React.ComponentType<{ className?: string }>
  toggles: ToggleDef[]
}[] = [
  {
    titleKey: "settings.general",
    icon: SlidersHorizontal,
    toggles: [
      {
        key: "sync-on-start",
        labelKey: "settings.syncOnStart",
        descKey: "settings.syncOnStartDesc",
        defaultValue: true,
      },
      {
        key: "compact",
        labelKey: "settings.compact",
        descKey: "settings.compactDesc",
        defaultValue: false,
      },
    ],
  },
  {
    titleKey: "settings.notifications",
    icon: BellRing,
    toggles: [
      {
        key: NOTIFICATIONS_KEY,
        labelKey: "settings.systemNotifications",
        descKey: "settings.systemNotificationsDesc",
        defaultValue: true,
      },
    ],
  },
  {
    titleKey: "settings.keyboard",
    icon: Keyboard,
    toggles: [
      {
        key: "keyboard",
        labelKey: "settings.shortcuts",
        descKey: "settings.shortcutsDesc",
        defaultValue: true,
        hint: <KeyboardHint />,
      },
    ],
  },
]

/** Kbd hint for the shortcuts toggle (translated at render time). */
function KeyboardHint(): React.JSX.Element {
  const t = useT()
  return (
    <span className="mt-0.5 flex flex-wrap items-center gap-1 text-xs text-muted-foreground">
      <Kbd>j</Kbd>/<Kbd>k</Kbd> {t("settings.hintNavigate")} · <Kbd>Enter</Kbd>{" "}
      {t("settings.hintOpen")} · <Kbd>a</Kbd> {t("settings.hintArchive")} · <Kbd>r</Kbd>{" "}
      {t("settings.hintCompose")}
    </span>
  )
}

const themeOptions: {
  value: ThemeMode
  labelKey: string
  icon: React.ComponentType<{ className?: string }>
}[] = [
  { value: "system", labelKey: "settings.themeSystem", icon: Monitor },
  { value: "light", labelKey: "settings.themeLight", icon: Sun },
  { value: "dark", labelKey: "settings.themeDark", icon: Moon },
]

/** SettingsPage — persisted toggles + the System/Light/Dark theme picker. */
/** Section order for the side nav; must match the render order below. */
const sectionKeys = [
  ...groups.map((g) => g.titleKey),
  "settings.send",
  "settings.appearance",
  "rules.title",
  "pgp.title",
  "ai.title",
  "backup.title",
  "security.title",
]

function sectionId(titleKey: string): string {
  return `s-${titleKey.replace(".", "-")}`
}

export function SettingsPage() {
  const t = useT()
  return (
    <PageBody>
      <PageHeader title={t("settings.title")} description={t("settings.description")} />
      <div className="md:grid md:grid-cols-[10.5rem_minmax(0,1fr)] md:gap-10">
        <nav aria-label={t("settings.sections")} className="hidden md:block">
          <ul className="sticky top-6 flex flex-col gap-0.5">
            {sectionKeys.map((k) => (
              <li key={k}>
                <button
                  type="button"
                  onClick={() =>
                    document
                      .getElementById(sectionId(k))
                      ?.scrollIntoView({ behavior: "smooth", block: "start" })
                  }
                  className="flex h-8 w-full items-center rounded-md px-2.5 text-left text-[13px] text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
                >
                  {t(k)}
                </button>
              </li>
            ))}
          </ul>
        </nav>
        <div className="flex min-w-0 flex-col gap-8">
          {groups.map((g) => (
            <Section key={g.titleKey} titleKey={g.titleKey} icon={g.icon}>
              {g.toggles.map((tg) => (
                <ToggleRow key={tg.key} def={tg} />
              ))}
            </Section>
          ))}

          <Section titleKey="settings.send" icon={Clock}>
            <SendUndoRow />
          </Section>

          <Section titleKey="settings.appearance" icon={Palette}>
            <ThemeRow />
            <LanguageRow />
          </Section>

          <Section titleKey="rules.title" icon={Filter}>
            <RulesSection />
          </Section>

          <Section titleKey="pgp.title" icon={KeyRound}>
            <PgpSection />
          </Section>

          <Section titleKey="ai.title" icon={Sparkles}>
            <AISection />
          </Section>

          <Section titleKey="backup.title" icon={DatabaseBackup}>
            <ToggleRow
              def={{
                key: AUTOBACKUP_KEY,
                labelKey: "backup.daily",
                descKey: "backup.dailyDesc",
                defaultValue: false,
              }}
            />
            <AutoBackupDirRow />
          </Section>

          <Section titleKey="security.title" icon={ShieldCheck}>
            <div className="flex items-start gap-4 px-4 py-3.5">
              <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="flex items-center gap-2 font-semibold">
                  {t("security.secureStorage")}{" "}
                  <Badge variant="secondary">{t("security.keyringBadge")}</Badge>
                </span>
                <span className="text-[13px] text-muted-foreground">
                  {t("security.keyringDesc")}
                </span>
              </div>
            </div>
          </Section>

          <p className="text-xs text-muted-foreground">{t("settings.autoSaved")}</p>
        </div>
      </div>
    </PageBody>
  )
}

function Section({
  titleKey,
  icon: Icon,
  children,
}: {
  titleKey: string
  icon: React.ComponentType<{ className?: string }>
  children: React.ReactNode
}) {
  const t = useT()
  return (
    <section id={sectionId(titleKey)} className="scroll-mt-6">
      <h2 className="mb-2.5 flex items-center gap-2 text-[15px] font-semibold">
        <Icon className="size-4 text-muted-foreground" />
        {t(titleKey)}
      </h2>
      <div className="divide-y divide-border overflow-hidden rounded-xl border border-border bg-card">
        {children}
      </div>
    </section>
  )
}

/** Undo-send window picker: 0 s = send immediately (no cancel window). */
const undoOptions = [0, 5, 10, 20, 30]

function SendUndoRow() {
  const t = useT()
  const [value, setValue] = React.useState(() => sendUndoSeconds())
  return (
    <div className="flex items-center gap-4 px-4 py-3.5">
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="font-semibold">{t("settings.undoWindow")}</span>
        <span className="text-[13px] text-muted-foreground">
          {t("settings.undoWindowDesc")} {value === 0 ? t("settings.undoWindowOff") : undefined}
        </span>
      </span>
      <Select
        value={String(value)}
        onValueChange={(v) => {
          const s = Number(v)
          setValue(s)
          setSendUndoSeconds(s)
        }}
      >
        <SelectTrigger className="w-32" aria-label={t("settings.undoWindow")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {undoOptions.map((s) => (
            <SelectItem key={s} value={String(s)}>
              {s === 0 ? t("settings.undoDisabled") : t("settings.seconds", { n: s })}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

/** Backup target folder: picked once via the native dialog, stored locally. */
function AutoBackupDirRow() {
  const t = useT()
  const [dir, setDir] = React.useState(() => autoBackupDir())
  const [picking, setPicking] = React.useState(false)

  async function pick(): Promise<void> {
    if (!isTauri()) {
      toast.error(t("common.desktopOnly"))
      return
    }
    setPicking(true)
    try {
      const { open } = await import("@tauri-apps/plugin-dialog")
      const picked = await open({
        directory: true,
        multiple: false,
        title: t("backup.dialogTitle"),
      })
      if (typeof picked === "string" && picked) {
        setAutoBackupDir(picked)
        setDir(picked)
      }
    } finally {
      setPicking(false)
    }
  }

  const last = autoBackupLastRun()
  return (
    <div className="flex items-center gap-4 px-4 py-3.5">
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="font-semibold">{t("backup.folder")}</span>
        <span className="truncate text-[13px] text-muted-foreground">
          {dir || t("backup.noFolder")}
          {dir &&
            last &&
            ` · ${t("backup.lastRun", { date: new Date(last).toLocaleString(dateLocale()) })}`}
          {dir && autoBackupDue() && ` · ${t("backup.due")}`}
        </span>
      </span>
      <Button variant="outline" size="sm" onClick={() => void pick()} disabled={picking}>
        <FolderOpen data-icon="inline-start" />
        {picking ? "…" : dir ? t("backup.change") : t("backup.choose")}
      </Button>
    </div>
  )
}

function ToggleRow({ def }: { def: ToggleDef }) {
  const t = useT()
  const [checked, setChecked] = React.useState(() => getSetting(def.key, def.defaultValue))

  function onCheckedChange(next: boolean): void {
    setChecked(next)
    setSetting(def.key, next)
  }

  return (
    <label className="flex cursor-pointer items-center gap-4 px-4 py-3.5 hover:bg-accent">
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="font-semibold">{t(def.labelKey)}</span>
        <span className="text-[13px] text-muted-foreground">{t(def.descKey)}</span>
        {def.hint}
      </span>
      <Switch checked={checked} onCheckedChange={onCheckedChange} aria-label={t(def.labelKey)} />
    </label>
  )
}

function ThemeRow(): React.JSX.Element {
  const t = useT()
  const [mode, setMode] = React.useState<ThemeMode>(() => getThemeMode())

  // Keep the local control in sync if the mode changes elsewhere (other tab).
  React.useEffect(() => {
    const onStorage = () => setMode(getThemeMode())
    window.addEventListener("storage", onStorage)
    return () => window.removeEventListener("storage", onStorage)
  }, [])

  function onChange(next: string): void {
    if (!next) return
    const m = next as ThemeMode
    setThemeMode(m) // persist + apply immediately
    setMode(m)
  }

  return (
    <div className="flex flex-wrap items-center gap-4 px-4 py-3.5">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="font-semibold">{t("settings.theme")}</span>
        <span className="text-[13px] text-muted-foreground">{t("settings.themeDesc")}</span>
      </div>
      <ToggleGroup
        type="single"
        variant="outline"
        value={mode}
        onValueChange={onChange}
        aria-label={t("settings.theme")}
      >
        {themeOptions.map((o) => (
          <ToggleGroupItem key={o.value} value={o.value} aria-label={t(o.labelKey)}>
            <o.icon data-icon="inline-start" />
            {t(o.labelKey)}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
    </div>
  )
}

/** LanguageRow — interface language (Italian / English). The choice persists
 *  immediately and re-renders the whole app via useT(); the storage listener
 *  keeps this control in sync with changes made in other tabs. */
function LanguageRow(): React.JSX.Element {
  const t = useT()
  const [locale, setUiLocale] = React.useState<Locale>(() => getLocale())

  React.useEffect(() => {
    const onStorage = () => setUiLocale(getLocale())
    window.addEventListener("storage", onStorage)
    return () => window.removeEventListener("storage", onStorage)
  }, [])

  function onChange(next: string): void {
    if (!next) return
    setLocale(next as Locale) // persist + notify every useT() subscriber
    setUiLocale(next as Locale)
  }

  return (
    <div className="flex flex-wrap items-center gap-4 px-4 py-3.5">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="font-semibold">{t("settings.language")}</span>
        <span className="text-[13px] text-muted-foreground">{t("settings.languageDesc")}</span>
      </div>
      <Select value={locale} onValueChange={onChange}>
        <SelectTrigger className="w-40" aria-label={t("settings.languageLabel")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {LOCALES.map((l) => (
            <SelectItem key={l} value={l}>
              {LOCALE_LABELS[l]}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

// --- Regole (M8) ------------------------------------------------------

const RULE_FIELDS: { value: string; key: string }[] = [
  { value: "from", key: "rules.fieldFrom" },
  { value: "to", key: "rules.fieldTo" },
  { value: "subject", key: "rules.fieldSubject" },
  { value: "body", key: "rules.fieldBody" },
  { value: "has_attachment", key: "rules.fieldAttachments" },
  { value: "spam_score", key: "rules.fieldSpamScore" },
]

const RULE_OPS: { value: string; key: string }[] = [
  { value: "contains", key: "rules.opContains" },
  { value: "equals", key: "rules.opEquals" },
  { value: "starts_with", key: "rules.opStartsWith" },
  { value: "ends_with", key: "rules.opEndsWith" },
  { value: "any", key: "rules.opAny" },
]

const RULE_ACTIONS: { value: string; key: string }[] = [
  { value: "move", key: "rules.actionMove" },
  { value: "mark_read", key: "rules.actionMarkRead" },
  { value: "star", key: "rules.actionStar" },
  { value: "mark_spam", key: "rules.actionMarkSpam" },
]

function optionKey(list: { value: string; key: string }[], v: string): string {
  return list.find((o) => o.value === v)?.key ?? v
}

/** Human description of a rule (list row). Live-translated via useT. */
function useDescribeRule(): (r: Rule, folderName: (id: string) => string) => string {
  const t = useT()
  return (r, folderName) => {
    let condition: string
    if (r.field === "spam_score") {
      condition = t("rules.describeSpamScore", { value: r.value })
    } else if (r.field === "has_attachment") {
      condition = t("rules.describeAttachments")
    } else if (r.op === "any") {
      condition = t("rules.describeAny", { field: t(optionKey(RULE_FIELDS, r.field)) })
    } else {
      condition =
        t(optionKey(RULE_FIELDS, r.field)) +
        " " +
        t(optionKey(RULE_OPS, r.op)) +
        ' "' +
        r.value +
        '"'
    }
    const action =
      r.action === "move"
        ? t("rules.describeMove", {
            folder: folderName(r.target ?? "") || t("rules.folderGeneric"),
          })
        : lowerFirst(t(optionKey(RULE_ACTIONS, r.action)))
    return condition + " → " + action
  }
}

function lowerFirst(s: string): string {
  return s.charAt(0).toLowerCase() + s.slice(1)
}

/** RulesSection — local rules CRUD; the engine applies them at ingest. */
function RulesSection(): React.JSX.Element {
  const t = useT()
  const describe = useDescribeRule()
  const [rules, setRules] = React.useState<Rule[]>([])
  const [accounts, setAccounts] = React.useState<Account[]>([])
  const [folderNames, setFolderNames] = React.useState<Map<string, string>>(new Map())
  const [editing, setEditing] = React.useState(false)

  const load = React.useCallback(async (): Promise<void> => {
    if (!isTauri()) return
    const svc = await getMailService()
    if (!svc.listRules) return
    try {
      const [rs, accs] = await Promise.all([svc.listRules(), svc.getAccounts()])
      setRules(rs)
      setAccounts(accs)
      const map = new Map<string, string>()
      for (const a of accs) {
        const fs = await svc.getFolders(a.id)
        for (const f of fs) map.set(f.id, formatFolderName(f.name))
      }
      setFolderNames(map)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("rules.loadFailed"))
    }
  }, [t])

  React.useEffect(() => {
    void load()
  }, [load])

  async function onToggle(r: Rule, enabled: boolean): Promise<void> {
    const svc = await getMailService()
    if (!svc.saveRule) return
    try {
      await svc.saveRule({ ...r, enabled })
      void load()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("rules.updateFailed"))
    }
  }

  async function onDelete(id: string): Promise<void> {
    const svc = await getMailService()
    if (!svc.deleteRule) return
    try {
      await svc.deleteRule(id)
      toast.success(t("rules.deleted"))
      void load()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("rules.deleteFailed"))
    }
  }

  return (
    <div>
      {rules.length === 0 && !editing && (
        <p className="px-4 py-3.5 text-[13px] text-muted-foreground">{t("rules.empty")}</p>
      )}
      {rules.map((r) => (
        <div key={r.id} className="flex items-center gap-3 px-4 py-3.5">
          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="truncate font-semibold">{r.name}</span>
            <span className="truncate text-[13px] text-muted-foreground">
              {describe(r, (id) => folderNames.get(id) ?? "")}
            </span>
          </span>
          <Switch
            checked={r.enabled}
            onCheckedChange={(v) => void onToggle(r, v)}
            aria-label={t("rules.enableAria", { name: r.name })}
          />
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t("rules.deleteTitle") + " " + r.name}
            title={t("rules.deleteTitle")}
            onClick={() => void onDelete(r.id)}
          >
            <Trash2 />
          </Button>
        </div>
      ))}
      {editing ? (
        <RuleForm
          accounts={accounts}
          onDone={() => {
            setEditing(false)
            void load()
          }}
        />
      ) : (
        <div className="px-4 py-3">
          <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
            <Plus data-icon="inline-start" />
            {t("rules.new")}
          </Button>
        </div>
      )}
    </div>
  )
}

function RuleForm({
  accounts,
  onDone,
}: {
  accounts: Account[]
  onDone: () => void
}): React.JSX.Element {
  const t = useT()
  const [name, setName] = React.useState("")
  const [field, setField] = React.useState("from")
  const [op, setOp] = React.useState("contains")
  const [value, setValue] = React.useState("")
  const [action, setAction] = React.useState("mark_read")
  const [accountId, setAccountId] = React.useState(accounts[0]?.id ?? "")
  const [folders, setFolders] = React.useState<Folder[]>([])
  const [target, setTarget] = React.useState("")

  React.useEffect(() => {
    if (action !== "move" || !accountId) return
    void (async () => {
      const svc = await getMailService()
      setFolders((await svc.getFolders(accountId)) ?? [])
    })()
  }, [action, accountId])

  const needsValue = field !== "has_attachment" && op !== "any"

  function autoName(): string {
    return t(optionKey(RULE_FIELDS, field)) + " → " + t(optionKey(RULE_ACTIONS, action))
  }

  async function submit(e: React.FormEvent): Promise<void> {
    e.preventDefault()
    const svc = await getMailService()
    if (!svc.saveRule) return
    if (needsValue && !value.trim()) {
      toast.error(t("rules.needValue"))
      return
    }
    if (action === "move" && !target) {
      toast.error(t("rules.needTarget"))
      return
    }
    try {
      await svc.saveRule({
        id: "",
        name: name.trim() || autoName(),
        enabled: true,
        position: 0,
        field,
        op: field === "spam_score" ? "at_least" : op,
        value: field === "has_attachment" ? "" : value.trim(),
        action,
        target: action === "move" ? target : "",
      })
      toast.success(t("rules.created"))
      onDone()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("rules.saveFailed"))
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-3 px-4 py-3.5">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t("rules.namePlaceholder")}
          className="h-8 w-full sm:w-56"
          aria-label={t("rules.name")}
        />
        <Select value={field} onValueChange={(v) => setField(v)}>
          <SelectTrigger className="h-8 w-full sm:w-40" aria-label={t("rules.field")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {RULE_FIELDS.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {t(o.key)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {field !== "has_attachment" && field !== "spam_score" && (
          <Select value={op} onValueChange={(v) => setOp(v)}>
            <SelectTrigger className="h-8 w-full sm:w-32" aria-label={t("rules.operator")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {RULE_OPS.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {t(o.key)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        {needsValue && (
          <Input
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder={
              field === "spam_score" ? t("rules.valueSpamPlaceholder") : t("rules.valuePlaceholder")
            }
            className="h-8 w-full sm:w-48"
            aria-label={t("rules.value")}
          />
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Select value={action} onValueChange={(v) => setAction(v)}>
          <SelectTrigger className="h-8 w-full sm:w-52" aria-label={t("rules.action")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {RULE_ACTIONS.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {t(o.key)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {action === "move" && (
          <>
            <Select value={accountId} onValueChange={(v) => setAccountId(v)}>
              <SelectTrigger className="h-8 w-full sm:w-44" aria-label={t("io.pickAccount")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {accounts.map((a) => (
                  <SelectItem key={a.id} value={a.id}>
                    {a.email}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={target} onValueChange={(v) => setTarget(v)}>
              <SelectTrigger className="h-8 w-full sm:w-44" aria-label={t("rules.targetFolder")}>
                <SelectValue placeholder={t("rules.folderPlaceholder")} />
              </SelectTrigger>
              <SelectContent>
                {folders.map((f) => (
                  <SelectItem key={f.id} value={f.id}>
                    {formatFolderName(f.name)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </>
        )}
      </div>
      <div className="flex items-center gap-2">
        <Button type="submit" size="sm">
          {t("rules.save")}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onDone}>
          {t("common.cancel")}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">{t("rules.note")}</p>
    </form>
  )
}

// --- Chiavi PGP (M9) --------------------------------------------------

interface PgpStatus {
  hasKey: boolean
  fingerprint: string
  pubKeys: { email: string; fingerprint: string }[]
}

/** PgpSection — per-account key management: generate (keychain, never
 *  leaves the device), import a private key, export the public half, manage
 *  contact public keys used to encrypt outgoing mail. */
function PgpSection(): React.JSX.Element {
  const t = useT()
  const [accounts, setAccounts] = React.useState<Account[]>([])
  const [selected, setSelected] = React.useState("")
  const [status, setStatus] = React.useState<PgpStatus | null>(null)
  const [busy, setBusy] = React.useState(false)
  const [showImport, setShowImport] = React.useState(false)
  const [armored, setArmored] = React.useState("")
  const [pubEmail, setPubEmail] = React.useState("")
  const [pubArmored, setPubArmored] = React.useState("")

  const load = React.useCallback(async (): Promise<void> => {
    if (!isTauri()) return
    try {
      const svc = await getMailService()
      const accs = await svc.getAccounts()
      setAccounts(accs)
      const active = accs.find((a) => a.isActive) ?? accs[0]
      if (!active) return
      setSelected((prev) => prev || active.id)
      const st = await pgpService.status(selected || active.id)
      setStatus(st)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("pgp.statusFailed"))
    }
  }, [selected, t])

  React.useEffect(() => {
    void load()
  }, [load])

  async function generate(): Promise<void> {
    setBusy(true)
    try {
      const acc = accounts.find((a) => a.id === selected)
      if (!acc) return
      await pgpService.generate(selected, acc.name || acc.email, acc.email)
      toast.success(t("pgp.generated"))
      await load()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("pgp.generateFailed"))
    } finally {
      setBusy(false)
    }
  }

  async function importPrivate(): Promise<void> {
    setBusy(true)
    try {
      await pgpService.importPrivate(selected, armored)
      toast.success(t("pgp.privateImported"))
      setArmored("")
      setShowImport(false)
      await load()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("pgp.importFailed"))
    } finally {
      setBusy(false)
    }
  }

  async function removeKey(): Promise<void> {
    setBusy(true)
    try {
      await pgpService.remove(selected)
      toast.success(t("pgp.privateRemoved"))
      await load()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("pgp.removeFailed"))
    } finally {
      setBusy(false)
    }
  }

  async function copyPublic(): Promise<void> {
    try {
      const { publicKey } = await pgpService.exportPublic(selected)
      await navigator.clipboard.writeText(publicKey)
      toast.success(t("pgp.publicCopied"))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("pgp.exportFailed"))
    }
  }

  async function importPub(): Promise<void> {
    try {
      await pgpService.importPub(pubEmail, pubArmored)
      toast.success(t("pgp.keySavedFor", { email: pubEmail }))
      setPubEmail("")
      setPubArmored("")
      await load()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("pgp.importFailed"))
    }
  }

  async function deletePub(email: string): Promise<void> {
    try {
      await pgpService.deletePub(email)
      await load()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("pgp.deleteFailed"))
    }
  }

  return (
    <div className="flex flex-col">
      <div className="flex items-center gap-4 px-4 py-3.5">
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="font-semibold">
            {status?.hasKey ? t("pgp.hasKey") : t("pgp.noKey")}
            {status?.hasKey && status.fingerprint ? " · " + status.fingerprint.slice(-16) : ""}
          </span>
          <span className="text-[13px] text-muted-foreground">{t("pgp.keyDesc")}</span>
        </span>
        <Select value={selected} onValueChange={(v) => setSelected(v)}>
          <SelectTrigger className="h-8 w-full sm:w-48" aria-label={t("io.pickAccount")}>
            <SelectValue placeholder={t("io.pickAccountPlaceholder")} />
          </SelectTrigger>
          <SelectContent>
            {accounts.map((a) => (
              <SelectItem key={a.id} value={a.id}>
                {a.email}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="flex flex-wrap items-center gap-2 px-4 py-3">
        {!status?.hasKey && (
          <Button size="sm" onClick={() => void generate()} disabled={busy || !selected}>
            <KeyRound data-icon="inline-start" />
            {t("pgp.generate")}
          </Button>
        )}
        {status?.hasKey && (
          <>
            <Button variant="outline" size="sm" onClick={() => void copyPublic()} disabled={busy}>
              <KeyRound data-icon="inline-start" />
              {t("pgp.copyPublic")}
            </Button>
            <Button variant="outline" size="sm" onClick={() => setShowImport(!showImport)}>
              {t("pgp.importPrivate")}
            </Button>
            <Button variant="ghost" size="sm" onClick={() => void removeKey()} disabled={busy}>
              {t("common.remove")}
            </Button>
          </>
        )}
      </div>
      {showImport && (
        <div className="flex flex-col gap-2 px-4 pb-3">
          <textarea
            value={armored}
            onChange={(e) => setArmored(e.target.value)}
            placeholder={t("pgp.privatePlaceholder")}
            className="min-h-24 rounded-md border border-border bg-background p-2 font-mono text-xs"
            aria-label={t("pgp.privatePlaceholder")}
          />
          <Button size="sm" onClick={() => void importPrivate()} disabled={busy || !armored.trim()}>
            {t("pgp.importToKeychain")}
          </Button>
        </div>
      )}
      <div className="px-4 py-3">
        <p className="mb-2 text-[11px] font-semibold tracking-[0.06em] text-muted-foreground uppercase">
          {t("pgp.contactKeys")}
        </p>
        {status?.pubKeys.map((k) => (
          <div key={k.email} className="flex items-center gap-3 py-1.5">
            <span className="min-w-0 flex-1 truncate text-[13px]">{k.email}</span>
            <span className="font-mono text-[11px] text-muted-foreground">
              {k.fingerprint.slice(-16)}
            </span>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t("pgp.removeKeyOf", { email: k.email })}
              onClick={() => void deletePub(k.email)}
            >
              <Trash2 />
            </Button>
          </div>
        ))}
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Input
            value={pubEmail}
            onChange={(e) => setPubEmail(e.target.value)}
            placeholder="email@contatto"
            className="h-8 w-full sm:w-48"
            aria-label={t("pgp.contactEmail")}
          />
          <Input
            value={pubArmored}
            onChange={(e) => setPubArmored(e.target.value)}
            placeholder={t("pgp.contactArmored")}
            className="h-8 w-full sm:w-72"
            aria-label={t("pgp.contactArmored")}
          />
          <Button
            variant="outline"
            size="sm"
            onClick={() => void importPub()}
            disabled={!pubEmail.trim() || !pubArmored.trim()}
          >
            {t("pgp.saveKey")}
          </Button>
        </div>
      </div>
    </div>
  )
}
