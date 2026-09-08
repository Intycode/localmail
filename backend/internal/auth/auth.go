// Package auth defines the authentication architecture.
//
// Rule: passwords and tokens NEVER go in SQLite, logs or IPC responses. They
// live in OS secure storage, reached through Tauri; the engine receives them
// in memory only (RuntimeSecrets) for the duration of a connection.
package auth

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Secret wraps a password/token so it cannot accidentally leak: String and
// MarshalJSON always redact; call Expose only to authenticate.
type Secret string

func (s Secret) String() string { return "[REDACTED]" }

func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED]") }

// Expose returns the actual secret value. Use it exclusively to authenticate.
func (s Secret) Expose() string { return string(s) }

// Empty reports whether the secret holds no value.
func (s Secret) Empty() bool { return s == "" }

// SecureStorage is the interface for OS keychain access (Tauri keyring).
// The concrete backend lives in Rust; Go never persists secrets.
type SecureStorage interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// InMemory implements SecureStorage in memory (dev / tests only).
type InMemory struct{ data map[string]string }

func NewInMemory() *InMemory { return &InMemory{data: map[string]string{}} }

func (s *InMemory) Get(key string) (string, error) {
	if v, ok := s.data[key]; ok {
		return v, nil
	}
	return "", fmt.Errorf("no secret for %q", key)
}
func (s *InMemory) Set(key, value string) error { s.data[key] = value; return nil }
func (s *InMemory) Delete(key string) error     { delete(s.data, key); return nil }

// Credential bundles what a provider needs to authenticate.
type Credential struct {
	AccountID string
	Kind      string // oauth2 | password | app-password
	Secret    Secret // only ever in SecureStorage / RuntimeSecrets
}

// RuntimeSecrets keeps secrets in memory for the engine process lifetime.
// Tauri re-delivers them after an engine restart; they are never written to
// disk, included in IPC responses, or logged (Secret redacts itself).
type RuntimeSecrets struct {
	mu      sync.RWMutex
	secrets map[string]Secret
}

func NewRuntimeSecrets() *RuntimeSecrets {
	return &RuntimeSecrets{secrets: map[string]Secret{}}
}

func (s *RuntimeSecrets) Put(accountID string, secret Secret) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets[accountID] = secret
}

func (s *RuntimeSecrets) Get(accountID string) (Secret, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.secrets[accountID]
	return v, ok
}

// Forget wipes a secret from memory (best effort zeroing, then delete).
func (s *RuntimeSecrets) Forget(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.secrets[accountID]; ok {
		b := []byte(v)
		for i := range b {
			b[i] = 0
		}
		delete(s.secrets, accountID)
	}
}

// OAuth flow placeholder for the Gmail/Microsoft phases.
type OAuthFlow interface {
	// Start returns a URL the user opens to authorize.
	Start(accountID string) (string, error)
	// Finish exchanges the authorization code for a token.
	Finish(accountID, code string) (string, error)
}
