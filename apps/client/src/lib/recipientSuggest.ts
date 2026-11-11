import type { Contact } from "../types"

/** The entry being typed: what follows the last comma of a recipients field. */
export function lastEntry(value: string): string {
  return value.slice(value.lastIndexOf(",") + 1).trim()
}

/** Replaces the entry being typed with the picked contact, ready for the
 *  next address. The name is dropped when it would break the comma-separated
 *  format the composer parses. */
export function applySuggestion(value: string, c: Contact): string {
  const head = value.slice(0, value.lastIndexOf(",") + 1)
  const name = c.name && !/[,<>]/.test(c.name) && c.name !== c.email ? c.name.trim() : ""
  const entry = name ? `${name} <${c.email}>` : c.email
  return `${head ? `${head} ` : ""}${entry}, `
}
