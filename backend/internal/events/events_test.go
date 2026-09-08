package events

import (
	"testing"

	"mail/backend/internal/domain"
)

func domainMessage() domain.Message {
	return domain.Message{ID: "m1", Subject: "test"}
}

func TestBusPublishSubscribe(t *testing.T) {
	bus := New()
	ch, stop := bus.Subscribe()
	defer stop()

	bus.SyncStarted("a1")
	bus.MessageCreated(domainMessage())

	got := <-ch
	if got.Kind != "sync.started" {
		t.Errorf("kind = %q", got.Kind)
	}
	got = <-ch
	if got.Kind != "message.created" {
		t.Errorf("kind = %q", got.Kind)
	}
}
