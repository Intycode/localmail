package localai

// M12 local AI tests: the HTTP contract of both dialects against fake
// httptest servers (no Ollama / LM Studio needed in CI), the loopback guard
// and the base-URL normalization.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mail/backend/internal/database"
)

// testDB opens a real (migrated) SQLite DB so LoadConfig/SaveConfig run
// against the same schema the engine uses.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNormalizeBase(t *testing.T) {
	cases := map[string]string{
		"":                          DefaultURL,
		"  ":                        DefaultURL,
		"127.0.0.1:11434":           "http://127.0.0.1:11434",
		"http://127.0.0.1:1234/":    "http://127.0.0.1:1234",
		"https://ai.lan.example:443": "https://ai.lan.example:443",
	}
	for in, want := range cases {
		if got := normalizeBase(in); got != want {
			t.Fatalf("normalizeBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGuardRejectsRemoteByDefault(t *testing.T) {
	c := New(Config{Base: "http://192.168.1.50:11434"})
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "non locale") {
		t.Fatalf("remote host accettato senza opt-in: %v", err)
	}
	c.AllowRemote = true
	if err := c.Validate(); err != nil {
		t.Fatalf("remote host rifiutato con opt-in: %v", err)
	}
	for _, local := range []string{"http://127.0.0.1:11434", "http://localhost:1234", "http://[::1]:11434"} {
		if err := New(Config{Base: local}).Validate(); err != nil {
			t.Fatalf("loopback %s rifiutato: %v", local, err)
		}
	}
	if err := New(Config{Base: "ftp://127.0.0.1:11434"}).Validate(); err == nil {
		t.Fatal("schema non http/https accettato")
	}
}

func TestListModelsOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.Error(w, "not found", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{{"name": "llama3.2:latest"}, {"name": "nomic-embed-text"}},
		})
	}))
	defer srv.Close()
	models, err := New(Config{Base: srv.URL}).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "llama3.2:latest" {
		t.Fatalf("models = %v", models)
	}
}

func TestListModelsOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.Error(w, "not found", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "qwen2.5-7b-instruct"}, {"id": "text-embedding-nomic"}},
		})
	}))
	defer srv.Close()
	c := New(Config{Base: srv.URL + "/v1", Kind: KindOpenAI}) // base with /v1 must not double it
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "qwen2.5-7b-instruct" {
		t.Fatalf("models = %v", models)
	}
	if !c.Available() {
		t.Fatal("probe /v1/models non riuscita")
	}
}

func TestChatBothKinds(t *testing.T) {
	msgs := []ChatMessage{{Role: "user", Content: "riassumi"}}

	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model    string        `json:"model"`
			Messages []ChatMessage `json:"messages"`
			Stream   bool          `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.URL.Path != "/api/chat" || in.Stream || in.Model != "m1" || len(in.Messages) != 1 {
			http.Error(w, "contratto non rispettato", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": "ecco il riassunto"},
		})
	}))
	defer ollamaSrv.Close()
	got, err := New(Config{Base: ollamaSrv.URL}).Chat(context.Background(), "m1", msgs)
	if err != nil || got != "ecco il riassunto" {
		t.Fatalf("ollama chat = %q err=%v", got, err)
	}

	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "riassunto openai"}}},
		})
	}))
	defer openaiSrv.Close()
	got, err = New(Config{Base: openaiSrv.URL, Kind: KindOpenAI}).Chat(context.Background(), "m1", msgs)
	if err != nil || got != "riassunto openai" {
		t.Fatalf("openai chat = %q err=%v", got, err)
	}

	if _, err := New(Config{Base: ollamaSrv.URL}).Chat(context.Background(), "", msgs); err == nil ||
		!strings.Contains(err.Error(), "modello chat") {
		t.Fatalf("modello vuoto accettato: %v", err)
	}
}

func TestEmbedOpenAIOrderedByIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.Error(w, "not found", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": []float32{4, 5}},
				{"index": 0, "embedding": []float32{1, 2}},
			},
		})
	}))
	defer srv.Close()
	vecs, err := New(Config{Base: srv.URL, Kind: KindOpenAI}).Embed(context.Background(), "emb", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][0] != 4 {
		t.Fatalf("ordine vettori non rispettato: %v", vecs)
	}
}

func TestLoadConfigFromDB(t *testing.T) {
	db := testDB(t)
	cfg := LoadConfig(db)
	if cfg.Base != DefaultURL || cfg.Kind != KindOllama || cfg.AllowRemote {
		t.Fatalf("default: %+v", cfg)
	}
	remote := true
	if err := SaveConfig(db, "http://192.168.1.7:1234", "openai", "qwen2.5", &remote); err != nil {
		t.Fatal(err)
	}
	cfg = LoadConfig(db)
	if cfg.Base != "http://192.168.1.7:1234" || cfg.Kind != KindOpenAI || !cfg.AllowRemote || cfg.ChatModel != "qwen2.5" {
		t.Fatalf("dopo save: %+v", cfg)
	}
	// OLLAMA_URL loses against app_config
	t.Setenv("OLLAMA_URL", "http://127.0.0.1:9999")
	if cfg := LoadConfig(db); cfg.Base != "http://192.168.1.7:1234" {
		t.Fatalf("app_config deve vincere sull'env: %+v", cfg)
	}
	if err := SaveConfig(db, "", "kind-sconosciuto", "", nil); err == nil {
		t.Fatal("kind non valido accettato")
	}
}

func TestAvailableShortTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second) // the probe must give up way earlier
	}))
	defer srv.Close()
	if New(Config{Base: srv.URL}).Available() {
		t.Fatal("probe su server lento riuscita")
	}
}

func TestChatWithImagesBothKinds(t *testing.T) {
	dataURL := "data:image/jpeg;base64,QUJD" // "ABC"
	msgs := []ChatMessage{{Role: "user", Content: "leggi il pdf", Images: []string{dataURL}}}

	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Role    string   `json:"role"`
				Content string   `json:"content"`
				Images  []string `json:"images"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if len(in.Messages) != 1 || len(in.Messages[0].Images) != 1 || in.Messages[0].Images[0] != "QUJD" {
			http.Error(w, "ollama vuole base64 nudo, non data URL", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": "ok"},
		})
	}))
	defer ollamaSrv.Close()
	got, err := New(Config{Base: ollamaSrv.URL}).Chat(context.Background(), "vl", msgs)
	if err != nil || got != "ok" {
		t.Fatalf("ollama vision = %q err=%v", got, err)
	}

	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					ImageURL *struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		m := in.Messages[0]
		if len(m.Content) != 2 || m.Content[0].Type != "text" ||
			m.Content[1].Type != "image_url" || m.Content[1].ImageURL == nil ||
			m.Content[1].ImageURL.URL != dataURL {
			http.Error(w, "openai vuole content[] con image_url data URL", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "letto"}}},
		})
	}))
	defer openaiSrv.Close()
	got, err = New(Config{Base: openaiSrv.URL, Kind: KindOpenAI}).Chat(context.Background(), "vl", msgs)
	if err != nil || got != "letto" {
		t.Fatalf("openai vision = %q err=%v", got, err)
	}
}
