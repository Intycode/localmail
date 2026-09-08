// Package localai is the minimal client for the USER'S LOCAL AI server
// (M12): Ollama natively or any OpenAI-compatible local server (LM Studio,
// llama.cpp, vLLM...). Nothing is required for the app to work — every
// feature degrades gracefully when the server is off. The client only ever
// talks to the endpoint the user configured, localhost by default: mail
// content never leaves the machine unless the user explicitly allows a
// non-local host.
package localai

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultURL is the standard local Ollama endpoint; overridden by app_config
// key ai.url (legacy fallback: OLLAMA_URL env).
const DefaultURL = "http://127.0.0.1:11434"

// DefaultEmbedModel is the embedding model used unless configured otherwise
// (app_config key search.model, owned by the semantic package).
const DefaultEmbedModel = "nomic-embed-text"

// Kind selects the API dialect of the local server.
type Kind string

const (
	// KindOllama talks the native Ollama API (/api/tags, /api/chat, /api/embed).
	KindOllama Kind = "ollama"
	// KindOpenAI talks the OpenAI-compatible API (/v1/models,
	// /v1/chat/completions, /v1/embeddings) spoken by LM Studio, llama.cpp,
	// vLLM and Ollama itself.
	KindOpenAI Kind = "openai"
)

// Config is the user-configured local AI server (app_config keys ai.*).
type Config struct {
	Base        string
	Kind        Kind
	AllowRemote bool
	ChatModel   string
}

// app_config keys owned by this package (table created by 0015_semantic.sql).
const (
	keyURL    = "ai.url"
	keyKind   = "ai.kind"
	keyChat   = "ai.chat_model"
	keyRemote = "ai.allow_remote"
)

// LoadConfig resolves the configured server: app_config keys win over the
// legacy OLLAMA_URL env, which wins over the built-in defaults.
func LoadConfig(db *sql.DB) Config {
	cfg := Config{Base: DefaultURL, Kind: KindOllama}
	if env := os.Getenv("OLLAMA_URL"); strings.TrimSpace(env) != "" {
		cfg.Base = strings.TrimSpace(env)
	}
	if db == nil {
		return cfg
	}
	rows, err := db.Query(`SELECT key, value FROM app_config WHERE key IN (?,?,?,?)`,
		keyURL, keyKind, keyChat, keyRemote)
	if err != nil {
		return cfg
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		switch k {
		case keyURL:
			if strings.TrimSpace(v) != "" {
				cfg.Base = strings.TrimSpace(v)
			}
		case keyKind:
			if k := Kind(strings.TrimSpace(v)); k == KindOllama || k == KindOpenAI {
				cfg.Kind = k
			}
		case keyChat:
			cfg.ChatModel = strings.TrimSpace(v)
		case keyRemote:
			cfg.AllowRemote = v == "true"
		}
	}
	return cfg
}

