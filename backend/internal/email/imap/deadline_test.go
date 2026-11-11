package imap

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// startSilentServer greets, accepts LOGIN, then never answers anything else.
func startSilentServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = conn.Write([]byte("* OK [CAPABILITY IMAP4rev1] ready\r\n"))
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					f := strings.Fields(line)
					if len(f) > 1 && strings.EqualFold(f[1], "LOGIN") {
						_, _ = conn.Write([]byte(f[0] + " OK [CAPABILITY IMAP4rev1] logged in\r\n"))
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func shortTimeout(t *testing.T) time.Duration {
	t.Helper()
	old := cmdReadTimeout
	cmdReadTimeout = 300 * time.Millisecond
	t.Cleanup(func() { cmdReadTimeout = old })
	return cmdReadTimeout
}

func TestDeadlineSilentServerFailsFast(t *testing.T) {
	timeout := shortTimeout(t)
	cfg, pw := testConfig(startSilentServer(t))
	s, err := Dial(cfg, pw)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer s.Close()

	start := time.Now()
	_, err = s.Fetch([]uint32{1}) // SELECT is sent, the server never replies
	if err == nil {
		t.Fatal("fetch on a silent server must fail")
	}
	if el := time.Since(start); el > 5*timeout {
		t.Errorf("fetch took %v, want about %v", el, timeout)
	}
}

func TestDeadlineIdleSessionStaysUsable(t *testing.T) {
	timeout := shortTimeout(t)
	addr, usr := startTestServer(t)
	if err := usr.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	cfg, pw := testConfig(addr)
	s, err := Dial(cfg, pw)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Select("INBOX"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * timeout) // no command pending: must not time out
	if _, err := s.UIDs(); err != nil {
		t.Fatalf("session died while merely idle: %v", err)
	}
}
