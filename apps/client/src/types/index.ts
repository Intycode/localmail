/**
 * Frontend types. These re-export the shared domain model so the UI only ever
 * deals with the high-level Mail model — never SQLite/IMAP/SMTP internals.
 */
export * from "@mail/shared-types"
