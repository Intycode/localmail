// Local-first server sync.
//
// Read/unread, archive/move and delete change the LOCAL database first and
// answer at once: the UI never waits for a (possibly slow or offline) mail
// server. The matching server change is recorded as a concrete command in
// pending_ops (srv.*) and replayed by the outbox worker, in creation order.
//
// Commands carry the mailbox and UIDs captured at the moment of the action,
// not the UI intent: after a local move the message rows already point at the
// new folder, so recomputing them at replay time would address the wrong
// UIDs. These kinds are NOT in the `ops` allowlist: the webview cannot call
// them.
package ipc

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"mail/backend/internal/provider"
	"mail/backend/internal/queue"
)

const (
	cmdFlags  = "srv.flags"
	cmdMove   = "srv.move"
	cmdDelete = "srv.delete"
)

// maxServerCmdAttempts: a command that keeps failing for a reason other than
// the network (message gone, folder renamed) is dropped after this many
// tries; the next folder sync reconciles the local copy with the server.
const maxServerCmdAttempts = 3

type serverCmd struct {
	AccountID string   `json:"accountId"`
	Mailbox   string   `json:"mailbox"`
	UIDs      []uint32 `json:"uids"`
	Target    string   `json:"target,omitempty"` // srv.move: destination mailbox
	Unread    bool     `json:"unread,omitempty"` // srv.flags: wanted state
}

func isServerCmd(kind string) bool { return strings.HasPrefix(kind, "srv.") }

// hasServer reports whether the account talks to a real mail server (mock
// and local-only accounts have nothing to sync).
func hasServer(app *App, accountID string) bool {
	var host string
	err := app.DB.QueryRow(`SELECT imap_host FROM accounts WHERE id=?`, accountID).Scan(&host)
	return err == nil && host != ""
}

// pushServerCmd records one server change. With a queue it is replayed by
// the outbox worker (nudged right away); without one (unit tests) it runs
// inline.
func pushServerCmd(app *App, kind string, c serverCmd) error {
	if len(c.UIDs) == 0 || c.Mailbox == "" || !hasServer(app, c.AccountID) {
		return nil
	}
	if app.Queue == nil {
		return runServerCmd(app, kind, c)
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err := app.Queue.Enqueue(c.AccountID, kind, payload); err != nil {
		return err
	}
	select {
	case drainNow <- struct{}{}:
	default: // a pass is already pending
	}
	return nil
}

func runServerCmd(app *App, kind string, c serverCmd) error {
	return withProvider(app, "", func(pv provider.MailProvider) error {
		switch kind {
		case cmdFlags:
			if c.Unread {
				return pv.MarkUnread(c.Mailbox, c.UIDs)
			}
			return pv.MarkRead(c.Mailbox, c.UIDs)
		case cmdMove:
			return pv.Move(c.Mailbox, c.UIDs, c.Target)
		case cmdDelete:
			return pv.Delete(c.Mailbox, c.UIDs)
		}
		return fmt.Errorf("comando server sconosciuto: %s", kind)
	}, c.AccountID)
}

// replayServerCmd executes one queued command.
func replayServerCmd(app *App, op queue.Op) error {
	var c serverCmd
	if err := json.Unmarshal([]byte(op.Payload), &c); err != nil {
		log.Printf("outbox: payload %s illeggibile, scartato: %v", op.ID, err)
		return nil // nothing to retry: report success so the row is deleted
	}
	return runServerCmd(app, op.Kind, c)
}
