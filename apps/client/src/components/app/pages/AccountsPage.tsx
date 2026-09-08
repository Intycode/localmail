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
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
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
      <div className="flex flex-col gap-3">
        {accounts.map((a) => (
          <AccountCard key={a.id} account={a} />
        ))}
      </div>
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
    <Card size="sm" className={cn(account.isActive && "ring-primary/40")}>
      <CardHeader>
        <div className="flex min-w-0 items-center gap-3">
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
          <div className="grid min-w-0 flex-1">
            <CardTitle className="truncate">{account.name}</CardTitle>
            <div className="flex items-center gap-1">
              <CardDescription className="min-w-0 truncate">{account.email}</CardDescription>
              <CopyButton value={account.email} label={t("thread.copyEmail")} size="icon-xs" />
            </div>
          </div>
        </div>
        <CardAction>
          <Badge variant="secondary" className="h-6 shrink-0 px-2.5 text-sm font-medium">
            {formatProvider(account.provider)}
          </Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-1.5">
        <span className="text-xs font-medium text-muted-foreground">{t("accounts.folders")}</span>
        {account.folders.length > 0 ? (
          <div className="flex flex-wrap gap-1.5">
            {account.folders.map((f) => (
              <Badge key={f.id} variant="outline" className="font-normal">
                {formatFolderName(f.name)}{" "}
                <span className="tabular-nums text-muted-foreground">{f.total}</span>
              </Badge>
            ))}
          </div>
        ) : (
          <span className="text-[13px] text-muted-foreground">{t("accounts.noFolders")}</span>
        )}
      </CardContent>
      <CardFooter className="gap-2">
        <Button size="sm" onClick={use} disabled={account.isActive}>
          <UserCheck data-icon="inline-start" />
          {account.isActive ? t("accounts.active") : t("accounts.useThis")}
        </Button>
        <Button variant="outline" size="sm" onClick={sync} disabled={syncing}>
          <RefreshCw data-icon="inline-start" className={syncing ? "animate-spin" : undefined} />
          {syncing ? t("accounts.syncing") : t("accounts.sync")}
        </Button>
        {isTauri() && (
          <Dialog>
            <DialogTrigger asChild>
              <Button variant="destructive" size="sm" className="ml-auto">
                <Trash2 data-icon="inline-start" />
                {t("common.remove")}
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
      </CardFooter>
    </Card>
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
