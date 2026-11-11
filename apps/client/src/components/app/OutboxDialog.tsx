import * as React from "react"
import { Loader2, SendHorizontal, X } from "lucide-react"
import { toast } from "sonner"

import { dateLocale, useT } from "@/lib/i18n"
import { getMailService } from "@/services/serviceRegistry"
import type { OutboxItem } from "@/services/mailService"
import { outboxStore } from "@/stores/outboxStore"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"

interface OutgoingMail {
  id: string
  to: string
  subject: string
  /** null → the worker is delivering it right now (not cancellable) */
  sendAt: Date | null
  lastError?: string
}

/** Parses the queued "send" ops; other parked ops (flags, moves) are hidden. */
function outgoing(items: OutboxItem[]): OutgoingMail[] {
  return items
    .filter((i) => i.kind === "send")
    .map((i) => {
      let msg: { to?: { name?: string; email: string }[]; subject?: string } = {}
      try {
        msg = JSON.parse(i.payload).message ?? {}
      } catch {
        // malformed payload: still listed so it can be cancelled
      }
      const at = new Date(i.nextAttemptAt)
      return {
        id: i.id,
        to: (msg.to ?? []).map((r) => r.name || r.email).join(", "),
        subject: msg.subject ?? "",
        // a claimed op has a sentinel time that the engine serializes as year 1
        sendAt: at.getFullYear() < 2000 ? null : at,
        lastError: i.lastError,
      }
    })
}

/** OutboxDialog — queued outgoing mail (undo window, scheduled, offline) with cancel. */
export function OutboxDialog({ children }: { children: React.ReactNode }): React.ReactNode {
  const t = useT()
  const [open, setOpen] = React.useState(false)
  const [items, setItems] = React.useState<OutgoingMail[] | null>(null)

  const refresh = React.useCallback(async () => {
    try {
      const svc = await getMailService()
      setItems(outgoing((await svc.listOutbox?.()) ?? []))
    } catch (err) {
      setItems([])
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }, [])

  React.useEffect(() => {
    if (!open) return
    void refresh()
    // outbox.updated fires on enqueue / delivery / cancel
    return outboxStore.subscribe(() => void refresh())
  }, [open, refresh])

  async function cancel(id: string): Promise<void> {
    try {
      const svc = await getMailService()
      await svc.cancelQueuedOp?.(id)
      toast.success(t("compose.sendCancelled"))
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
    void refresh()
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{children}</DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("outbox.title")}</DialogTitle>
          <DialogDescription>{t("outbox.description")}</DialogDescription>
        </DialogHeader>
        {items === null ? (
          <Loader2 className="mx-auto size-5 animate-spin text-muted-foreground" />
        ) : items.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">{t("outbox.empty")}</p>
        ) : (
          <ul className="flex max-h-96 flex-col divide-y divide-border overflow-y-auto">
            {items.map((m) => (
              <li key={m.id} className="flex items-center gap-3 py-2.5">
                <SendHorizontal className="size-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">
                    {m.subject || t("inbox.noSubject")}
                  </p>
                  <p className="truncate text-xs text-muted-foreground">
                    {t("outbox.to", { to: m.to || "—" })} ·{" "}
                    {m.sendAt
                      ? t("outbox.at", { date: m.sendAt.toLocaleString(dateLocale()) })
                      : t("outbox.sending")}
                  </p>
                  {m.lastError && (
                    <p className="truncate text-xs text-destructive" title={m.lastError}>
                      {m.lastError}
                    </p>
                  )}
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={!m.sendAt}
                  onClick={() => void cancel(m.id)}
                >
                  <X data-icon="inline-start" />
                  {t("common.cancel")}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </DialogContent>
    </Dialog>
  )
}
