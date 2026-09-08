import { CloudOff, Clock3 } from "lucide-react"

import { useT } from "@/lib/i18n"
import { useOutbox } from "@/stores/outboxStore"

/**
 * OfflineBanner — the offline-first status strip under the topbar. Amber
 * while the engine cannot reach the mail server; shows the pending count
 * whenever operations are parked (send, flags, moves, deletes).
 */
export function OfflineBanner(): React.ReactNode {
  const t = useT()
  const outbox = useOutbox()
  if (!outbox.known && outbox.pending === 0 && outbox.online) return null
  if (outbox.online && outbox.pending === 0) return null

  return (
    <div
      role="status"
      className="flex h-8 shrink-0 items-center justify-center gap-2 border-b border-amber-500/30 bg-amber-500/10 px-3 text-xs font-medium text-amber-700 dark:text-amber-400"
    >
      {!outbox.online ? (
        <>
          <CloudOff data-icon="inline-start" className="size-3.5" />
          <span>{t("offline.message")}</span>
        </>
      ) : (
        <>
          <Clock3 data-icon="inline-start" className="size-3.5" />
          <span>{t("offline.retrying")}</span>
        </>
      )}
      {outbox.pending > 0 && (
        <span className="rounded-full border border-amber-500/40 px-1.5 py-px font-mono text-[10px]">
          {outbox.pending}
        </span>
      )}
    </div>
  )
}
