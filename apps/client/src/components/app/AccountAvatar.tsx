import * as React from "react"

import {
  AVATAR_ICONS,
  avatarTint,
  getAvatarServerSnapshot,
  getAvatarSnapshot,
  subscribeAvatars,
} from "@/lib/avatar"
import { initials } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"

const sizeClasses = {
  sm: {
    root: "size-5 rounded-md after:rounded-md",
    fallback: "rounded-md text-[10px]",
    icon: "size-3",
  },
  md: {
    root: "size-8 rounded-lg after:rounded-lg",
    fallback: "rounded-lg text-xs",
    icon: "size-4",
  },
  lg: {
    root: "size-9 rounded-lg after:rounded-lg",
    fallback: "rounded-lg text-sm",
    icon: "size-4",
  },
} as const

interface AccountAvatarProps {
  accountId: string
  name: string
  size?: keyof typeof sizeClasses
  className?: string
}

/**
 * AccountAvatar — initials (or the chosen icon) in the chosen accent color.
 * Reads the per-account preference via useSyncExternalStore so changes in the
 * accounts screen apply live in the sidebar too.
 */
export function AccountAvatar({ accountId, name, size = "md", className }: AccountAvatarProps) {
  const prefs = React.useSyncExternalStore(
    subscribeAvatars,
    getAvatarSnapshot,
    getAvatarServerSnapshot,
  )
  const pref = prefs[accountId] ?? {}
  const s = sizeClasses[size]
  const Icon = pref.icon ? AVATAR_ICONS.find((i) => i.key === pref.icon)?.icon : undefined

  return (
    <Avatar className={cn(s.root, className)}>
      <AvatarFallback
        className={cn(s.fallback, !pref.color && "bg-primary/10 text-primary")}
        style={pref.color ? avatarTint(pref.color) : undefined}
      >
        {Icon ? <Icon className={s.icon} /> : initials(name)}
      </AvatarFallback>
    </Avatar>
  )
}
