import * as React from "react"
import { Archive, ArchiveRestore, LogIn, LogOut } from "lucide-react"
import { toast } from "sonner"

import { getMailService } from "@/services/serviceRegistry"
import { formatFolderName } from "@/lib/format"
import { useT } from "@/lib/i18n"
import { ioService, isTauri, pickPath } from "@/services/tauriMailService"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { Account, Folder } from "@/types"

/** AccountsIo — import/export card on the accounts page (M11): folder export
 *  as mbox, message import (.eml/.mbox) into the local-only Importate folder,
 *  encrypted-DB backup and restore. The account picker targets the import
 *  destination; export uses the folder picker. */
export function AccountsIo({ accounts }: { accounts: Account[] }): React.JSX.Element {
  const t = useT()
  const [folders, setFolders] = React.useState<Folder[]>([])
  const [accountId, setAccountId] = React.useState("")
  const [folderId, setFolderId] = React.useState("")
  const [busy, setBusy] = React.useState(false)

  React.useEffect(() => {
    const active = accounts.find((a) => a.isActive) ?? accounts[0]
    if (active) setAccountId((cur) => cur || active.id)
  }, [accounts])

  React.useEffect(() => {
    if (!accountId || !isTauri()) return
    void (async () => {
      try {
        const svc = await getMailService()
        setFolders((await svc.getFolders(accountId)) ?? [])
        setFolderId("")
      } catch {
        setFolders([])
      }
    })()
  }, [accountId])

  async function exportFolder(): Promise<void> {
    if (!folderId) {
      toast.error(t("io.pickExportFolder"))
      return
    }
    setBusy(true)
    try {
      const [dest] = await pickPath("save", {
        defaultPath: "archivio.mbox",
        filters: [{ name: "mbox", extensions: ["mbox"] }],
      })
      if (!dest) return
      const res = await ioService.exportMailbox(folderId, dest)
      toast.success(
        t("io.exported", {
          count: res.exported,
          failed: res.failed > 0 ? t("io.exportFailed", { n: res.failed }) : "",
        }),
      )
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("io.exportError"))
    } finally {
      setBusy(false)
    }
  }

  async function importFile(): Promise<void> {
    if (!accountId) {
      toast.error(t("io.pickImportAccount"))
      return
    }
    setBusy(true)
    try {
      const [src] = await pickPath("open", {
        filters: [{ name: "Email", extensions: ["mbox", "eml"] }],
      })
      if (!src) return
      const res = await ioService.importMails(accountId, src)
      toast.success(
        t("io.imported", {
          count: res.imported,
          skipped: res.skipped > 0 ? t("io.importSkipped", { n: res.skipped }) : "",
        }),
      )
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("io.importError"))
    } finally {
      setBusy(false)
    }
  }

  async function backup(): Promise<void> {
    setBusy(true)
    try {
      const { appDataDir } = await import("@tauri-apps/api/path")
      // default to the app-data dir: the app always has full access there, so
      // neither backup nor restore can hit macOS TCC folder restrictions.
      const dir = await appDataDir()
      const [dest] = await pickPath("save", {
        defaultPath: dir + "/mail-backup.db",
        filters: [{ name: "SQLite", extensions: ["db"] }],
      })
      if (!dest) return
      await ioService.backupCreate(dest)
      toast.success(t("io.backupCreated"))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("io.backupError"))
    } finally {
      setBusy(false)
    }
  }

  async function restore(): Promise<void> {
    setBusy(true)
    try {
      const [{ ask }, { appDataDir }] = await Promise.all([
        import("@tauri-apps/plugin-dialog"),
        import("@tauri-apps/api/path"),
      ])
      const dir = await appDataDir()
      const [src] = await pickPath("open", {
        defaultPath: dir,
        filters: [{ name: "SQLite", extensions: ["db"] }],
      })
      if (!src) return
      const go = await ask(t("io.restoreConfirm"), {
        title: t("io.restoreDialogTitle"),
        kind: "warning",
      })
      if (!go) return
      await ioService.backupRestore(src)
      // full reload: every store/thread/folder in the webview was loaded from
      // the pre-restore database — re-read everything from the restored one.
      toast.success(t("io.restored"))
      setTimeout(() => window.location.reload(), 600)
      return
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t("io.restoreError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card size="sm" className="mt-8">
      <CardHeader>
        <CardTitle>{t("io.title")}</CardTitle>
        <CardDescription>{t("io.description")}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <Select value={accountId} onValueChange={(v) => setAccountId(v)}>
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
          <Select value={folderId} onValueChange={(v) => setFolderId(v)}>
            <SelectTrigger className="h-8 w-full sm:w-48" aria-label={t("io.pickFolder")}>
              <SelectValue placeholder={t("io.pickFolderPlaceholder")} />
            </SelectTrigger>
            <SelectContent>
              {folders.map((f) => (
                <SelectItem key={f.id} value={f.id}>
                  {formatFolderName(f.name)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            variant="outline"
            size="sm"
            onClick={() => void exportFolder()}
            disabled={busy || !folderId}
          >
            <LogOut data-icon="inline-start" />
            {t("io.exportMbox")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => void importFile()}
            disabled={busy || !accountId}
          >
            <LogIn data-icon="inline-start" />
            {t("io.importMails")}
          </Button>
        </div>
        <div className="flex flex-wrap items-center gap-2 border-t pt-3">
          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="text-sm font-semibold">{t("io.backupDb")}</span>
            <span className="text-[13px] text-muted-foreground">{t("io.backupDbDesc")}</span>
          </span>
          <Button variant="outline" size="sm" onClick={() => void backup()} disabled={busy}>
            <Archive data-icon="inline-start" />
            {t("io.backup")}
          </Button>
          <Button variant="outline" size="sm" onClick={() => void restore()} disabled={busy}>
            <ArchiveRestore data-icon="inline-start" />
            {t("io.restore")}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}
