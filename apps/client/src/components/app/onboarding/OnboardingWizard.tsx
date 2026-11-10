import * as React from "react"
import { Keyboard, Languages, Mail, RefreshCw, ShieldCheck, TriangleAlert } from "lucide-react"

import { cn } from "@/lib/utils"
import { navigate } from "@/lib/router"
import { setOnboardingDone } from "@/lib/settings"
import { getLocale, LOCALES, setLocale, useT, type Locale } from "@/lib/i18n"
import { setAccounts as setStoreAccounts } from "@/stores/accountStore"
import { getMailService } from "@/services/serviceRegistry"
import { syncAccount, syncStore } from "@/services/syncService"
import type { Account } from "@/types"
import { AccountConnectForm } from "@/components/app/AccountConnectForm"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Kbd } from "@/components/ui/kbd"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Step = "welcome" | "connect" | "sync" | "done"

interface OnboardingWizardProps {
  /** Called when the wizard closes for any reason (finish, skip, Esc). */
  onClose: () => void
}

/** Native names — a language picker should never be in the other language. */
const LOCALE_LABELS: Record<Locale, string> = { it: "Italiano", en: "English" }

/**
 * OnboardingWizard — the first-run experience, shown once when the app
 * starts with zero accounts (see lib/onboarding.ts for the trigger).
 *
 * Four steps: welcome → connect the first account (shared
 * AccountConnectForm: Google, Microsoft, IMAP/JMAP) → first sync progress →
 * done, with the essential keyboard hints. The welcome step carries the
 * language picker: the choice applies to the whole app immediately.
 *
 * The user is never trapped: every close (Esc included) marks the onboarding
 * as done and returns to the app, where the AccountsPage empty state offers
 * the same flow.
 */
export function OnboardingWizard({ onClose }: OnboardingWizardProps) {
  const [step, setStep] = React.useState<Step>("welcome")
  const [accountId, setAccountId] = React.useState("")

  // any dismissal = skip: flag set so the wizard never shows again
  function dismiss(): void {
    setOnboardingDone()
    onClose()
  }

  function finish(): void {
    setOnboardingDone()
    onClose()
    navigate("/")
  }

  /** Account connected: refresh the stores, pick the new account and jump
   *  to the sync step (an explicit sync makes the progress visible even if
   *  the engine's automatic first sync is still starting). */
  async function connected(added: Account): Promise<void> {
    setAccountId(added.id)
    try {
      const svc = await getMailService()
      setStoreAccounts(await svc.getAccounts())
      syncAccount(added.id)
    } catch {
      // engine hiccup: the event stream still drives the sync step
    }
    setStep("sync")
  }

  return (
    <Dialog open onOpenChange={(open) => !open && dismiss()}>
      <DialogContent
        showCloseButton={false}
        className={cn(
          "w-[calc(100vw-2rem)] sm:max-w-[34rem]",
          step === "connect" || step === "sync"
            ? "flex h-[min(38rem,calc(100dvh-2rem))] flex-col gap-0 overflow-hidden p-0"
            : "gap-4 p-6",
        )}
      >
        <StepBar
          step={step}
          className={step === "connect" || step === "sync" ? "px-6 pt-5" : undefined}
        />
        {step === "welcome" && <WelcomeStep onStart={() => setStep("connect")} onSkip={dismiss} />}
        {step === "connect" && (
          <ConnectStep
            onConnected={(a) => void connected(a)}
            onBack={dismiss}
            formId="onboarding-account-form"
          />
        )}
        {step === "sync" && (
          <SyncStep
            accountId={accountId}
            onRetry={() => syncAccount(accountId)}
            onNext={() => setStep("done")}
            onSkip={dismiss}
          />
        )}
        {step === "done" && <DoneStep onFinish={finish} />}
      </DialogContent>
    </Dialog>
  )
}

const STEPS: { step: Step; labelKey: string }[] = [
  { step: "welcome", labelKey: "onboarding.stepWelcome" },
  { step: "connect", labelKey: "onboarding.stepAccount" },
  { step: "sync", labelKey: "onboarding.stepSync" },
  { step: "done", labelKey: "onboarding.stepDone" },
]

