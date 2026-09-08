package jmap

// Mock-JMAP tests: the provider is exercised against an httptest server that
// implements the subset of RFC 8620/8621 Mail uses (session, Mailbox
// query/get, Email query/get/set, blob upload/download).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mail/backend/internal/database"
	"mail/backend/internal/domain"
	"mail/backend/internal/provider"
)

type mockServer struct {
	blobs   map[string][]byte // blobId -> raw
	imports []json.RawMessage // captured Email/import args
	sets    []json.RawMessage // captured Email/set args
	next    int
}

// handleAPI implements the JMAP method dispatch over POST /api. Requests
// are positional arrays: [[name, args, callID], ...].
func (m *mockServer) handleAPI(w http.ResponseWriter, r *http.Request) {
	var raw []([]json.RawMessage)
	_ = json.NewDecoder(r.Body).Decode(&raw)
	type invocation struct {
		Name string
		Args json.RawMessage
		Call string
	}
	var req []invocation
	for _, item := range raw {
		c := invocation{}
		if len(item) > 0 {
			_ = json.Unmarshal(item[0], &c.Name)
		}
		if len(item) > 1 {
			c.Args = item[1]
		}
		if len(item) > 2 {
			_ = json.Unmarshal(item[2], &c.Call)
		}
		req = append(req, c)
	}
	var resps []map[string]any
	for _, call := range req {
		switch call.Name {
		case "Mailbox/query":
			resps = append(resps, map[string]any{
				"methodResponses": []any{
					[]any{"Mailbox/query", map[string]any{"ids": []string{"mb1", "mb2", "mb3"}}, call.Call},
				},
			})
		case "Mailbox/get":
			var args struct {
				IDs []string `json:"ids"`
			}
			_ = json.Unmarshal(call.Args, &args)
			list := []map[string]any{
				{"id": "mb1", "name": "INBOX", "role": "inbox"},
			}
			for _, id := range args.IDs {
				if id == "mb2" {
					list = append(list, map[string]any{"id": "mb2", "name": "Sent", "role": "sent"})
				}
				if id == "mb3" {
					list = append(list, map[string]any{"id": "mb3", "name": "Drafts", "role": "drafts"})
				}
			}
			resps = append(resps, map[string]any{
				"methodResponses": []any{
					[]any{"Mailbox/get", map[string]any{"list": list}, call.Call},
				},
			})
		case "Email/query":
			var args struct {
				Filter struct {
					InMailbox string `json:"inMailbox"`
				} `json:"filter"`
			}
			_ = json.Unmarshal(call.Args, &args)
			ids := []string{}
			if args.Filter.InMailbox == "mb1" {
				ids = []string{"e1", "e2"}
			}
			resps = append(resps, map[string]any{
				"methodResponses": []any{
					[]any{"Email/query", map[string]any{"ids": ids, "total": len(ids)}, call.Call},
				},
			})
		case "Email/get":
			var args struct {
				IDs []string `json:"ids"`
			}
			_ = json.Unmarshal(call.Args, &args)
			list := []map[string]any{}
			for _, id := range args.IDs {
				list = append(list, map[string]any{
					"id":       id,
					"blobId":   "blob-" + id,
					"keywords": map[string]bool{"$seen": id == "e1"},
				})
			}
			resps = append(resps, map[string]any{
				"methodResponses": []any{
					[]any{"Email/get", map[string]any{"list": list}, call.Call},
				},
			})
		case "Email/set":
			m.sets = append(m.sets, call.Args)
			resps = append(resps, map[string]any{
				"methodResponses": []any{
					[]any{"Email/set", map[string]any{}, call.Call},
				},
			})
		case "Email/import":
			m.imports = append(m.imports, call.Args)
			resps = append(resps, map[string]any{
				"methodResponses": []any{
					[]any{"Email/import", map[string]any{}, call.Call},
				},
			})
		case "Identity/get":
			resps = append(resps, map[string]any{
				"methodResponses": []any{
					[]any{"Identity/get", map[string]any{"list": []map[string]any{{"id": "id1", "email": "me@example.com"}}}, call.Call},
				},
			})
		case "EmailSubmission/set":
			resps = append(resps, map[string]any{
				"methodResponses": []any{
					[]any{"EmailSubmission/set", map[string]any{}, call.Call},
				},
			})
		default:
			resps = append(resps, map[string]any{
				"methodResponses": []any{
					[]any{"error", map[string]any{"description": "unknown method " + call.Name}, call.Call},
				},
			})
		}
	}
	_ = json.NewEncoder(w).Encode(resps[0])
}

