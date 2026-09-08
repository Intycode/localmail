// Package semantic implements the optional embedding layer of M10: vectors
// per message in the encrypted DB, a chunked backfill the UI can drive, and
// a cosine-similarity query with thread aggregation. All similarity math is
// plain Go over BLOBs — no SQLite extension, no external service beyond the
// optional user-configured local AI server (Ollama / LM Studio, M12).
package semantic

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"

	"mail/backend/internal/domain"
	"mail/backend/internal/localai"
)

// Embedder is what semantic needs from a local AI client: only the
// embeddings, so any dialect (Ollama native, OpenAI-compatible) works.
type Embedder interface {
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// Store wraps the encrypted DB for the embedding tables.
type Store struct {
	db  *sql.DB
	ctx context.Context
}

func New(db *sql.DB) *Store { return &Store{db: db, ctx: context.Background()} }

// Model returns the configured embedding model (default when unset).
func (s *Store) Model() string {
	var m string
	err := s.db.QueryRow(`SELECT value FROM app_config WHERE key='search.model'`).Scan(&m)
	if err != nil || strings.TrimSpace(m) == "" {
		return localai.DefaultEmbedModel
	}
	return m
}

// SetModel stores the model; changing it invalidates every stored vector
// (different models are not comparable), so embeddings are wiped and the
// user re-indexes.
func (s *Store) SetModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("modello vuoto")
	}
	if _, err := s.db.Exec(`INSERT INTO app_config(key,value) VALUES('search.model',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, model); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM message_embeddings`)
	return err
}

// Counts returns (indexed with the current model, total messages).
func (s *Store) Counts() (indexed, total int, err error) {
	model := s.Model()
	err = s.db.QueryRow(`SELECT
			(SELECT COUNT(*) FROM message_embeddings WHERE model=?),
			(SELECT COUNT(*) FROM messages)`, model).Scan(&indexed, &total)
	return indexed, total, err
}

// embedText is what a message is reduced to before embedding: subject, sender
// and the first part of the plain-text body — meaning lives mostly there.
func embedText(subject, fromName, bodyText string) string {
	body := bodyText
	if len(body) > 2000 {
		body = body[:2000]
	}
	return strings.TrimSpace(subject + "\n" + fromName + "\n" + body)
}

// Backfill embeds up to limit messages that miss a vector for the current
// model (newest first) and returns how many were stored. The UI drives it in
// chunks so a large mailbox never blocks one request for minutes.
func (s *Store) Backfill(client Embedder, limit int) (int, error) {
	model := s.Model()
	rows, err := s.db.Query(`
		SELECT m.id, m.account, m.subject, COALESCE(m.from_name,''), COALESCE(m.body_text,'')
		FROM messages m
		LEFT JOIN message_embeddings e ON e.message_id = m.id AND e.model = ?
		WHERE e.message_id IS NULL
		ORDER BY m.date DESC
		LIMIT ?`, model, limit)
	if err != nil {
		return 0, err
	}
	type job struct {
		id, account, subject, fromName, bodyText string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.account, &j.subject, &j.fromName, &j.bodyText); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(jobs) == 0 {
		return 0, nil
	}

	// single request with all texts: both dialects batch natively and a
	// local server handles hundreds of inputs per call
	texts := make([]string, len(jobs))
	for i := range jobs {
		texts[i] = embedText(jobs[i].subject, jobs[i].fromName, jobs[i].bodyText)
	}
	vectors, err := client.Embed(s.ctx, model, texts)
	if err != nil {
		return 0, err
	}
	stored := 0
	for i := range jobs {
		if vectors == nil || i >= len(vectors) || len(vectors[i]) == 0 {
			continue
		}
		blob := encodeVector(vectors[i])
		if _, err := s.db.Exec(`INSERT OR REPLACE INTO message_embeddings(message_id,account,model,dim,vector,updated_at)
			VALUES(?,?,?,?,?,?)`, jobs[i].id, jobs[i].account, model, len(vectors[i]), blob, time.Now().UTC().Format(timeFmt)); err != nil {
			continue // one failed row must not abort the backfill
		}
		stored++
	}
	return stored, nil
}

const timeFmt = "2006-01-02T15:04:05.000000000Z07:00"

// encodeVector packs float32s little-endian into a BLOB.
func encodeVector(v []float32) []byte {
	out := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(f))
	}
	return out
}