/** Four-segment progress bar: where the user is in the first-run flow. */
function StepBar({ step, className }: { step: Step; className?: string }) {
  const t = useT()
  const current = STEPS.findIndex((s) => s.step === step)
  return (
    <ol
      aria-label={t("onboarding.steps")}
      className={cn("grid shrink-0 grid-cols-4 gap-2", className)}
    >
      {STEPS.map((s, i) => (
        <li
          key={s.step}
          aria-current={i === current ? "step" : undefined}
          className="flex flex-col gap-1.5"
        >
          <span className={cn("h-1 rounded-full", i <= current ? "bg-primary" : "bg-border")} />
          <span
            className={cn(
              "truncate text-[11px]",
              i === current ? "font-semibold text-foreground" : "text-muted-foreground",
            )}
          >
            {t(s.labelKey)}
          </span>
        </li>
      ))}
    </ol>
  )
}

function WelcomeStep({ onStart, onSkip }: { onStart: () => void; onSkip: () => void }) {
  const t = useT()
  return (
    <div className="flex flex-col items-center gap-5 text-center">
      <div className="flex size-14 items-center justify-center rounded-2xl bg-primary/10">
        <Mail className="size-7 text-primary" />
      </div>
      <DialogHeader className="items-center gap-2">
        <DialogTitle className="text-lg">{t("onboarding.welcomeTitle")}</DialogTitle>
        <DialogDescription className="max-w-sm">{t("onboarding.welcomeDesc")}</DialogDescription>
      </DialogHeader>
      <ul className="flex flex-col gap-2 text-left text-sm text-muted-foreground">
        <li className="flex items-center gap-2.5">
          <ShieldCheck className="size-4 shrink-0 text-primary" />
          {t("onboarding.pointNoCloud")}
        </li>
        <li className="flex items-center gap-2.5">
          <Mail className="size-4 shrink-0 text-primary" />
          {t("onboarding.pointLocal")}
        </li>
        <li className="flex items-center gap-2.5">
          <Keyboard className="size-4 shrink-0 text-primary" />
          {t("onboarding.pointKeyboard")}
        </li>
      </ul>
      <div className="flex w-full items-center justify-center gap-2">
        <Languages aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
        <Select value={getLocale()} onValueChange={(v) => v && setLocale(v as Locale)}>
          <SelectTrigger className="w-40" aria-label={t("onboarding.language")}>
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
      <div className="mt-2 flex w-full flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <Button variant="ghost" onClick={onSkip}>
          {t("common.skip")}
        </Button>
        <Button onClick={onStart}>{t("onboarding.start")}</Button>
      </div>
    </div>
  )
}

function ConnectStep({
  onConnected,
  onBack,
  formId,
}: {
  onConnected: (account: Account) => void
  onBack: () => void
  formId: string
}) {
  const t = useT()
  return (
    <>
      <DialogHeader className="shrink-0 border-b border-border px-6 pt-5 pr-12 pb-4">
        <DialogTitle>{t("onboarding.connectTitle")}</DialogTitle>
        <DialogDescription>{t("onboarding.connectDesc")}</DialogDescription>
      </DialogHeader>
      <AccountConnectForm
        formId={formId}
        onConnected={onConnected}
        footer={(submitting) => (
          <div className="flex shrink-0 flex-col-reverse gap-2 border-t border-border px-6 py-3.5 sm:flex-row sm:justify-end">
            <Button type="button" variant="ghost" onClick={onBack}>
              {t("common.skip")}
            </Button>
            <Button type="submit" form={formId} disabled={submitting}>
              {submitting ? t("accounts.connecting") : t("accounts.connect")}
            </Button>
          </div>
        )}
      />
    </>
  )
}

