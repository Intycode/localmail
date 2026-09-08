package auth

import (
	"testing"
)

func TestInMemorySecureStorage(t *testing.T) {
	s := NewInMemory()
	if err := s.Set("token", "abc"); err != nil {
		t.Fatal(err)
	}
	v, err := s.Get("token")
	if err != nil || v != "abc" {
		t.Errorf("get = %q err=%v", v, err)
	}
	if err := s.Delete("token"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("token"); err == nil {
		t.Errorf("expected error after delete")
	}
}