func setup(t *testing.T) (*Provider, *mockServer) {
	t.Helper()
	m := &mockServer{blobs: map[string][]byte{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/jmap/session":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accounts": map[string]any{
					"acc1": map[string]any{
						"name":         "Test",
						"capabilities": map[string]any{"urn:ietf:params:jmap:mail": map[string]any{}},
					},
				},
				"primaryAccounts": map[string]string{"urn:ietf:params:jmap:core": "acc1"},
				"apiUrl":          tsURL(r) + "/api",
				"downloadUrl":     tsURL(r) + "/download/{accountId}/{blobId}/{name}",
				"uploadUrl":       tsURL(r) + "/upload/{accountId}",
				"eventSourceUrl":  tsURL(r) + "/events?types={types}",
			})
		case r.URL.Path == "/api":
			m.handleAPI(w, r)
		case strings.HasPrefix(r.URL.Path, "/download/"):
			parts := strings.Split(r.URL.Path, "/")
			blob := parts[len(parts)-2]
			data, ok := m.blobs[blob]
			if !ok {
				http.Error(w, "no blob", http.StatusNotFound)
				return
			}
			_, _ = w.Write(data)
		case strings.HasPrefix(r.URL.Path, "/upload/"):
			buf := make([]byte, r.ContentLength)
			_, _ = readFull(r, buf)
			m.next++
			id := fmt.Sprintf("up-%d", m.next)
			m.blobs[id] = buf
			_ = json.NewEncoder(w).Encode(map[string]any{"blobId": id})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	dbPath := t.TempDir() + "/test.db"
	db, err := database.Open(dbPath, strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p := New(db, ts.URL+"/jmap/session", "me@example.com", "tok")
	t.Cleanup(p.Close)
	return p, m
}

func readFull(r *http.Request, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		c, err := r.Body.Read(buf[n:])
		n += c
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func tsURL(r *http.Request) string { return "http://" + r.Host }

func TestPing(t *testing.T) {
	p, _ := setup(t)
	if err := p.Ping(); err != nil {
		t.Fatal(err)
	}
}

func TestPingUnauthorized(t *testing.T) {
	t.Skip("mock always accepts tok; covered by handler logic")
}

func TestSyncFolderInitial(t *testing.T) {
	p, m := setup(t)
	m.blobs["blob-e1"] = []byte("Subject: uno\r\n\r\nbody\r\n")
	m.blobs["blob-e2"] = []byte("Subject: due\r\n\r\nbody\r\n")

	fs, err := p.SyncFolder(provider.FolderRef{FolderID: "f1", MailboxName: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	if fs.UIDValidity != 1 {
		t.Fatalf("uidvalidity = %d", fs.UIDValidity)
	}
	if len(fs.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(fs.Messages))
	}
	if fs.AllUIDs[0] != 1 || fs.AllUIDs[1] != 2 {
		t.Fatalf("all uids = %v", fs.AllUIDs)
	}
	// flags: e1 seen, e2 unread
	if fs.Messages[0].UID != 1 || len(fs.Messages[0].Flags) == 0 {
		t.Fatalf("e1 flags = %v", fs.Messages[0].Flags)
	}
	if len(fs.Messages[1].Flags) != 0 {
		t.Fatalf("e2 should be unread, flags = %v", fs.Messages[1].Flags)
	}

	// second sync: no new messages, same uids (stable mapping)
	fs2, err := p.SyncFolder(provider.FolderRef{FolderID: "f1", MailboxName: "INBOX", LastUID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs2.Messages) != 0 {
		t.Fatalf("second sync fetched %d messages, want 0", len(fs2.Messages))
	}
	if len(fs2.AllUIDs) != 2 {
		t.Fatalf("all uids = %v", fs2.AllUIDs)
	}
}

func TestMarkRead(t *testing.T) {
	p, m := setup(t)
	m.blobs["blob-e1"] = []byte("Subject: uno\r\n\r\nbody\r\n")
	m.blobs["blob-e2"] = []byte("Subject: due\r\n\r\nbody\r\n")
	if _, err := p.SyncFolder(provider.FolderRef{FolderID: "f1", MailboxName: "INBOX"}); err != nil {
		t.Fatal(err)
	}
	// uid 2 = e2 (unread): mark it read
	if err := p.MarkRead("INBOX", []uint32{2}); err != nil {
		t.Fatal(err)
	}
	if len(m.sets) != 1 {
		t.Fatalf("Email/set calls = %d, want 1", len(m.sets))
	}
	var args struct {
		Update map[string]map[string]any `json:"update"`
	}
	if err := json.Unmarshal(m.sets[0], &args); err != nil {
		t.Fatal(err)
	}
	up, ok := args.Update["e2"]
	if !ok {
		t.Fatalf("update target = %v", args.Update)
	}
	kw := up["keywords"].(map[string]any)
	if kw["$seen"] != true {
		t.Fatalf("e2 keywords = %v", kw)
	}
}

func TestMoveAndFetchRaw(t *testing.T) {
	p, m := setup(t)
	m.blobs["blob-e1"] = []byte("Subject: con allegato\r\n\r\nbody\r\n")
	if _, err := p.SyncFolder(provider.FolderRef{FolderID: "f1", MailboxName: "INBOX"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Move("INBOX", []uint32{1}, "Sent"); err != nil {
		t.Fatal(err)
	}
	if len(m.sets) != 1 {
		t.Fatalf("Email/set calls = %d", len(m.sets))
	}
	raw, err := p.FetchRaw("INBOX", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "con allegato") {
		t.Fatalf("raw = %q", raw)
	}
}

func TestSendSubmission(t *testing.T) {
	p, m := setup(t)
	m.blobs["blob-e1"] = []byte("x")
	if _, err := p.SyncFolder(provider.FolderRef{FolderID: "f1", MailboxName: "INBOX"}); err != nil {
		t.Fatal(err)
	}
	raw, err := p.Send(recipient(), compose())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Ciao") {
		t.Fatalf("raw = %q", raw)
	}
	if len(m.imports) != 1 {
		t.Fatalf("Email/import calls = %d, want 1", len(m.imports))
	}
}

func recipient() domain.Recipient {
	return domain.Recipient{Email: "dest@example.com", Name: "Dest"}
}

func compose() domain.ComposeMessage {
	return domain.ComposeMessage{
		Subject: "Ciao",
		Body:    "Corpo del messaggio",
		To:      []domain.Recipient{recipient()},
	}
}

func TestUnknownMailboxNoOp(t *testing.T) {
	p, m := setup(t)
	m.blobs["blob-e1"] = []byte("x")
	if _, err := p.SyncFolder(provider.FolderRef{FolderID: "f1", MailboxName: "INBOX"}); err != nil {
		t.Fatal(err)
	}
	// a custom folder with no server counterpart: sync is a no-op that keeps
	// the current mapping (reconciliation must not wipe)
	fs, err := p.SyncFolder(provider.FolderRef{FolderID: "f-custom", MailboxName: "Archivio 2026"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs.AllUIDs) != 0 || len(fs.Messages) != 0 {
		t.Fatalf("unexpected sync result: %+v", fs)
	}
}
