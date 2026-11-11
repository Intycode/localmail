/**
 * lib/signature — per-account mail signature (text + optional image), stored
 * in localStorage under `mail.signatures` (device-local, like lib/avatar).
 * The composer appends it at send time; the engine turns the image data URL
 * into an inline cid part so recipients see it.
 */

export interface Signature {
  text: string
  /** PNG data URL, already downscaled by fileToSignatureImage. */
  image?: string
  /** Where the image sits relative to the text; omitted = "below". */
  imagePosition?: SignatureImagePosition
  /** Displayed width in px; omitted = the image's own width. */
  imageWidth?: number
}

export type SignatureImagePosition = "above" | "below" | "left" | "right"

const KEY = "mail.signatures"
export const MAX_IMAGE_WIDTH = 480

function read(): Record<string, Signature> {
  if (typeof window === "undefined") return {}
  try {
    return JSON.parse(window.localStorage.getItem(KEY) ?? "{}") as Record<string, Signature>
  } catch {
    return {}
  }
}

export function getSignature(accountId: string): Signature | null {
  return read()[accountId] ?? null
}

/** Saves (or, when empty, removes) the signature. Throws when storage is full. */
export function setSignature(accountId: string, sig: Signature): void {
  const next = read()
  if (sig.text.trim() || sig.image) next[accountId] = sig
  else delete next[accountId]
  window.localStorage.setItem(KEY, JSON.stringify(next))
}

/** Image file -> PNG data URL at most 480px wide (plus that width): keeps
 *  the storage and every sent mail small. */
export async function fileToSignatureImage(file: File): Promise<{ image: string; width: number }> {
  const bmp = await createImageBitmap(file)
  const scale = Math.min(1, MAX_IMAGE_WIDTH / bmp.width)
  const canvas = document.createElement("canvas")
  canvas.width = Math.round(bmp.width * scale)
  canvas.height = Math.round(bmp.height * scale)
  canvas.getContext("2d")!.drawImage(bmp, 0, 0, canvas.width, canvas.height)
  return { image: canvas.toDataURL("image/png"), width: canvas.width }
}
