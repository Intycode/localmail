// Package domain holds the shared mail data model. It mirrors the TypeScript
// types in packages/shared-types and is the contract between the Go engine,
// the Tauri bridge and the UI. No package here leaks IMAP/SQLite internals.
package domain

import "time"

type Recipient struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Attachment struct {
	ID          string `json:"id"`
	MessageID   string `json:"messageId"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType"`
}

type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type Folder struct {
	ID      string `json:"id"`
	Account string `json:"accountId"`
	Name    string `json:"name"`
	Type    string `json:"type"` // inbox | sent | archive | drafts | trash | custom
	Total   int    `json:"total"`
	Unread  int    `json:"unread"`
}

type Account struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Email    string      `json:"email"`
	Provider string      `json:"provider"` // gmail | microsoft | generic-imap | mock
	Active   bool        `json:"isActive"`
	Folders  []Folder    `json:"folders"`
	Imap     *ImapConfig `json:"imap,omitempty"` // non-secret config; password never here
	Smtp     *SmtpConfig `json:"smtp,omitempty"` // optional outgoing server; empty = derived
}

// SmtpConfig is the non-secret SMTP connection configuration (outgoing mail).
// The password is the account secret (keychain), never persisted. Empty
// fields are derived from the IMAP config at send time.
type SmtpConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"` // tls | starttls | plain
	Username string `json:"username"`
	// Auth selects the SASL mechanism: "password" (default, LOGIN/PLAIN)
	// or "oauth" (XOAUTH2, the secret is an access/refresh token).
	Auth string `json:"auth,omitempty"`
}

// ImapConfig is the non-secret IMAP connection configuration. The password
// lives in the OS keychain and is passed to the engine in memory only.
type ImapConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"` // tls | starttls | plain
	Username string `json:"username"`
	// Auth selects the SASL mechanism: "password" (default, LOGIN/PLAIN)
	// or "oauth" (XOAUTH2, the secret is an access/refresh token).
	Auth string `json:"auth,omitempty"`
}

type Thread struct {
	ID           string      `json:"id"`
	FolderID     string      `json:"folderId"`
	AccountID    string      `json:"accountId"`
	Subject      string      `json:"subject"`
	MessageIDs   []string    `json:"messageIds"`
	From         Recipient   `json:"from"`
	To           []Recipient `json:"to"`
	Snippet      string      `json:"snippet"`
	Date         time.Time   `json:"date"`
	IsUnread     bool        `json:"isUnread"`
	IsStarred    bool        `json:"isStarred"`
	Labels       []Label     `json:"labels"`
	MessageCount int         `json:"messageCount"`
	HasAttach    bool        `json:"hasAttachments"`
}

type Message struct {
	ID        string      `json:"id"`
	ThreadID  string      `json:"threadId"`
	FolderID  string      `json:"folderId"`
	AccountID string      `json:"accountId"`
	Subject   string      `json:"subject"`
	From      Recipient   `json:"from"`
	To        []Recipient `json:"to"`
	CC        []Recipient `json:"cc"`
	BCC       []Recipient `json:"bcc"`
	Date      time.Time   `json:"date"`
	Body      string      `json:"body"`
	BodyText  string      `json:"bodyText"`
	// RemoteImages is the number of remote <img> the anti-tracking sanitizer
	// left in the HTML body: the UI gates them behind explicit consent.
	RemoteImages int `json:"remoteImages"`
	// SpamScore (0..100, local heuristics M7) and Phishing flags computed at
	// ingest; the UI surfaces them as warnings, nothing moves automatically.
	SpamScore float64  `json:"spamScore"`
	Phishing  []string `json:"phishing"`
	// PGP (M9): IsEncrypted marks a message whose body is (or was) encrypted;
	// IsSigned + SigStatus (valid | invalid | unknown) for signed messages.
	IsEncrypted       bool         `json:"isEncrypted"`
	IsSigned          bool         `json:"isSigned"`
	SigStatus         string       `json:"sigStatus,omitempty"`
	IsUnread          bool         `json:"isUnread"`
	IsStarred         bool         `json:"isStarred"`
	Labels            []Label      `json:"labels"`
	Attachments       []Attachment `json:"attachments"`
	ProviderMessageID string       `json:"providerMessageId,omitempty"`

	// threading metadata (RFC 5322); not exposed to the UI
	MessageID  string   `json:"messageId,omitempty"`
	InReplyTo  []string `json:"inReplyTo,omitempty"`
	References []string `json:"references,omitempty"`
}

