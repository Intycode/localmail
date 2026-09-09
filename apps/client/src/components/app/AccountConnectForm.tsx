import * as React from "react"
import type { SubmitEvent } from "react"
import { Eye, EyeOff, TriangleAlert } from "lucide-react"
import { toast } from "sonner"

import { getMailService } from "@/services/serviceRegistry"
import { isTauri } from "@/services/tauriMailService"
import { t, useT } from "@/lib/i18n"
import type { Account, ImapAccountDraft, ImapConfig, SmtpConfig } from "@/types"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"

interface AccountConnectFormProps {
  /** id of the <form>: lets an external footer button submit it (`form=` attr). */
  formId?: string
  /** classes for the scrollable body (the <form> element itself) */
  bodyClassName?: string
  /**
   * Called after an account was added successfully (OAuth or manual).
   * The caller decides what happens next: close the dialog, advance the
   * onboarding wizard, refresh stores… The form never navigates.
   */
  onConnected: () => void
  /**
   * Pinned footer, rendered after the scrollable body. Receives the
   * submitting flag; the standard submit button is
   * `<Button type="submit" form={formId}>`.
   */
  footer?: (submitting: boolean) => React.ReactNode
}

/**
 * AccountConnectForm — the full "connect an account" flow as a reusable
 * form: Google OAuth, Microsoft device-code and manual IMAP/JMAP.
 *
 * Extracted from the AccountsPage dialog so both the classic dialog and the
 * first-run onboarding wizard share the same logic (OAuth handling, smart
 * prefill, SMTP defaults, validation). The secret goes to the OS keychain
 * via the engine; nothing is stored in the webview.
 */
