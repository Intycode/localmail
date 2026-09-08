// Package events is a tiny in-process pub/sub bus. The sync engine publishes
// AppEvents; the Tauri bridge subscribes and forwards them to the UI. This
// keeps the UI decoupled from engine internals.
package events

import (
	"sync"
	"time"

	"mail/backend/internal/domain"
)

func nowISO() string { return time.Now().UTC().Format(time.RFC3339) }

// Event is the wire format between engine and UI.
type Event struct {
	Kind string `json:"kind"` // sync.started | ... | message.created | ...
	Data any    `json:"data"`
}

// Bus is a minimal publish/subscribe hub.
type Bus struct {
	mu   sync.RWMutex
	subs map[int]chan Event
	next int
}

func New() *Bus {
	return &Bus{subs: map[int]chan Event{}}
}

// Subscribe returns a channel that receives all published events.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan Event, 64)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, id)
		close(ch)
	}
}

// Publish sends an event to every subscriber.
func (b *Bus) Publish(kind string, data any) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e := Event{Kind: kind, Data: data}
	for _, ch := range b.subs {
		ch <- e
	}
}

// Convenience helpers for the standard event names.
func (b *Bus) SyncStarted(account string) {
	b.Publish("sync.started", map[string]string{"accountId": account})
}
func (b *Bus) SyncProgress(account string, done, total int) {
	b.Publish("sync.progress", map[string]any{"accountId": account, "done": done, "total": total})
}
func (b *Bus) SyncCompleted(account string) {
	b.Publish("sync.completed", map[string]any{"accountId": account, "at": nowISO()})
}
func (b *Bus) SyncError(account, msg string) {
	b.Publish("sync.error", map[string]string{"accountId": account, "message": msg})
}
func (b *Bus) MessageCreated(m domain.Message) { b.Publish("message.created", m) }
func (b *Bus) MessageUpdated(m domain.Message) { b.Publish("message.updated", m) }
func (b *Bus) MessageDeleted(id string) {
	b.Publish("message.deleted", map[string]string{"messageId": id})
}
func (b *Bus) ThreadUpdated(t domain.Thread)   { b.Publish("thread.updated", t) }
func (b *Bus) AccountUpdated(a domain.Account) { b.Publish("account.updated", a) }

// OutboxUpdated reports the pending-operations count after enqueue/drain.
func (b *Bus) OutboxUpdated(pending int) {
	b.Publish("outbox.updated", map[string]int{"pending": pending})
}

// ConnectionState reports the engine's view of server reachability, emitted
// on transitions only (offline after a network failure, back online after a
// successful replay).
func (b *Bus) ConnectionState(online bool) {
	b.Publish("connection.state", map[string]bool{"online": online})
}
