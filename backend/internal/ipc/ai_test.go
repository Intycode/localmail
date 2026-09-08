package ipc

// M12 local AI ops: config persistence (app_config), status probe, model
// listing and chat completion against a FAKE local server (the HTTP
// contract is what matters — no Ollama / LM Studio needed in CI). Also
// covers the loopback guard at the protocol boundary.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOllama serves /api/tags (model list) and /api/chat (echo completion).
func fakeOllama(chatContent string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{{"name": "llama3.2:latest"}, {"name": "nomic-embed-text"}},
			})
		case "/api/chat":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]any{"role": "assistant", "content": chatContent},
			})
		default:
			http.Error(w, "not found", 404)
		}
	}))
}

func TestAIConfigAndStatus(t *testing.T) {
	env := startEnv(t)
	srv := fakeOllama("ok")
	defer srv.Close()

	ok, _, errText := env.call(t, "c1", "ai.config", map[string]any{
		"url": srv.URL, "kind": "ollama", "chatModel": "llama3.2:latest",
	})
	if !ok {
		t.Fatalf("ai.config: %s", errText)
	}

	ok, data, errText := env.call(t, "c2", "ai.status", nil)
	if !ok {
		t.Fatalf("ai.status: %s", errText)
	}
	var st struct {
		Available bool   `json:"available"`
		URL       string `json:"url"`
		Kind      string `json:"kind"`
		ChatModel string `json:"chatModel"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Available || st.URL != srv.URL || st.Kind != "ollama" || st.ChatModel != "llama3.2:latest" {
		t.Fatalf("status = %+v", st)
	}
}

func TestAIConfigRejectsRemoteHost(t *testing.T) {
	env := startEnv(t)
	ok, _, errText := env.call(t, "c1", "ai.config", map[string]any{
		"url": "http://10.0.0.9:11434",
	})
	if ok || !strings.Contains(errText, "non locale") {
		t.Fatalf("host remoto accettato senza opt-in: ok=%v err=%s", ok, errText)
	}
	// explicit consent unlocks it
	allow := true
	ok, _, errText = env.call(t, "c2", "ai.config", map[string]any{
		"url": "http://10.0.0.9:11434", "allowRemote": allow,
	})
	if !ok {
		t.Fatalf("host remoto rifiutato con opt-in: %s", errText)
	}
}

func TestAIModelsAndChat(t *testing.T) {
	env := startEnv(t)
	srv := fakeOllama("riassunto pronto")
	defer srv.Close()

	if ok, _, errText := env.call(t, "c0", "ai.config", map[string]any{
		"url": srv.URL, "chatModel": "llama3.2:latest",
	}); !ok {
		t.Fatalf("ai.config: %s", errText)
	}

	ok, data, errText := env.call(t, "c1", "ai.models", nil)
	if !ok {
		t.Fatalf("ai.models: %s", errText)
	}
	var ml struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(data, &ml); err != nil {
		t.Fatal(err)
	}
	if len(ml.Models) != 2 || ml.Models[0] != "llama3.2:latest" {
		t.Fatalf("models = %v", ml.Models)
	}

	ok, data, errText = env.call(t, "c2", "ai.chat", map[string]any{
		"messages": []map[string]string{{"role": "user", "content": "riassumi questo thread"}},
	})
	if !ok {
		t.Fatalf("ai.chat: %s", errText)
	}
	var cr struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(data, &cr); err != nil {
		t.Fatal(err)
	}
	if cr.Content != "riassunto pronto" {
		t.Fatalf("content = %q", cr.Content)
	}
}

func TestAIChatNeedsModel(t *testing.T) {
	env := startEnv(t)
	ok, _, errText := env.call(t, "c1", "ai.chat", map[string]any{
		"messages": []map[string]string{{"role": "user", "content": "ciao"}},
	})
	if ok || !strings.Contains(errText, "modello chat") {
		t.Fatalf("chat senza modello configurato: ok=%v err=%s", ok, errText)
	}
}
