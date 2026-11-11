import * as React from "react"
import { Pencil, Type } from "lucide-react"

import {
  AVATAR_COLORS,
  AVATAR_ICONS,
  getAvatarServerSnapshot,
  getAvatarSnapshot,
  resetAvatarPref,
  setAvatarPref,
  subscribeAvatars,
} from "@/lib/avatar"
import { cn } from "@/lib/utils"
import { useT } from "@/lib/i18n"
import { AccountAvatar } from "@/components/app/AccountAvatar"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"

const swatchFocus =
  "outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
const swatchSelected = "ring-2 ring-ring ring-offset-2 ring-offset-background"
const iconSwatch =
  "flex size-8 items-center justify-center rounded-lg border border-border text-muted-foreground hover:bg-accent"

interface AvatarDialogProps {
  accountId: string
  name: string
  trigger?: React.ReactNode
}

/**
 * AvatarDialog — pick accent color and icon for an account avatar. Selections
 * persist (device-local) and apply immediately, including the live preview.
 */
export function AvatarDialog({ accountId, name, trigger }: AvatarDialogProps) {
  const t = useT()
  const [open, setOpen] = React.useState(false)
  const prefs = React.useSyncExternalStore(
    subscribeAvatars,
    getAvatarSnapshot,
    getAvatarServerSnapshot,
  )
  const pref = prefs[accountId] ?? {}

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        {trigger ?? (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t("accounts.personalizeAvatar")}
            title={t("accounts.personalizeAvatar")}
          >
            <Pencil />
          </Button>
        )}
      </DialogTrigger>
      <DialogContent className="sm:max-w-md" aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{t("avatar.title", { name })}</DialogTitle>
        </DialogHeader>

        <div className="flex items-center gap-3 px-0.5">
          <AccountAvatar accountId={accountId} name={name} size="lg" />
          <p className="min-w-0 truncate text-sm font-medium">{name}</p>
        </div>

        <div className="flex flex-col gap-2">
          <span className="text-xs font-medium text-muted-foreground">{t("avatar.color")}</span>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              title={t("avatar.colorDefaultTitle")}
              aria-label={t("avatar.colorDefault")}
              aria-pressed={!pref.color}
              onClick={() => setAvatarPref(accountId, { ...pref, color: undefined })}
              className={cn(
                "flex size-8 cursor-pointer items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary",
                swatchFocus,
                !pref.color && swatchSelected,
              )}
            >
              A
            </button>
            {AVATAR_COLORS.map((c) => (
              <button
                key={c.value}
                type="button"
                title={t(c.label)}
                aria-label={t(c.label)}
                aria-pressed={pref.color === c.value}
                onClick={() => setAvatarPref(accountId, { ...pref, color: c.value })}
                style={{ backgroundColor: c.value }}
                className={cn(
                  "size-8 cursor-pointer rounded-full",
                  swatchFocus,
                  pref.color === c.value && swatchSelected,
                )}
              />
            ))}
          </div>
        </div>

        <div className="flex flex-col gap-2">
          <span className="text-xs font-medium text-muted-foreground">{t("avatar.icon")}</span>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              title={t("avatar.initials")}
              aria-label={t("avatar.initials")}
              aria-pressed={!pref.icon}
              onClick={() => setAvatarPref(accountId, { ...pref, icon: undefined })}
              className={cn(
                iconSwatch,
                "cursor-pointer",
                swatchFocus,
                !pref.icon && "border-primary/30 bg-primary/10 text-primary hover:bg-primary/10",
              )}
            >
              <Type className="size-4" />
            </button>
            {AVATAR_ICONS.map((i) => (
              <button
                key={i.key}
                type="button"
                title={t(i.label)}
                aria-label={t(i.label)}
                aria-pressed={pref.icon === i.key}
                onClick={() => setAvatarPref(accountId, { ...pref, icon: i.key })}
                className={cn(
                  iconSwatch,
                  "cursor-pointer",
                  swatchFocus,
                  pref.icon === i.key &&
                    "border-primary/30 bg-primary/10 text-primary hover:bg-primary/10",
                )}
              >
                <i.icon className="size-4" />
              </button>
            ))}
          </div>
        </div>

        <DialogFooter className="sm:justify-between">
          <Button variant="ghost" size="sm" onClick={() => resetAvatarPref(accountId)}>
            {t("avatar.reset")}
          </Button>
          <Button size="sm" onClick={() => setOpen(false)}>
            {t("common.done")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