type SearchQuery struct {
	Query     string    `json:"query"`
	FolderID  string    `json:"folderId,omitempty"`
	AccountID string    `json:"accountId,omitempty"`
	Before    time.Time `json:"before,omitempty"`
	After     time.Time `json:"after,omitempty"`
	// Mode: "keyword" (default) or "semantic" (embedding-based; falls back
	// to keyword when Ollama is unavailable or nothing is indexed).
	Mode string `json:"mode,omitempty"`
}

type SearchResult struct {
	ThreadID string    `json:"threadId"`
	Subject  string    `json:"subject"`
	Snippet  string    `json:"snippet"`
	Matched  string    `json:"matchedOn"` // subject | sender | recipient | body | semantic
	Date     time.Time `json:"date"`
	// Score: cosine similarity 0..1, semantic mode only.
	Score float32 `json:"score,omitempty"`
}

type ComposeMessage struct {
	To      []Recipient `json:"to"`
	CC      []Recipient `json:"cc"`
	BCC     []Recipient `json:"bcc"`
	Subject string      `json:"subject"`
	Body    string      `json:"body"`
	// HTMLBody is the optional HTML version of Body: when set the message is
	// composed as multipart/alternative (text part = Body, html part = HTMLBody),
	// so replies keep the quoted message in its original HTML form.
	HTMLBody    string       `json:"htmlBody,omitempty"`
	Attachments []Attachment `json:"attachments"`

	// Threading headers for replies (RFC 5322). Optional: set by the UI when
	// composing a reply so the sent message joins the existing thread.
	InReplyTo  string   `json:"inReplyTo,omitempty"`
	References []string `json:"references,omitempty"`

	// Sign/Encrypt (M9): OpenPGP on the text part (PGP/Inline). Encrypting
	// requires a stored public key for every recipient and sends text-only
	// (no HTML) so no plaintext leaks; with attachments it is refused.
	Sign    bool `json:"sign,omitempty"`
	Encrypt bool `json:"encrypt,omitempty"`

	// Files are outgoing attachments (new send path): referenced by a
	// client-chosen path so the content never round-trips the IPC bridge.
	// The engine reads each file at send time (smtp.LoadFiles) and embeds
	// it into the composed MIME message.
	Files []OutgoingFile `json:"files,omitempty"`

	// SendAt schedules the send (undo-send window or deferred delivery):
	// RFC 3339 timestamp. When in the future the engine parks the message
	// in the outbox queue and the worker delivers it at that time; the
	// user can cancel via outbox.cancel while it is pending.
	SendAt string `json:"sendAt,omitempty"`
}

// OutgoingFile is one file to attach to an outgoing message. Path is
// absolute and chosen by the user via the native save/open dialog. Data is
// filled in memory by smtp.LoadFiles and is never part of the wire format
// (json "-"), so queued outbox payloads only ever contain paths.
type OutgoingFile struct {
	Path        string `json:"path"`
	Name        string `json:"name,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Data        []byte `json:"-"`
}

// Contact is one address seen in the local mail history (compose
// autocomplete). Name may be empty; Email is the identity.
type Contact struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// Rule is a local mail rule (M8): one condition matched against incoming
// messages, one action applied in position order. Empty Account applies the
// rule to every account. Fields: from, to, subject, body, spam_score,
// has_attachment. Ops: contains, equals, starts_with, ends_with, at_least,
// any. Actions: move (Target = local folder id), mark_read, star, mark_spam.
type Rule struct {
	ID       string `json:"id"`
	Account  string `json:"accountId,omitempty"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Position int    `json:"position"`
	Field    string `json:"field"`
	Op       string `json:"op"`
	Value    string `json:"value"`
	Action   string `json:"action"`
	Target   string `json:"target,omitempty"`
}