function SyncStep({
  accountId,
  onRetry,
  onNext,
  onSkip,
}: {
  accountId: string
  onRetry: () => void
  onNext: () => void
  onSkip: () => void
}) {
  const t = useT()
  // latest sync event for the just-connected account drives the UI
  const state = React.useSyncExternalStore(syncStore.subscribe, syncStore.get)
  const ev = accountId ? state.byAccount[accountId] : undefined

  return (
    <>
      <DialogHeader className="shrink-0 border-b border-border px-6 pt-5 pr-12 pb-4">
        <DialogTitle>{t("onboarding.syncTitle")}</DialogTitle>
        <DialogDescription>{t("onboarding.syncDesc")}</DialogDescription>
      </DialogHeader>
      <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-4 px-6 text-center">
        {ev?.type === "sync.error" ? (
          <>
            <div className="flex size-12 items-center justify-center rounded-xl bg-destructive/10">
              <TriangleAlert className="size-6 text-destructive" />
            </div>
            <div>
              <p className="text-sm font-medium">{t("onboarding.syncFailed")}</p>
              <p className="mt-1 max-w-sm text-xs leading-relaxed text-muted-foreground">
                {ev.message ?? t("onboarding.syncFailedDesc")}
              </p>
            </div>
          </>
        ) : ev?.type === "sync.completed" ? (
          <>
            <div className="flex size-12 items-center justify-center rounded-xl bg-primary/10">
              <ShieldCheck className="size-6 text-primary" />
            </div>
            <p className="text-sm font-medium">{t("onboarding.syncDone")}</p>
          </>
        ) : (
          <>
            <RefreshCw className="size-6 animate-spin text-muted-foreground" />
            <p className="text-sm text-muted-foreground">
              {ev?.type === "sync.progress"
                ? t("onboarding.syncProgress", { done: ev.done, total: ev.total })
                : t("onboarding.syncConnecting")}
            </p>
          </>
        )}
      </div>
      <div className="flex shrink-0 flex-col-reverse gap-2 border-t border-border px-6 py-3.5 sm:flex-row sm:justify-end">
        <Button type="button" variant="ghost" onClick={onSkip}>
          {t("common.skip")}
        </Button>
        {ev?.type === "sync.error" ? (
          <Button onClick={onRetry}>{t("common.retry")}</Button>
        ) : (
          <Button
            onClick={onNext}
            disabled={ev?.type === "sync.started" || ev?.type === "sync.progress"}
          >
            {ev?.type === "sync.completed"
              ? t("onboarding.continue")
              : t("onboarding.skipAndContinue")}
          </Button>
        )}
      </div>
    </>
  )
}

function DoneStep({ onFinish }: { onFinish: () => void }) {
  const t = useT()
  return (
    <div className="flex flex-col items-center gap-5 text-center">
      <div className="flex size-14 items-center justify-center rounded-2xl bg-primary/10">
        <Mail className="size-7 text-primary" />
      </div>
      <DialogHeader className="items-center gap-2">
        <DialogTitle className="text-lg">{t("onboarding.doneTitle")}</DialogTitle>
        <DialogDescription className="max-w-sm">{t("onboarding.doneDesc")}</DialogDescription>
      </DialogHeader>
      <ul className="flex flex-col gap-2 text-left text-sm">
        <li className="flex items-center gap-2.5">
          <Kbd>j</Kbd>
          <Kbd>k</Kbd>
          <span className="text-muted-foreground">{t("onboarding.doneScroll")}</span>
        </li>
        <li className="flex items-center gap-2.5">
          <Kbd>r</Kbd>
          <span className="text-muted-foreground">{t("onboarding.doneCompose")}</span>
        </li>
        <li className="flex items-center gap-2.5">
          <Kbd>a</Kbd>
          <span className="text-muted-foreground">{t("onboarding.doneArchive")}</span>
        </li>
      </ul>
      <p className="text-xs text-muted-foreground">{t("onboarding.doneSettings")}</p>
      <div className="mt-1 flex w-full flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <Button onClick={onFinish}>{t("onboarding.openMail")}</Button>
      </div>
    </div>
  )
}
