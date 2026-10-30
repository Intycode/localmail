import * as React from "react"
import { Mail, Pencil, Plus, RefreshCw, Trash2, UserCheck } from "lucide-react"
import { toast } from "sonner"

import { cn } from "@/lib/utils"
import { navigate } from "@/lib/router"
import { formatFolderName, formatProvider } from "@/lib/format"
import { setActiveAccount, setAccounts as setStoreAccounts } from "@/stores/accountStore"
import { getMailService } from "@/services/serviceRegistry"
import { syncAccount } from "@/services/syncService"
import { isTauri } from "@/services/tauriMailService"
import { useAccounts } from "@/lib/useAccounts"
import { useT } from "@/lib/i18n"
import type { Account, ImapAccountDraft } from "@/types"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { AccountAvatar } from "@/components/app/AccountAvatar"
import { AvatarDialog } from "@/components/app/AvatarDialog"
import { CopyButton } from "@/components/app/CopyButton"
import { AccountConnectForm } from "@/components/app/AccountConnectForm"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { PageBody, PageHeader } from "../PageHeader"
import { AccountsIo } from "./AccountsIo"

interface AccountsPageProps {
  accounts: Account[]
}

/** AccountsPage — connected accounts + the real add-account (generic IMAP) flow. */
export function AccountsPage({ accounts: staticAccounts }: AccountsPageProps) {
  const t = useT()
  // static props are a build-time snapshot: under Tauri the store is live
  const accounts = useAccounts(staticAccounts)
  return (
    <PageBody>
      <PageHeader
        title={t("accounts.title")}
        description={t("accounts.description")}
        actions={<AddAccountDialog />}
      />
      {accounts.length === 0 && (
        <Empty className="py-10">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Mail />
            </EmptyMedia>
            <EmptyTitle>{t("accounts.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("accounts.emptyDesc")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <AddAccountDialog
              trigger={
                <Button>
                  <Plus data-icon="inline-start" />
                  {t("nav.connectAccount")}
                </Button>
              }
            />
          </EmptyContent>
        </Empty>
      )}
      {accounts.length > 0 && (
        <ul className="divide-y divide-border overflow-hidden rounded-xl border border-border bg-card">
          {accounts.map((a) => (
            <AccountCard key={a.id} account={a} />
          ))}
        </ul>
      )}
      {accounts.length > 0 && <AccountsIo accounts={accounts} />}
    </PageBody>
  )
}

function AccountCard({ account }: { account: Account }) {
  const t = useT()
  const [syncing, setSyncing] = React.useState(false)
  const [removing, setRemoving] = React.useState(false)

  async function use(): Promise<void> {
    setActiveAccount(account.id)
    if (isTauri()) {
      try {
        const svc = (await getMailService()) as AccountAdmin
        await svc.setActive?.(account.id)
      } catch {
        toast.error(t("accounts.setActiveFailed"))
      }
    }
    navigate("/")
  }

  function sync(): void {
    setSyncing(true)
    syncAccount(account.id)
    setTimeout(() => setSyncing(false), 800)
  }

  async function remove(): Promise<void> {
    setRemoving(true)
    try {
      const svc = await getMailService()
      const admin = svc as AccountAdmin
      if (!admin.removeAccount) throw new Error(t("common.engineOnly"))
      await admin.removeAccount(account.id)
      setStoreAccounts(await svc.getAccounts())
      toast.success(t("accounts.removed"))
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("accounts.removeFailed"))
      setRemoving(false)
    }
  }

  return (
    <li
      className={cn(
        "flex flex-wrap items-center gap-x-4 gap-y-3 px-5 py-4",
        account.isActive && "bg-primary/[0.03]",
      )}
    >
      <AvatarDialog
        accountId={account.id}
        name={account.name}
        trigger={
          <button
            type="button"
            aria-label={t("accounts.personalizeAvatar")}
            title={t("accounts.changeAvatar")}
            className="group/av relative cursor-pointer rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-card"
          >
            <AccountAvatar accountId={account.id} name={account.name} size="lg" />
            <span className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-full bg-background ring-1 ring-border transition-colors group-hover/av:bg-muted">
              <Pencil className="size-2.5 text-muted-foreground" />
            </span>
          </button>
        }
      />
      <div className="flex min-w-0 flex-1 basis-60 flex-col gap-0.5">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate font-semibold">{account.name}</span>
          <Badge variant="secondary" className="font-mono text-[11px] font-normal">
            {formatProvider(account.provider)}
          </Badge>
          {account.isActive && (
            <Badge className="bg-primary/10 text-primary">{t("nav.activeBadge")}</Badge>
          )}
        </div>
        <div className="flex min-w-0 items-center gap-1">
          <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">
            {account.email}
          </span>
          <CopyButton value={account.email} label={t("thread.copyEmail")} size="icon-xs" />
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {!account.isActive && (
          <Button variant="outline" size="sm" onClick={use}>
            <UserCheck data-icon="inline-start" />
            {t("accounts.useThis")}
          </Button>
        )}
        <Button variant="outline" size="sm" onClick={sync} disabled={syncing}>
          <RefreshCw data-icon="inline-start" className={syncing ? "animate-spin" : undefined} />
          {syncing ? t("accounts.syncing") : t("accounts.sync")}
        </Button>
        {isTauri() && (
          <Dialog>
            <DialogTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                className="text-destructive hover:text-destructive"
                aria-label={t("common.remove")}
                title={t("common.remove")}
              >
                <Trash2 />
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>
                  {t("accounts.removeConfirmTitle", { name: account.name })}
                </DialogTitle>
                <DialogDescription>{t("accounts.removeConfirmDesc")}</DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <Button variant="destructive" size="sm" onClick={remove} disabled={removing}>
                  {removing ? t("accounts.removing") : t("accounts.removeForever")}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        )}
      </div>
      <div className="flex basis-full flex-wrap gap-1.5 sm:pl-[3.25rem]">
        {account.folders.length > 0 ? (
          account.folders.map((f) => (
            <Badge key={f.id} variant="outline" className="font-normal">
              {formatFolderName(f.name)}{" "}
              <span className="text-muted-foreground tabular-nums">{f.total}</span>
            </Badge>
          ))
        ) : (
          <span className="text-[13px] text-muted-foreground">{t("accounts.noFolders")}</span>
        )}
      </div>
    </li>
  )
}

/** Engine-side account administration (Tauri only). */
type AccountAdmin = {
  addAccount?: (draft: ImapAccountDraft) => Promise<Account>
  removeAccount?: (accountId: string) => Promise<void>
  setActive?: (accountId: string) => Promise<void>
}

/**
 * AddAccountDialog — the classic add-account dialog: a thin shell (title +
 * pinned footer) around the shared AccountConnectForm.
 */
function AddAccountDialog({ trigger }: { trigger?: React.ReactNode }) {
  const t = useT()
  const [open, setOpen] = React.useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        {trigger ?? (
          <Button size="sm">
            <Plus data-icon="inline-start" />
            {t("accounts.add")}
          </Button>
        )}
      </DialogTrigger>
      {/* Fixed header + scrollable body + pinned footer: the form always fits
          the window and the password / submit stay reachable. */}
      <DialogContent className="flex max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-[34rem]">
        <DialogHeader className="shrink-0 border-b border-border px-6 pt-5 pr-12 pb-4">
          <DialogTitle>{t("accounts.addImapTitle")}</DialogTitle>
          <DialogDescription>{t("accounts.addImapDesc")}</DialogDescription>
        </DialogHeader>
        <AccountConnectForm
          formId="add-account-form"
          onConnected={() => {
            setOpen(false)
            // the engine event stream would refresh the stores anyway, but a
            // reload also re-runs the first sync immediately
            window.location.reload()
          }}
          footer={(submitting) => (
            <DialogFooter className="shrink-0 border-t border-border px-6 py-3.5">
              <Button type="submit" form="add-account-form" disabled={submitting}>
                {submitting ? t("accounts.connecting") : t("accounts.connect")}
              </Button>
            </DialogFooter>
          )}
        />
      </DialogContent>
    </Dialog>
  )
}
