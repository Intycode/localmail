// Package e2e drives the REAL engine binary over stdio JSON-lines: it is
// the end-to-end guard for the IPC contract (allowlist, error envelopes,
// event framing) without any Tauri/webview in the loop. The binary is built
// once in TestMain from ./cmd/mail, exactly like engine:build does.
package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

var engineBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mail-e2e-*")
	if err != nil {
		os.Exit(1)
	}
	engineBin = filepath.Join(dir, "mail-engine")
	build := exec.Command("go", "build", "-o", engineBin, "./cmd/mail")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		os.Stderr.Write(out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type frame struct {
	ID     string         `json:"id"`
	OK     bool           `json:"ok"`
	Error  string         `json:"error"`
	Kind   string         `json:"kind"`
	Result map[string]any `json:"data"`
}

type session struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	// frames carries every parsed JSON-lines frame (responses AND events):
	// a goroutine owns the pipe, call() consumes with a timeout.
	frames chan frame
	done   chan struct{}
	t      *testing.T
}

func start(t *testing.T) *session {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	cmd := exec.Command(engineBin, "--serve", "--db", dbPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn engine: %v", err)
	}
	s := &session{
		cmd:    cmd,
		stdin:  stdin,
		frames: make(chan frame, 256),
		done:   make(chan struct{}),
		t:      t,
	}
	go func() {
		defer close(s.frames)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1<<20), 8<<20)
		for scanner.Scan() {
			var f frame
			if json.Unmarshal(scanner.Bytes(), &f) != nil {
				continue
			}
			s.frames <- f
		}
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return s
}

// call sends one request and reads framed lines until the response with the
// matching id arrives; events are drained along the way.
func (s *session) call(id, op string, params map[string]any) (ok bool, result map[string]any, errMsg string) {
	s.t.Helper()
	req := map[string]any{"id": id, "op": op}
	if params != nil {
		req["params"] = params
	}
	raw, err := json.Marshal(req)
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.stdin.Write(append(raw, '\n')); err != nil {
		s.t.Fatalf("write %s: %v", op, err)
	}
	deadline := time.After(20 * time.Second)
	for {
		select {
		case <-deadline:
			s.t.Fatalf("timeout waiting for response %s", op)
		case f, open := <-s.frames:
			if !open {
				s.t.Fatalf("engine closed stdout before answering %s", op)
			}
			if f.Kind != "" || f.ID == "" {
				continue // event or unframed noise: not ours
			}
			if f.ID != id {
				continue // response to someone else: cannot happen here
			}
			return f.OK, f.Result, f.Error
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func TestProtocolEndToEnd(t *testing.T) {
	s := start(t)

	// 1) healthy op against an empty store
	ok, data, errMsg := s.call("r1", "accounts.list", nil)
	if !ok || errMsg != "" {
		t.Fatalf("accounts.list: ok=%v err=%q", ok, errMsg)
	}
	if list, present := data["items"]; present {
		_ = list
	}

	// 2) unknown op: refused by the allowlist with a clean envelope
	ok, _, _ = s.call("r2", "totally.unknown", nil)
	if ok {
		t.Fatal("unknown op must not succeed")
	}

	// 3) malformed JSON line: must not crash the engine
	if _, err := s.stdin.Write([]byte("questo non e' json\n")); err != nil {
		t.Fatal(err)
	}
	// the engine must still answer the next request
	ok, _, errMsg = s.call("r3", "outbox.list", nil)
	if !ok || errMsg != "" {
		t.Fatalf("engine did not survive a malformed line: ok=%v err=%q", ok, errMsg)
	}

	// 4) send without accounts: validation error, not a crash
	ok, _, _ = s.call("r4", "send", map[string]any{
		"message": map[string]any{"subject": "x", "body": "y"},
	})
	if ok {
		t.Fatal("send without accounts must fail")
	}

	// 5) op requiring params but none given
	ok, _, _ = s.call("r5", "thread.get", nil)
	if ok {
		t.Fatal("thread.get without threadId must fail")
	}

	// 6) the engine answers a second full round: no state corruption
	ok, _, errMsg = s.call("r6", "accounts.list", nil)
	if !ok || errMsg != "" {
		t.Fatalf("accounts.list round 2: ok=%v err=%q", ok, errMsg)
	}
}
