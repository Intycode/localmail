// Outbox — offline-first replay machinery.
//
// When a queueable op (send, flag change, move, delete) fails because the
// mail server is unreachable, the request is parked in pending_ops with its
// ORIGINAL params and the UI receives {queued:true} instead of an error. The
// worker below replays due items every drainInterval; each op executes
// through the same handler that served it live, so replay behaviour is
// identical. Secrets are never persisted: handlers re-read the runtime vault.
package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"mail/backend/internal/queue"
)

// drainInterval is how often the worker looks for due operations.
const drainInterval = 5 * time.Second

// queueableOps — user-intent operations that may be parked and replayed.
var queueableOps = map[string]bool{
	"send":              true,
	"thread.markRead":   true,
	"thread.markUnread": true,
	"thread.archive":    true,
	"thread.move":       true,
	"message.delete":    true,
}

// isNetworkError classifies transport-level failures: only these mean
// "offline" (worth queuing + retrying). Auth errors, validation errors and
// engine bugs must surface to the user instead.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection refused", "connection reset", "connection closed",
		"no such host", "network is unreachable", "network unreachable",
		"i/o timeout", "dial tcp", "lookup ", "tls: handshake", "broken pipe",
		"unreachable", "timeout",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// queueAccount resolves the owning account for the pending_ops FK. Params
// carry accountId when the caller knew it; otherwise it is derived from the
// thread/message, and send falls back to the active account.
func queueAccount(app *App, op string, params json.RawMessage) string {
	var p struct {
		AccountID string `json:"accountId"`
		ThreadID  string `json:"threadId"`
		MessageID string `json:"messageId"`
	}
	_ = json.Unmarshal(params, &p)
	if p.AccountID != "" {
		return p.AccountID
	}
	switch {
	case strings.HasPrefix(op, "thread."):
		if p.ThreadID != "" {
			if id := threadAccount(app, p.ThreadID); id != "" {
				return id
			}
		}
	case strings.HasPrefix(op, "message."):
		if p.MessageID != "" {
			if accountID, _, _, _, err := app.Messages.ActionTarget(p.MessageID); err == nil {
				return accountID
			}
		}
	case op == "send":
		if accounts, err := app.Accounts.List(); err == nil {
			for _, a := range accounts {
				if a.Active {
					return a.ID
				}
			}
		}
	}
	return ""
}

// enqueueFailedOp parks one failed request and notifies the UI. Returns
// false when the op cannot be queued (no resolvable account, store missing).
func (s *Server) enqueueFailedOp(op string, params json.RawMessage) bool {
	app := s.app
	if app.Queue == nil {
		return false
	}
	accountID := queueAccount(app, op, params)
	if accountID == "" {
		return false
	}
	payload := stripSecret(params)
	if _, err := app.Queue.Enqueue(accountID, op, payload); err != nil {
		return false
	}
	s.setOnline(false)
	emitOutboxCount(app)
	return true
}

func emitOutboxCount(app *App) {
	if app.Events == nil || app.Queue == nil {
		return
	}
	if n, err := app.Queue.Count(); err == nil {
		app.Events.OutboxUpdated(n)
	}
}

// drainPass replays all due operations once through the regular handlers.
// It returns how many were processed and whether any transport failure was
// seen (i.e. the server is still unreachable).
func drainPass(app *App) (processed int, offlineSeen bool) {
	if app.Queue == nil {
		return 0, false
	}
	due, err := app.Queue.Due(time.Now(), 20)
	if err != nil || len(due) == 0 {
		return 0, false
	}
	for _, op := range due {
		handler, ok := ops[op.Kind]
		if !ok {
			// op no longer allowed by the protocol: drop it
			_ = app.Queue.Done(op.ID)
			continue
		}
		// claim first: an op cancelled (undo-send) since the snapshot is skipped
		if claimed, err := app.Queue.Claim(op.ID); err != nil || !claimed {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout(op.Kind))
		_, execErr := handler(ctx, app, json.RawMessage(op.Payload))
		cancel()
		processed++
		if execErr == nil {
			_ = app.Queue.Done(op.ID)
			continue
		}
		_ = app.Queue.Failed(op.ID, execErr, time.Now())
		if isNetworkError(execErr) {
			offlineSeen = true
		}
	}
	emitOutboxCount(app)
	return processed, offlineSeen
}

// setOnline emits connection.state on transitions only.
func (s *Server) setOnline(online bool) {
	if s.offline.Swap(!online) == !online {
		return // no transition
	}
	if s.app.Events != nil {
		s.app.Events.ConnectionState(online)
	}
}

// startOutboxWorker runs the drain loop until stop closes. The first pass
// runs immediately so items queued in a previous session replay right away.
func (s *Server) startOutboxWorker(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(drainInterval)
		defer ticker.Stop()
		if s.app.Queue != nil {
			_ = s.app.Queue.ReleaseClaims(time.Now())
		}
		s.drainOutbox()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.drainOutbox()
			case <-drainNow:
				s.drainOutbox()
			}
		}
	}()
}

func (s *Server) drainOutbox() {
	processed, offlineSeen := drainPass(s.app)
	if offlineSeen {
		s.setOnline(false)
		return
	}
	if processed > 0 {
		s.setOnline(true)
	}
}

// --- outbox ops ---

func opOutboxList(_ context.Context, app *App, _ json.RawMessage) (any, error) {
	if app.Queue == nil {
		return map[string]any{"pending": 0, "items": []queue.Op{}}, nil
	}
	items, err := app.Queue.List(50)
	if err != nil {
		return nil, err
	}
	n, _ := app.Queue.Count()
	return map[string]any{"pending": n, "items": items}, nil
}

func opOutboxCancel(_ context.Context, app *App, params json.RawMessage) (any, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := decode(params, &p); err != nil || p.ID == "" {
		return nil, fmt.Errorf("id mancante")
	}
	if err := app.Queue.Cancel(p.ID); err != nil {
		return nil, err
	}
	n, _ := app.Queue.Count()
	if app.Events != nil {
		app.Events.OutboxUpdated(n)
	}
	return map[string]int{"pending": n}, nil
}

// drainNow nudges the worker for an immediate replay pass. It is a channel
// (not a direct drainPass call) because opOutboxDrain is referenced by the
// ops map initializer and drainPass looks handlers up in that same map: a
// direct call would be an initialization cycle.
var drainNow = make(chan struct{}, 1)

// opOutboxDrain asks the worker for an immediate pass (the UI calls it when
// connectivity returns; the worker also drains on its own schedule). The
// ack is immediate: results arrive as outbox.updated / connection.state.
func opOutboxDrain(_ context.Context, _ *App, _ json.RawMessage) (any, error) {
	select {
	case drainNow <- struct{}{}:
	default: // a pass is already pending
	}
	return map[string]bool{"queued": true}, nil
}

// stripSecret drops the "secret" param before an op is persisted: secrets
// live only in the keychain and the runtime vault, never in pending_ops.
func stripSecret(params json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(params, &m) != nil || m == nil {
		return json.RawMessage("{}")
	}
	delete(m, "secret")
	out, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage("{}")
	}
	return out
}