export function AccountConnectForm({
  formId = "add-account-form",
  bodyClassName = "min-h-0 flex-1 overflow-y-auto px-6 py-5",
  onConnected,
  footer,
}: AccountConnectFormProps) {
  const t = useT()
  const [form, setForm] = React.useState<FormState>(emptyForm)
  const [errors, setErrors] = React.useState<Partial<Record<keyof FormState, string>>>({})
  const [submitting, setSubmitting] = React.useState(false)
  const [googleStarting, setGoogleStarting] = React.useState(false)
  const [microsoftStarting, setMicrosoftStarting] = React.useState(false)
  const [showPassword, setShowPassword] = React.useState(false)
  /** last engine error, shown inline inside the dialog: a 4s toast is too
   *  easy to miss while the modal stays open on failure. */
  const [submitError, setSubmitError] = React.useState("")

  function set<K extends keyof FormState>(key: K, value: FormState[K]): void {
    setForm((f) => {
      const next = { ...f, [key]: value }
      // Smart prefill: from the email derive username and IMAP host (only in
      // still-empty fields, so manual edits are never stomped).
      if (key === "email") {
        const email = String(value).trim()
        const at = email.indexOf("@")
        if (at > 0 && email.indexOf(".", at) > at + 1) {
          const domain = email.slice(at + 1)
          if (!f.username.trim()) next.username = email
          if (next.kind === "imap" && !f.host.trim()) next.host = `imap.${domain}`
        }
      }
      if (key === "kind") {
        // switching server type clears host-derived prefill mismatches
        next.smtpAdvanced = false
      }
      // Standard ports follow the chosen security.
      if (key === "security") next.port = value === "tls" ? "993" : "143"
      if (key === "smtpSecurity") {
        next.smtpPort = value === "tls" ? "465" : value === "starttls" ? "587" : "25"
      }
      return next
    })
  }

  async function submit(e: SubmitEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    setSubmitError("")
    const found = validate(form)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    if (!isTauri()) {
      toast.error(t("connect.browserOnly"))
      return
    }
    setSubmitting(true)
    try {
      const svc = await getMailService()
      const admin = svc as AccountAdmin
      if (!admin.addAccount) throw new Error(t("common.engineOnly"))
      if (form.kind === "jmap") {
        // JMAP: Imap.Host carries the session URL; sending goes through
        // EmailSubmission, so no SMTP config is sent
        await admin.addAccount({
          name: form.name.trim(),
          email: form.email.trim(),
          imap: {
            host: form.host.trim(),
            port: 443,
            security: "tls",
            username: form.username.trim(),
          },
          secret: form.password,
          provider: "jmap",
        })
      } else {
        await admin.addAccount({
          name: form.name.trim(),
          email: form.email.trim(),
          imap: {
            host: form.host.trim(),
            port: Number(form.port),
            security: form.security,
            username: form.username.trim(),
          },
          ...(form.smtpAdvanced && form.smtpHost.trim()
            ? {
                smtp: {
                  host: form.smtpHost.trim(),
                  port: Number(form.smtpPort) || 465,
                  security: form.smtpSecurity,
                  username: form.smtpUsername.trim(),
                },
              }
            : {}),
          secret: form.password,
          allowInsecure: form.security === "plain",
        })
      }
      toast.success(t("connect.added"))
      // clear the password field immediately: the secret lives in the keychain
      setForm(emptyForm)
      setErrors({})
      onConnected()
    } catch (err) {
      const message = err instanceof Error ? err.message : t("connect.addFailed")
      setSubmitError(message)
      // long duration: the exact server error is the only diagnostic the
      // user has (wrong host, auth failure, blocked port…)
      toast.error(message, { duration: 10_000 })
    } finally {
      setSubmitting(false)
    }
  }

  /** Microsoft (Outlook) OAuth2: device-code flow. The browser is already
   *  open on devicelogin: the toast shows the code to type while the wait
   *  command polls until the user finishes. */
  async function submitMicrosoft(): Promise<void> {
    if (!isTauri()) {
      toast.error(t("connect.microsoftDesktopOnly"))
      return
    }
    setMicrosoftStarting(true)
    setSubmitError("")
    try {
      const svc = await getMailService()
      if (!svc.oauthMicrosoftBegin) throw new Error(t("common.engineOnly"))
      const begin = await svc.oauthMicrosoftBegin()
      if (!svc.oauthMicrosoftWait) throw new Error(t("common.engineOnly"))
      toast.info(t("connect.deviceCode", { code: begin.userCode }), {
        description: t("connect.deviceCodeDesc", { uri: begin.verificationUri }),
        duration: 600_000,
      })
      const { email, refreshToken } = await svc.oauthMicrosoftWait(
        begin.deviceCode,
        begin.intervalSecs,
      )
      const admin = svc as AccountAdmin
      if (!admin.addAccount) throw new Error(t("common.engineOnly"))
      const draft: ImapAccountDraft = {
        name: email,
        email,
        imap: {
          host: "https://graph.microsoft.com",
          port: 443,
          security: "tls",
          username: email,
          auth: "oauth",
        },
        secret: refreshToken,
        provider: "microsoft",
      }
      await admin.addAccount(draft)
      toast.success(t("connect.microsoftConnected"))
      onConnected()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t("connect.microsoftFailed")
      setSubmitError(message)
      toast.error(message, { duration: 10_000 })
    } finally {
      setMicrosoftStarting(false)
    }
  }

  /** Google (Gmail) OAuth2: browser consent → refresh token in the keychain,
   *  XOAUTH2 for IMAP/SMTP. Blocks until the user completes the consent. */
  async function submitGoogle(): Promise<void> {
    if (!isTauri()) {
      toast.error(t("connect.googleDesktopOnly"))
      return
    }
    setGoogleStarting(true)
    setSubmitError("")
    try {
      const svc = await getMailService()
      if (!svc.oauthGoogleStart) throw new Error(t("common.engineOnly"))
      const { email, refreshToken } = await svc.oauthGoogleStart()
      const admin = svc as AccountAdmin
      if (!admin.addAccount) throw new Error(t("common.engineOnly"))
      const draft: ImapAccountDraft = {
        name: email,
        email,
        imap: {
          host: "imap.gmail.com",
          port: 993,
          security: "tls",
          username: email,
          auth: "oauth",
        },
        smtp: {
          host: "smtp.gmail.com",
          port: 465,
          security: "tls",
          username: email,
          auth: "oauth",
        },
        secret: refreshToken,
        provider: "gmail-oauth",
      }
      await admin.addAccount(draft)
      toast.success(t("connect.googleConnected"))
      onConnected()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t("connect.googleFailed")
      setSubmitError(message)
      toast.error(message, { duration: 10_000 })
    } finally {
      setGoogleStarting(false)
    }
  }

  const id = (suffix: string): string => `${formId}-${suffix}`

  return (
    <>
      <form id={formId} className={bodyClassName} onSubmit={(e) => void submit(e)}>
        <FieldGroup className="gap-4">
          {submitError && (
            <Alert variant="destructive" role="alert">
              <TriangleAlert />
              <AlertTitle>{t("connect.errorTitle")}</AlertTitle>
              <AlertDescription className="break-words">{submitError}</AlertDescription>
            </Alert>
          )}
          <div className="flex items-center justify-between gap-4 rounded-lg border border-border bg-muted/30 px-3.5 py-3">
            <div className="min-w-0">
              <p className="text-sm font-medium">{t("connect.gmail")}</p>
              <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
                {t("connect.gmailDesc")}
              </p>
            </div>
            <Button
              type="button"
              variant="outline"
              disabled={googleStarting || submitting}
              onClick={() => void submitGoogle()}
            >
              {googleStarting ? t("connect.googleWait") : t("connect.googleCta")}
            </Button>
          </div>
          <div className="flex items-center justify-between gap-4 rounded-lg border border-border bg-muted/30 px-3.5 py-3">
            <div className="min-w-0">
              <p className="text-sm font-medium">{t("connect.microsoft")}</p>
              <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
                {t("connect.microsoftDesc")}
              </p>
            </div>
            <Button
              type="button"
              variant="outline"
              disabled={microsoftStarting || googleStarting || submitting}
              onClick={() => void submitMicrosoft()}
            >
              {microsoftStarting ? t("connect.microsoftWait") : t("connect.microsoftCta")}
            </Button>
          </div>
          <div className="flex items-center justify-between gap-3">
            <p className="text-xs text-muted-foreground">{t("connect.orManual")}</p>
            <div className="flex items-center gap-1 rounded-lg border border-border p-0.5">
              <Button
                type="button"
                size="sm"
                variant={form.kind === "imap" ? "secondary" : "ghost"}
                onClick={() => set("kind", "imap")}
              >
                IMAP
              </Button>
              <Button
                type="button"
                size="sm"
                variant={form.kind === "jmap" ? "secondary" : "ghost"}
                onClick={() => set("kind", "jmap")}
              >
                JMAP
              </Button>
            </div>
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Field data-invalid={errors.name ? true : undefined}>
              <FieldLabel htmlFor={id("name")}>{t("connect.name")}</FieldLabel>
              <Input
                id={id("name")}
                value={form.name}
                onChange={(e) => set("name", e.target.value)}
                placeholder="Personale"
                autoComplete="off"
              />
              {errors.name && <FieldError>{errors.name}</FieldError>}
            </Field>
            <Field data-invalid={errors.email ? true : undefined}>
              <FieldLabel htmlFor={id("email")}>{t("connect.email")}</FieldLabel>
              <Input
                id={id("email")}
                type="email"
                value={form.email}
                onChange={(e) => set("email", e.target.value)}
                placeholder="luca@esempio.it"
                autoComplete="off"
              />
              {errors.email && <FieldError>{errors.email}</FieldError>}
            </Field>
          </div>

          {form.kind === "jmap" ? (
            <p className="mt-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
              {t("connect.jmapServer")}
            </p>
          ) : (
            <p className="mt-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
              {t("connect.imapServer")}
            </p>
          )}

          <div className="grid grid-cols-3 gap-3">
            <Field
              className={form.kind === "jmap" ? "col-span-3" : "col-span-2"}
              data-invalid={errors.host ? true : undefined}
            >
              <FieldLabel htmlFor={id("host")}>
                {form.kind === "jmap" ? t("connect.sessionUrl") : t("connect.server")}
              </FieldLabel>
              <Input
                id={id("host")}
                value={form.host}
                onChange={(e) => set("host", e.target.value)}
                placeholder={
                  form.kind === "jmap" ? "https://api.fastmail.com/jmap/session" : "imap.esempio.it"
                }
                autoComplete="off"
                spellCheck={false}
              />
              {errors.host && <FieldError>{errors.host}</FieldError>}
            </Field>
            {form.kind === "imap" && (
              <Field data-invalid={errors.port ? true : undefined}>
                <FieldLabel htmlFor={id("port")}>{t("connect.port")}</FieldLabel>
                <Input
                  id={id("port")}
                  inputMode="numeric"
                  value={form.port}
                  onChange={(e) => set("port", e.target.value.replace(/[^0-9]/g, ""))}
                  placeholder="993"
                />
                {errors.port && <FieldError>{errors.port}</FieldError>}
              </Field>
            )}
          </div>

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {form.kind === "imap" && (
              <Field data-invalid={errors.security ? true : undefined}>
                <FieldLabel htmlFor={id("security")}>{t("connect.security")}</FieldLabel>
                <Select
                  value={form.security}
                  onValueChange={(v) => set("security", v as ImapConfig["security"])}
                >
                  <SelectTrigger id={id("security")} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="tls">{t("connect.tls")}</SelectItem>
                    <SelectItem value="starttls">{t("connect.starttls")}</SelectItem>
                    <SelectItem value="plain">{t("connect.plain")}</SelectItem>
                  </SelectContent>
                </Select>
                {form.security === "plain" && (
                  <FieldDescription className="text-destructive">
                    {t("connect.plainWarning")}
                  </FieldDescription>
                )}
              </Field>
            )}
            <Field data-invalid={errors.username ? true : undefined}>
              <FieldLabel htmlFor={id("username")}>{t("connect.username")}</FieldLabel>
              <Input
                id={id("username")}
                value={form.username}
                onChange={(e) => set("username", e.target.value)}
                placeholder="luca@esempio.it"
                autoComplete="off"
                spellCheck={false}
              />
              {errors.username && <FieldError>{errors.username}</FieldError>}
            </Field>
          </div>

          <Field data-invalid={errors.password ? true : undefined}>
            <FieldLabel htmlFor={id("password")}>
              {form.kind === "jmap" ? t("connect.token") : t("connect.password")}
            </FieldLabel>
            <div className="relative">
              <Input
                id={id("password")}
                type={showPassword ? "text" : "password"}
                value={form.password}
                onChange={(e) => set("password", e.target.value)}
                autoComplete="off"
                className="pr-9"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="absolute top-1/2 right-1.5 -translate-y-1/2"
                aria-label={showPassword ? t("connect.hidePassword") : t("connect.showPassword")}
                title={showPassword ? t("connect.hidePassword") : t("connect.showPassword")}
                onClick={() => setShowPassword((v) => !v)}
              >
                {showPassword ? <EyeOff /> : <Eye />}
              </Button>
            </div>
            <FieldDescription>{t("connect.passwordKeychain")}</FieldDescription>
            {errors.password && <FieldError>{errors.password}</FieldError>}
          </Field>

          {form.kind === "imap" && (
            <div className="flex items-center justify-between gap-4 rounded-lg border border-border bg-muted/30 px-3.5 py-3">
              <div className="min-w-0">
                <FieldLabel htmlFor={id("smtp-advanced")} className="font-normal">
                  {t("connect.smtpCustom")}
                </FieldLabel>
                <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
                  {form.smtpAdvanced ? t("connect.smtpManual") : t("connect.smtpAuto")}
                </p>
              </div>
              <Switch
                id={id("smtp-advanced")}
                checked={form.smtpAdvanced}
                onCheckedChange={(v) => {
                  if (v) {
                    setForm((f) => ({ ...f, smtpAdvanced: true, ...smtpDefaults(f) }))
                  } else {
                    setForm((f) => ({
                      ...f,
                      smtpAdvanced: false,
                      smtpHost: "",
                      smtpPort: "465",
                      smtpSecurity: "tls",
                      smtpUsername: "",
                    }))
                  }
                }}
              />
            </div>
          )}

          {form.kind === "imap" && form.smtpAdvanced && (
            <div className="mt-1 flex flex-col gap-4">
              <div className="grid grid-cols-3 gap-3">
                <Field className="col-span-2">
                  <FieldLabel htmlFor={id("smtp-host")}>{t("connect.smtpServer")}</FieldLabel>
                  <Input
                    id={id("smtp-host")}
                    value={form.smtpHost}
                    onChange={(e) => set("smtpHost", e.target.value)}
                    placeholder="smtp.esempio.it"
                    autoComplete="off"
                    spellCheck={false}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor={id("smtp-port")}>{t("connect.port")}</FieldLabel>
                  <Input
                    id={id("smtp-port")}
                    inputMode="numeric"
                    value={form.smtpPort}
                    onChange={(e) => set("smtpPort", e.target.value.replace(/[^0-9]/g, ""))}
                    placeholder="465"
                  />
                </Field>
              </div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor={id("smtp-security")}>{t("connect.smtpSecurity")}</FieldLabel>
                  <Select
                    value={form.smtpSecurity}
                    onValueChange={(v) => set("smtpSecurity", v as SmtpConfig["security"])}
                  >
                    <SelectTrigger id={id("smtp-security")} className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="tls">{t("connect.tls")}</SelectItem>
                      <SelectItem value="starttls">{t("connect.starttls")}</SelectItem>
                      <SelectItem value="plain">{t("connect.plain")}</SelectItem>
                    </SelectContent>
                  </Select>
                </Field>
                <Field>
                  <FieldLabel htmlFor={id("smtp-username")}>{t("connect.smtpUsername")}</FieldLabel>
                  <Input
                    id={id("smtp-username")}
                    value={form.smtpUsername}
                    onChange={(e) => set("smtpUsername", e.target.value)}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </Field>
              </div>
              <FieldDescription>{t("connect.smtpPasswordNote")}</FieldDescription>
            </div>
          )}
          {form.kind === "jmap" && <FieldDescription>{t("connect.jmapNote")}</FieldDescription>}
        </FieldGroup>
      </form>
      {footer?.(submitting)}
    </>
  )
}

