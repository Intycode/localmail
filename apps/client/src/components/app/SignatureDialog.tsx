import * as React from "react"
import { toast } from "sonner"

import {
  fileToSignatureImage,
  getSignature,
  MAX_IMAGE_WIDTH,
  setSignature,
  type Signature,
  type SignatureImagePosition,
} from "@/lib/signature"
import { signatureHtml } from "@/lib/composeQuote"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useT } from "@/lib/i18n"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"

const POSITIONS: SignatureImagePosition[] = ["above", "below", "left", "right"]

interface SignatureDialogProps {
  accountId: string
  name: string
  trigger: React.ReactNode
}

/**
 * SignatureDialog — edit the per-account signature (text + optional image).
 * Stored device-local; the composer appends it to every outgoing mail.
 */
export function SignatureDialog({ accountId, name, trigger }: SignatureDialogProps) {
  const t = useT()
  const [open, setOpen] = React.useState(false)
  const [text, setText] = React.useState("")
  const [image, setImage] = React.useState<string | undefined>()
  const [position, setPosition] = React.useState<SignatureImagePosition>("below")
  const [width, setWidth] = React.useState(240)
  const fileRef = React.useRef<HTMLInputElement>(null)

  function onOpenChange(next: boolean): void {
    if (next) {
      const sig = getSignature(accountId)
      setText(sig?.text ?? "")
      setImage(sig?.image)
      setPosition(sig?.imagePosition ?? "below")
      setWidth(sig?.imageWidth ?? 240)
    }
    setOpen(next)
  }

  async function pick(e: React.ChangeEvent<HTMLInputElement>): Promise<void> {
    const file = e.target.files?.[0]
    e.target.value = "" // picking the same file again must fire onChange
    if (!file) return
    try {
      const picked = await fileToSignatureImage(file)
      setImage(picked.image)
      setWidth(Math.min(picked.width, 240))
    } catch {
      toast.error(t("signature.imageFailed"))
    }
  }

  const draft: Signature = {
    text,
    ...(image ? { image, imagePosition: position, imageWidth: width } : {}),
  }

  function save(): void {
    try {
      setSignature(accountId, draft)
      toast.success(t("signature.saved"))
      setOpen(false)
    } catch {
      toast.error(t("signature.saveFailed"))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("signature.title", { name })}</DialogTitle>
          <DialogDescription>{t("signature.hint")}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <label
            htmlFor={`sig-text-${accountId}`}
            className="text-xs font-medium text-muted-foreground"
          >
            {t("signature.textLabel")}
          </label>
          <Textarea
            id={`sig-text-${accountId}`}
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={t("signature.textPlaceholder")}
            className="min-h-24"
          />
        </div>

        <div className="flex flex-col gap-2">
          <span className="text-xs font-medium text-muted-foreground">{t("signature.image")}</span>
          <div className="flex gap-2">
            <input
              ref={fileRef}
              type="file"
              accept="image/png,image/jpeg,image/gif,image/webp"
              className="hidden"
              onChange={(e) => void pick(e)}
            />
            <Button variant="outline" size="sm" onClick={() => fileRef.current?.click()}>
              {image ? t("signature.changeImage") : t("signature.addImage")}
            </Button>
            {image && (
              <Button variant="ghost" size="sm" onClick={() => setImage(undefined)}>
                {t("common.remove")}
              </Button>
            )}
          </div>
        </div>

        {image && (
          <>
            <div className="flex flex-col gap-2">
              <span className="text-xs font-medium text-muted-foreground">
                {t("signature.position")}
              </span>
              <ToggleGroup
                type="single"
                variant="outline"
                size="sm"
                value={position}
                onValueChange={(v) => v && setPosition(v as SignatureImagePosition)}
              >
                {POSITIONS.map((p) => (
                  <ToggleGroupItem key={p} value={p}>
                    {t(`signature.position.${p}`)}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            </div>
            <div className="flex flex-col gap-2">
              <label
                htmlFor={`sig-width-${accountId}`}
                className="text-xs font-medium text-muted-foreground"
              >
                {t("signature.width", { px: width })}
              </label>
              <input
                id={`sig-width-${accountId}`}
                type="range"
                min={24}
                max={MAX_IMAGE_WIDTH}
                step={4}
                value={width}
                onChange={(e) => setWidth(Number(e.target.value))}
                className="accent-primary"
              />
            </div>
          </>
        )}

        {(text.trim() || image) && (
          <div className="flex flex-col gap-2">
            <span className="text-xs font-medium text-muted-foreground">
              {t("signature.preview")}
            </span>
            <div
              className="overflow-x-auto rounded-md border bg-white p-3 text-sm text-black"
              // own markup: text escaped and image validated by signatureHtml
              dangerouslySetInnerHTML={{ __html: signatureHtml(draft) }}
            />
          </div>
        )}

        <DialogFooter>
          <Button size="sm" onClick={save}>
            {t("signature.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