// SaveConfig persists the connection part of the config (ChatModel is saved
// by the caller together with the rest; empty fields mean "leave unchanged").
func SaveConfig(db *sql.DB, base, kind, chatModel string, allowRemote *bool) error {
	set := func(key, value string) error {
		if _, err := db.Exec(`INSERT INTO app_config(key,value) VALUES(?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
			return err
		}
		return nil
	}
	if strings.TrimSpace(base) != "" {
		if err := set(keyURL, strings.TrimSpace(base)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(kind) != "" {
		k := Kind(strings.TrimSpace(kind))
		if k != KindOllama && k != KindOpenAI {
			return fmt.Errorf("tipo di server non valido: %s", kind)
		}
		if err := set(keyKind, string(k)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(chatModel) != "" {
		if err := set(keyChat, strings.TrimSpace(chatModel)); err != nil {
			return err
		}
	}
	if allowRemote != nil {
		v := "false"
		if *allowRemote {
			v = "true"
		}
		if err := set(keyRemote, v); err != nil {
			return err
		}
	}
	return nil
}

// Client talks to one local AI server.
type Client struct {
	Base string
	Kind Kind
	// AllowRemote mirrors the user opt-in: when false the client refuses
	// non-loopback endpoints (mail content must stay on the machine).
	AllowRemote bool
	HTTP        *http.Client
}

// New builds a client from the config; the base URL is normalized and the
// privacy guard applied eagerly so every call fails fast on a blocked host.
func New(cfg Config) *Client {
	c := &Client{
		Base:        normalizeBase(cfg.Base),
		Kind:        cfg.Kind,
		AllowRemote: cfg.AllowRemote,
		HTTP:        &http.Client{Timeout: 150 * time.Second}, // covers a 120s chat op
	}
	if c.Kind != KindOpenAI {
		c.Kind = KindOllama
	}
	return c
}

// normalizeBase trims the endpoint and adds the scheme when missing.
func normalizeBase(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = DefaultURL
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	return strings.TrimRight(base, "/")
}

// Validate enforces the privacy guard: only loopback endpoints unless the
// user explicitly allowed a remote host (toggle in Impostazioni → AI locale).
func (c *Client) Validate() error {
	u, err := url.Parse(c.Base)
	if err != nil || u.Host == "" {
		return fmt.Errorf("endpoint non valido: %s", c.Base)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint non valido (usa http o https): %s", c.Base)
	}
	if !c.AllowRemote && !isLoopback(u.Hostname()) {
		return fmt.Errorf("endpoint non locale (%s): il contenuto delle mail resta sul dispositivo solo con server locali; abilita il consenso esplicito nelle Impostazioni", u.Hostname())
	}
	return nil
}

// isLoopback reports whether the host targets this machine.
func isLoopback(host string) bool {
	switch strings.ToLower(strings.Trim(host, "[]")) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// url builds the endpoint for one API path: p is the Ollama-native path
// ("/api/tags") or the OpenAI path ("/v1/models"); a base that already ends
// with /v1 does not get a second /v1 appended.
func (c *Client) url(p string) string {
	base := c.Base
	if c.Kind == KindOpenAI {
		if strings.HasSuffix(base, "/v1") {
			return base + strings.TrimPrefix(p, "/v1")
		}
		return base + p
	}
	return base + p
}

// Available probes the server with a short timeout: a not-running local AI
// is an expected, non-error condition for the whole feature.
func (c *Client) Available() bool {
	p := "/api/tags"
	if c.Kind == KindOpenAI {
		p = "/v1/models"
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, c.url(p), nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ListModels returns the model identifiers served by the endpoint, sorted as
// the server reports them (Ollama: newest first; OpenAI: server order).
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	p := "/api/tags"
	if c.Kind == KindOpenAI {
		p = "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(p), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("server AI non raggiungibile su %s: %w", c.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("elenco modelli: status %d da %s", resp.StatusCode, c.url(p))
	}
	var out []string
	if c.Kind == KindOpenAI {
		var body struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return nil, fmt.Errorf("elenco modelli: %w", err)
		}
		for _, m := range body.Data {
			if strings.TrimSpace(m.ID) != "" {
				out = append(out, m.ID)
			}
		}
	} else {
		var body struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return nil, fmt.Errorf("elenco modelli: %w", err)
		}
		for _, m := range body.Models {
			if strings.TrimSpace(m.Name) != "" {
				out = append(out, m.Name)
			}
		}
	}
	return out, nil
}

// ChatMessage is one turn of a chat request. Images (M12 vision path) are
// data URLs ("data:image/jpeg;base64,...") so the same value works for both
// dialects: OpenAI-compatible servers take the URL as-is, Ollama gets the
// raw base64 (prefix stripped) in its `images` field.
type ChatMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

// rawBase64 strips the data-URL prefix (Ollama wants bare base64).
func rawBase64(dataURL string) string {
	if i := strings.Index(dataURL, "base64,"); i >= 0 {
		return dataURL[i+len("base64,"):]
	}
	return dataURL
}

// Chat runs a non-streaming completion with the given chat model and returns
// the assistant message content.
func (c *Client) Chat(ctx context.Context, model string, messages []ChatMessage) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if strings.TrimSpace(model) == "" {
		return "", fmt.Errorf("nessun modello chat configurato: Impostazioni → AI locale")
	}
	if len(messages) == 0 {
		return "", fmt.Errorf("richiesta vuota")
	}
	p := "/api/chat"
	var payload []byte
	var err error
	if c.Kind == KindOpenAI {
		p = "/v1/chat/completions"
		// OpenAI vision format: content is an array of typed parts.
		type part struct {
			Type     string `json:"type"`
			Text     string `json:"text,omitempty"`
			ImageURL *struct {
				URL string `json:"url"`
			} `json:"image_url,omitempty"`
		}
		type openAIMessage struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		}
		msgs := make([]openAIMessage, 0, len(messages))
		for _, m := range messages {
			if len(m.Images) == 0 {
				msgs = append(msgs, openAIMessage{Role: m.Role, Content: m.Content})
				continue
			}
			parts := []part{{Type: "text", Text: m.Content}}
			for _, img := range m.Images {
				parts = append(parts, part{Type: "image_url", ImageURL: &struct {
					URL string `json:"url"`
				}{URL: img}})
			}
			msgs = append(msgs, openAIMessage{Role: m.Role, Content: parts})
		}
		payload, err = json.Marshal(map[string]any{"model": model, "messages": msgs, "stream": false})
	} else {
		// Ollama native format: content stays a string, images are raw b64.
		type ollamaMessage struct {
			Role    string   `json:"role"`
			Content string   `json:"content"`
			Images  []string `json:"images,omitempty"`
		}
		msgs := make([]ollamaMessage, 0, len(messages))
		for _, m := range messages {
			om := ollamaMessage{Role: m.Role, Content: m.Content}
			for _, img := range m.Images {
				om.Images = append(om.Images, rawBase64(img))
			}
			msgs = append(msgs, om)
		}
		payload, err = json.Marshal(map[string]any{"model": model, "messages": msgs, "stream": false})
	}
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(p), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("richiesta al server AI non riuscita: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server AI: status %d", resp.StatusCode)
	}
	if c.Kind == KindOpenAI {
		var body struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", fmt.Errorf("risposta AI non valida: %w", err)
		}
		if len(body.Choices) == 0 {
			return "", fmt.Errorf("risposta AI vuota")
		}
		return body.Choices[0].Message.Content, nil
	}
	var body struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("risposta AI non valida: %w", err)
	}
	return body.Message.Content, nil
}

// Embed turns texts into vectors. Returns one vector per input (order
// preserved); empty inputs yield an error — callers should filter.
func (c *Client) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if len(texts) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(map[string]any{"model": model, "input": texts})
	if err != nil {
		return nil, err
	}
	p := "/api/embed"
	if c.Kind == KindOpenAI {
		p = "/v1/embeddings"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(p), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding: status %d", resp.StatusCode)
	}
	if c.Kind != KindOpenAI {
		var body struct {
			Embeddings [][]float32 `json:"embeddings"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return nil, fmt.Errorf("embedding: %w", err)
		}
		if len(body.Embeddings) != len(texts) {
			return nil, fmt.Errorf("embedding: %d vettori per %d testi", len(body.Embeddings), len(texts))
		}
		return body.Embeddings, nil
	}
	var body struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	if len(body.Data) != len(texts) {
		return nil, fmt.Errorf("embedding: %d vettori per %d testi", len(body.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range body.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("embedding: indice fuori intervallo %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}