type AccountAdmin = {
  addAccount?: (draft: {
    name: string
    email: string
    imap: ImapConfig
    smtp?: SmtpConfig
    secret: string
    allowInsecure?: boolean
    provider?: string
  }) => Promise<Account>
  removeAccount?: (accountId: string) => Promise<void>
  setActive?: (accountId: string) => Promise<void>
}

interface FormState {
  /** Server type: classic IMAP or JMAP (Fastmail/Stalwart). */
  kind: "imap" | "jmap"
  name: string
  email: string
  host: string
  port: string
  security: ImapConfig["security"]
  username: string
  password: string
  // outgoing SMTP (optional): empty fields are derived by the engine from IMAP
  smtpAdvanced: boolean
  smtpHost: string
  smtpPort: string
  smtpSecurity: SmtpConfig["security"]
  smtpUsername: string
}

const emptyForm: FormState = {
  kind: "imap",
  name: "",
  email: "",
  host: "",
  port: "993",
  security: "tls",
  username: "",
  password: "",
  smtpAdvanced: false,
  smtpHost: "",
  smtpPort: "465",
  smtpSecurity: "tls",
  smtpUsername: "",
}

/** Derives the precompiled SMTP defaults from the IMAP form values. */
function smtpDefaults(f: FormState): Partial<FormState> {
  const host = f.host.trim()
  const derived = host.includes(".") ? `smtp.${host.slice(host.indexOf(".") + 1)}` : `smtp.${host}`
  return {
    smtpHost: f.smtpHost || derived,
    smtpPort: f.smtpPort || "465",
    smtpUsername: f.smtpUsername || f.username,
  }
}

function validate(form: FormState): Partial<Record<keyof FormState, string>> {
  const errors: Partial<Record<keyof FormState, string>> = {}
  if (!form.name.trim()) errors.name = t("connect.errName")
  if (!form.email.trim()) errors.email = t("connect.errEmail")
  const host = form.host.trim()
  if (form.kind === "jmap") {
    // JMAP: the "host" field carries the session URL
    if (!host) errors.host = t("connect.errJmapUrl")
    else if (!/^https:\/\/.+/.test(host)) errors.host = t("connect.errJmapHttps")
  } else {
    if (!host) errors.host = t("connect.errImapHost")
    else if (/^[a-z]+:/i.test(host)) errors.host = t("connect.errHostSchema")
    else if (/[\s\\]/.test(host) || host.includes("/")) errors.host = t("connect.errHostInvalid")
    const port = Number(form.port)
    if (!form.port || !Number.isInteger(port) || port < 1 || port > 65535)
      errors.port = t("connect.errPort")
  }
  if (!form.username.trim()) errors.username = t("connect.errUsername")
  if (!form.password)
    errors.password = form.kind === "jmap" ? t("connect.errToken") : t("connect.errPassword")
  return errors
}
