package ipc

import "sync"

// sendLog makes send idempotent per ClientID: a second request with the same
// id waits for the first and gets its result instead of delivering again. A
// failed send is forgotten so the user can retry it.
//
// shortcut: in memory only and never pruned (a few bytes per sent mail, per
// engine run); after an engine restart a retry is no longer recognised. Move
// it to a table if sends must be idempotent across restarts.
type sendLog struct {
	mu sync.Mutex
	m  map[string]*sendCall
}

type sendCall struct {
	done chan struct{}
	res  any
	err  error
}

var sends = &sendLog{m: map[string]*sendCall{}}

func (l *sendLog) do(id string, fn func() (any, error)) (any, error) {
	l.mu.Lock()
	if c, ok := l.m[id]; ok {
		l.mu.Unlock()
		<-c.done
		return c.res, c.err
	}
	c := &sendCall{done: make(chan struct{})}
	l.m[id] = c
	l.mu.Unlock()

	c.res, c.err = fn()
	if c.err != nil {
		l.mu.Lock()
		delete(l.m, id)
		l.mu.Unlock()
	}
	close(c.done)
	return c.res, c.err
}
