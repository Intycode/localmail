package imap

import (
	"net"
	"os"
	"sync/atomic"
	"time"
)

// Read timeouts (vars so tests can shorten them). go-imap already bounds
// writes and the middle of a response, but a server that accepts a command
// and then never answers would block the reader forever: this closes that gap.
var (
	cmdReadTimeout  = 2 * time.Minute  // server must answer within this after we send
	idleReadTimeout = 35 * time.Minute // while IDLE; go-imap re-issues IDLE every 28 min
)

// deadlineConn arms a read deadline when a command is written (and, while
// IDLE, before every read). go-imap clears the deadline after each response,
// so a session with nothing pending is never timed out.
type deadlineConn struct {
	net.Conn
	explicit atomic.Int64 // read deadline go-imap set itself (UnixNano, 0 = none)
	pending  atomic.Bool  // a command was written and no response has started since
	idle     atomic.Int64 // IDLE read timeout in ns (0 = not idling)
	expired  atomic.Bool  // our deadline fired: stay failed (go-imap would retry the read for 30 s more)
}

func (d *deadlineConn) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		d.explicit.Store(0)
		if d.pending.Load() {
			// go-imap clears the deadline after a response while our next
			// command is already on the wire: keep it covered
			t = time.Now().Add(cmdReadTimeout)
		}
	} else {
		d.explicit.Store(t.UnixNano())
		d.pending.Store(false) // a response is arriving: go-imap bounds it
	}
	return d.Conn.SetReadDeadline(t)
}

func (d *deadlineConn) Write(p []byte) (int, error) {
	d.pending.Store(true)
	if d.explicit.Load() == 0 { // never loosen go-imap's own mid-response deadline
		_ = d.Conn.SetReadDeadline(time.Now().Add(cmdReadTimeout))
	}
	return d.Conn.Write(p)
}

func (d *deadlineConn) Read(p []byte) (int, error) {
	if d.expired.Load() {
		return 0, os.ErrDeadlineExceeded
	}
	if t := d.idle.Load(); t > 0 && d.explicit.Load() == 0 {
		_ = d.Conn.SetReadDeadline(time.Now().Add(time.Duration(t)))
	}
	n, err := d.Conn.Read(p)
	if ne, ok := err.(net.Error); ok && ne.Timeout() && d.explicit.Load() == 0 {
		d.expired.Store(true)
	}
	return n, err
}

// setIdle switches the IDLE read timeout on (t > 0) or off (t == 0).
func (d *deadlineConn) setIdle(t time.Duration) {
	d.idle.Store(int64(t))
	if t > 0 {
		_ = d.Conn.SetReadDeadline(time.Now().Add(t))
	} else if d.explicit.Load() == 0 {
		_ = d.Conn.SetReadDeadline(time.Time{})
	}
}
