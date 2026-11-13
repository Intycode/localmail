import * as React from "react"
import {
  Archive,
  Check,
  ChevronsUpDown,
  Clock,
  FilePenLine,
  Folder as FolderIcon,
  Import,
  Inbox,
  Mails,
  SquarePen,
  Send,
  SendHorizontal,
  Settings,
  ShieldAlert,
  Trash2,
  User,
} from "lucide-react"

import { cn } from "@/lib/utils"
import { formatFolderName } from "@/lib/format"
import { routerStore } from "@/lib/router"
import { useT } from "@/lib/i18n"
import { switchAccount } from "@/lib/useAccounts"
import type { Account, Folder } from "@/types"
import { AccountAvatar } from "@/components/app/AccountAvatar"
import { AppLogo } from "@/components/app/AppLogo"
import { OutboxDialog } from "@/components/app/OutboxDialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  SidebarSeparator,
} from "@/components/ui/sidebar"

interface AppSidebarProps extends React.ComponentProps<typeof Sidebar> {
  accounts: Account[]
  folders: Folder[]
  activeFolderId: string
  /** the unified inbox (?account=all) is the active view */
  unifiedActive?: boolean
}

/** Minimal monochrome glyph per folder type. The lookup fallback guards
 *  against backend folder types the client does not know yet (an undefined
 *  icon would crash the whole sidebar render). */
const folderIcons: Record<Folder["type"], React.ComponentType<{ className?: string }>> = {
  inbox: Inbox,
  sent: Send,
  drafts: FilePenLine,
  archive: Archive,
  trash: Trash2,
  junk: ShieldAlert,
  snoozed: Clock,
  custom: FolderIcon,
  import: Import,
}

export function AppSidebar({
  accounts,
  folders,
  activeFolderId,
  unifiedActive,
  ...props
}: AppSidebarProps) {
  const t = useT()
  const route = React.useSyncExternalStore(
    routerStore.subscribe,
    () => routerStore.get().route.name,
    () => "inbox",
  )
  // folder highlight only while a mail view is open, not on accounts/settings
  const inMail = route === "inbox" || route === "thread"
  const activeAccount = accounts.find((a) => a.isActive) ?? accounts[0]
  // Unified view entry: meaningful only with more than one account. The badge
  // sums every account's inbox unread count.
  const showUnified = accounts.length >= 2
  const unifiedUnread = accounts.reduce(
    (sum, a) =>
      sum + (a.folders ?? []).filter((f) => f.type === "inbox").reduce((s, f) => s + f.unread, 0),
    0,
  )

  return (
    <Sidebar collapsible="offcanvas" {...props}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <AccountSwitcher accounts={accounts} active={activeAccount} />
          </SidebarMenuItem>
        </SidebarMenu>
        {/* Compose lives here on desktop; search + sync sit in the list toolbar (InboxPage). */}
        <div className="flex gap-1.5 px-0.5 pt-1">
          <Button asChild className="flex-1">
            <a href="/compose" aria-current={route === "compose" ? "page" : undefined}>
              <SquarePen data-icon="inline-start" />
              {t("nav.compose")}
            </a>
          </Button>
        </div>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>{t("nav.folders")}</SidebarGroupLabel>
          <SidebarGroupContent>
            {accounts.length === 0 && (
              <p className="mb-2 px-2 text-xs text-muted-foreground">
                {t("nav.noAccounts")}{" "}
                <a className="text-primary underline-offset-4 hover:underline" href="/accounts">
                  {t("nav.connectOne")}
                </a>
                .
              </p>
            )}
            <SidebarMenu>
              {showUnified && (
                <SidebarMenuItem>
                  <SidebarMenuButton
                    asChild
                    isActive={unifiedActive}
                    tooltip={t("nav.allMailboxes")}
                  >
                    <a href="/?account=all" aria-current={unifiedActive ? "page" : undefined}>
                      <Mails />
                      <span>{t("nav.allMailboxes")}</span>
                    </a>
                  </SidebarMenuButton>
                  {unifiedUnread > 0 && (
                    <SidebarMenuBadge className="tabular-nums">{unifiedUnread}</SidebarMenuBadge>
                  )}
                </SidebarMenuItem>
              )}
              {folders.map((f) => {
                const Icon = folderIcons[f.type] ?? FolderIcon
                const active = inMail && f.id === activeFolderId
                return (
                  <React.Fragment key={f.id}>
                    <SidebarMenuItem>
                      <SidebarMenuButton
                        asChild
                        isActive={active}
                        tooltip={formatFolderName(f.name)}
                      >
                        <a href={`/?folder=${f.id}`} aria-current={active ? "page" : undefined}>
                          <Icon />
                          <span>{formatFolderName(f.name)}</span>
                        </a>
                      </SidebarMenuButton>
                      {f.unread > 0 && (
                        <SidebarMenuBadge className="tabular-nums">{f.unread}</SidebarMenuBadge>
                      )}
                    </SidebarMenuItem>
                    {f.type === "sent" && (
                      <SidebarMenuItem>
                        <OutboxDialog>
                          <SidebarMenuButton tooltip={t("outbox.title")}>
                            <SendHorizontal />
                            <span>{t("outbox.title")}</span>
                          </SidebarMenuButton>
                        </OutboxDialog>
                      </SidebarMenuItem>
                    )}
                  </React.Fragment>
                )
              })}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter>
        <SidebarSeparator />
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton asChild isActive={route === "accounts"} tooltip={t("nav.accounts")}>
              <a href="/accounts" aria-current={route === "accounts" ? "page" : undefined}>
                <User />
                <span>{t("nav.accounts")}</span>
              </a>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton asChild isActive={route === "settings"} tooltip={t("nav.settings")}>
              <a href="/settings" aria-current={route === "settings" ? "page" : undefined}>
                <Settings />
                <span>{t("nav.settings")}</span>
              </a>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

function AccountSwitcher({ accounts, active }: { accounts: Account[]; active?: Account }) {
  const t = useT()
  if (accounts.length === 0) {
    return (
      <SidebarMenuButton size="lg" asChild>
        <a href="/accounts">
          <AppLogo className="size-8" />
          <div className="grid min-w-0 flex-1 leading-tight">
            <span className="truncate font-semibold">LocalMail</span>
            <span className="truncate text-xs text-muted-foreground">{t("nav.noAccountYet")}</span>
          </div>
        </a>
      </SidebarMenuButton>
    )
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <SidebarMenuButton
          size="lg"
          className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
        >
          {active && <AccountAvatar accountId={active.id} name={active.name} size="md" />}
          <div className="grid min-w-0 flex-1 leading-tight">
            <span className="truncate font-semibold">{active?.name ?? "LocalMail"}</span>
            <span className="truncate text-xs text-muted-foreground">{active?.email}</span>
          </div>
          <ChevronsUpDown className="ml-auto size-4 opacity-50" />
        </SidebarMenuButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        className="w-(--radix-dropdown-menu-trigger-width) min-w-56"
        align="start"
        side="bottom"
      >
        <DropdownMenuLabel>{t("nav.accounts")}</DropdownMenuLabel>
        {accounts.map((a) => (
          <DropdownMenuItem
            key={a.id}
            className={cn(a.isActive && "font-semibold")}
            onSelect={() => void switchAccount(a.id).catch(() => undefined)}
          >
            <AccountAvatar accountId={a.id} name={a.name} size="sm" />
            <span className="min-w-0 flex-1 truncate">{a.email}</span>
            {a.isActive && <Check className="size-4" aria-label={t("nav.activeBadge")} />}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <a href="/accounts">{t("nav.manageAccounts")}</a>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
