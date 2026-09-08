package semantic

// M10 semantic tests: vector roundtrip + cosine math (pure), and a full
// backfill+search cycle against a FAKE local embedding server (no Ollama
// needed in CI — the HTTP contract is what matters).

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mail/backend/internal/database"
	"mail/backend/internal/domain"
	"mail/backend/internal/localai"
)

func TestEncodeDecodeVector(t *testing.T) {
	v := []float32{0.5, -1.25, 3, 0}
	got := decodeVector(encodeVector(v))
	if len(got) != len(v) {
		t.Fatalf("len = %d", len(got))
	}
	for i := range v {
		if got[i] != v[i] {
			t.Fatalf("v[%d] = %f", i, got[i])
		}
	}
	if decodeVector([]byte{1, 2, 3}) != nil {
		t.Fatal("blocco non allineato accettato")
	}
}

func TestCosine(t *testing.T) {
	same := cosine([]float32{1, 0, 1}, []float32{1, 0, 1})
	if math.Abs(same-1) > 1e-9 {
		t.Fatalf("parallel = %f", same)
	}
	if cosine([]float32{1, 0}, []float32{0, 1}) > 1e-9 {
		t.Fatal("orthogonal non zero")
	}
	if cosine(nil, nil) != 0 || cosine([]float32{1}, []float32{1, 2}) != 0 {
		t.Fatal("casi degradati")
	}
}

func TestBackfillAndSearch(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "sem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// fake embedding server: deterministic 4-dim vectors from the text bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		vecs := make([][]float32, len(in.Input))
		for i, txt := range in.Input {
			// letter-frequency bag: texts about the same topic share enough
			// characters that cosine ranks them together (deterministic)
			v := make([]float32, 26)
			total := 0
			for _, c := range strings.ToLower(txt) {
				if c >= 'a' && c <= 'z' {
					v[int(c)-'a']++
					total++
				}
			}
			if total > 0 {
				for j := range v {
					v[j] /= float32(total)
				}
			}
			vecs[i] = v
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})
	}))
	defer srv.Close()
	client := localaiClientForTest(srv.URL)

	// seed account + two clearly different messages
	if _, err := db.Exec(`INSERT INTO accounts(id,name,email,provider,active,created_at)
		VALUES('a1','L','l@x.dev','mock',1,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO folders(id,account,name,type) VALUES('f','a1','Inbox','inbox')`); err != nil {
		t.Fatal(err)
	}
	ins := func(id, thread, subject, body string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO threads(id,folder,account,subject,snippet,last_date,is_unread,is_starred,message_count,has_attach)
			VALUES(?,?,?,?,?,'2026-08-25T09:00:00Z',0,0,1,0)`, thread, "f", "a1", subject, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO messages(id,thread,folder,account,subject,from_name,from_email,body,body_text,date)
			VALUES(?,?, 'f','a1',?, 'Anna','anna@acme.dev',?,?, '2026-08-25T09:00:00.000000000Z')`,
			id, thread, subject, body, body); err != nil {
			t.Fatal(err)
		}
	}
	ins("m1", "t1", "Rinnovo contratto affitto", "Il rinnovo del contratto di affitto e pronto")
	ins("m2", "t2", "Ferie agosto", "Richiesta ferie per agosto")

	st := New(db)
	n, err := st.Backfill(client, 100)
	if err != nil || n != 2 {
		t.Fatalf("backfill = %d err=%v", n, err)
	}
	// idempotent: a second pass stores nothing new
	if n, _ := st.Backfill(client, 100); n != 0 {
		t.Fatalf("backfill non idempotente: %d", n)
	}

	// query similar to m1 text ranks t1 first
	res, err := st.Search(client, domain.SearchQuery{Query: "rinnovo del contratto di affitto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 || res[0].ThreadID != "t1" {
		t.Fatalf("semantic top: %+v", res)
	}
	if res[0].Matched != "semantic" || res[0].Score <= 0 || res[0].Score > 1 {
		t.Fatalf("score: %+v", res[0])
	}
	// snippet contains a real fragment
	if !strings.Contains(strings.ToLower(res[0].Snippet+res[0].Subject), "contratto") {
		t.Fatalf("snippet: %q", res[0].Snippet)
	}

	// model switch wipes vectors and forces reindex
	if err := st.SetModel("altro-modello"); err != nil {
		t.Fatal(err)
	}
	indexed, total, err := st.Counts()
	if err != nil || indexed != 0 || total != 2 {
		t.Fatalf("counts dopo switch: %d/%d err=%v", indexed, total, err)
	}
}

// localaiClientForTest builds a Client pointed at the fake server.
func localaiClientForTest(base string) *localai.Client {
	return localai.New(localai.Config{Base: base})
}