// decodeVector unpacks a BLOB back to float32s (nil when the size is wrong).
func decodeVector(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

// cosine similarity for equal-length vectors (0 when degenerate).
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// Search embeds the query and ranks messages by cosine similarity, then
// aggregates the best message per thread. Results are strictly scoped to the
// messages that HAVE an embedding for the current model — a mailbox without
// a completed backfill returns fewer (or no) results and the caller falls
// back to keyword search.
func (s *Store) Search(client Embedder, q domain.SearchQuery) ([]domain.SearchResult, error) {
	model := s.Model()
	vectors, err := client.Embed(s.ctx, model, []string{q.Query})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("query non rappresentabile come vettore")
	}
	queryVec := vectors[0]

	where := "WHERE e.model = ?"
	args := []any{model}
	if q.AccountID != "" {
		where += " AND e.account = ?"
		args = append(args, q.AccountID)
	}
	if q.FolderID != "" {
		where += " AND t.folder = ?"
		args = append(args, q.FolderID)
	}
	if !q.After.IsZero() {
		where += " AND m.date >= ?"
		args = append(args, q.After.UTC().Format(timeFmt))
	}
	if !q.Before.IsZero() {
		where += " AND m.date <= ?"
		args = append(args, q.Before.UTC().Format(timeFmt))
	}

	rows, err := s.db.Query(`
		SELECT m.id, t.id, m.subject, COALESCE(m.body_text,''), COALESCE(m.from_name,''), e.vector
		FROM message_embeddings e
		JOIN messages m ON m.id = e.message_id
		JOIN threads t  ON t.id = m.thread
		`+where, args...)
	if err != nil {
		return nil, err
	}
	type hit struct {
		threadID, subject, body, from string
		score                         float64
	}
	best := map[string]*hit{}
	for rows.Next() {
		var id string
		var h hit
		var blob []byte
		if err := rows.Scan(&id, &h.threadID, &h.subject, &h.body, &h.from, &blob); err != nil {
			rows.Close()
			return nil, err
		}
		vec := decodeVector(blob)
		if vec == nil {
			continue // stale dim from a model switch: ignored, backfill replaces
		}
		score := cosine(queryVec, vec)
		if cur, ok := best[h.threadID]; !ok || score > cur.score {
			bh := h
			bh.score = score
			best[h.threadID] = &bh
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// rank threads by score, top 30
	type ranked struct {
		id    string
		score float64
	}
	var all []ranked
	for id, h := range best {
		all = append(all, ranked{id, h.score})
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].score > all[j-1].score; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if len(all) > 30 {
		all = all[:30]
	}

	out := make([]domain.SearchResult, 0, len(all))
	for _, r := range all {
		h := best[r.id]
		out = append(out, domain.SearchResult{
			ThreadID: r.id,
			Subject:  h.subject,
			Snippet:  makeSemanticSnippet(q.Query, h.subject, h.body, h.from),
			Matched:  "semantic",
			Score:    float32(r.score),
			Date:     s.threadDate(r.id),
		})
	}
	return out, nil
}

// threadDate returns the latest message date of a thread (same as search pkg).
func (s *Store) threadDate(threadID string) time.Time {
	var last string
	err := s.db.QueryRow(`SELECT MAX(date) FROM messages WHERE thread=?`, threadID).Scan(&last)
	if err != nil || last == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return time.Time{}
	}
	return t
}

// makeSemanticSnippet picks the excerpt around the rarest query word — no FTS
// rank to lean on here, so a simple word-boundary window is the honest choice.
func makeSemanticSnippet(q, subject, body, from string) string {
	for _, text := range []string{subject, body} {
		for _, w := range strings.Fields(q) {
			if len(w) < 3 {
				continue
			}
			lower := strings.ToLower(text)
			i := strings.Index(lower, strings.ToLower(w))
			if i < 0 {
				continue
			}
			start := i - 40
			if start < 0 {
				start = 0
			}
			end := i + len(w) + 40
			if end > len(text) {
				end = len(text)
			}
			sn := text[start:end]
			if start > 0 {
				sn = "…" + sn
			}
			if end < len(text) {
				sn += "…"
			}
			return sn
		}
	}
	return strings.TrimSpace(from + " — " + subject)
}
