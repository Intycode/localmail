package rule

// Rule tests (M8): condition matching is case-insensitive and complete; the
// store filters by account (global rules apply everywhere) and honors
// enabled/position ordering.

import (
	"path/filepath"
	"testing"

	"mail/backend/internal/database"
	"mail/backend/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func sampleMsg() *domain.Message {
	return &domain.Message{
		Subject:   "Newsletter di settembre",
		From:      domain.Recipient{Email: "news@shop.example", Name: "Shop"},
		To:        []domain.Recipient{{Email: "luca@x.dev"}},
		BodyText:  "Offerte del mese",
		SpamScore: 12,
	}
}

func TestMatchingFromContainsCaseInsensitive(t *testing.T) {
	r := domain.Rule{Field: "from", Op: "contains", Value: "SHOP.EXAMPLE"}
	if len(Matching([]domain.Rule{r}, sampleMsg())) != 1 {
		t.Fatal("from contains non corrisposto")
	}
	r.Value = "gmail.com"
	if len(Matching([]domain.Rule{r}, sampleMsg())) != 0 {
		t.Fatal("falso positivo su from contains")
	}
}

func TestMatchingSubjectOps(t *testing.T) {
	m := sampleMsg()
	if len(Matching([]domain.Rule{{Field: "subject", Op: "contains", Value: "SETTEMBRE"}}, m)) != 1 {
		t.Fatal("subject contains fallito")
	}
	if len(Matching([]domain.Rule{{Field: "subject", Op: "starts_with", Value: "newsletter"}}, m)) != 1 {
		t.Fatal("subject starts_with fallito")
	}
	if len(Matching([]domain.Rule{{Field: "subject", Op: "ends_with", Value: "settembre"}}, m)) != 1 {
		t.Fatal("subject ends_with fallito")
	}
	if len(Matching([]domain.Rule{{Field: "subject", Op: "equals", Value: "newsletter di settembre"}}, m)) != 1 {
		t.Fatal("subject equals fallito")
	}
	if len(Matching([]domain.Rule{{Field: "subject", Op: "equals", Value: "altro"}}, m)) != 0 {
		t.Fatal("falso positivo su equals")
	}
}

func TestMatchingToAnyAndAttachments(t *testing.T) {
	m := sampleMsg()
	if len(Matching([]domain.Rule{{Field: "to", Op: "contains", Value: "luca@x.dev"}}, m)) != 1 {
		t.Fatal("to contains fallito")
	}
	if len(Matching([]domain.Rule{{Field: "has_attachment", Op: "any"}}, m)) != 0 {
		t.Fatal("has_attachment vero senza allegati")
	}
	m.Attachments = []domain.Attachment{{Name: "f.pdf", Size: 1}}
	if len(Matching([]domain.Rule{{Field: "has_attachment", Op: "any"}}, m)) != 1 {
		t.Fatal("has_attachment non rilevato")
	}
}

func TestMatchingSpamScore(t *testing.T) {
	m := sampleMsg()
	m.SpamScore = 65
	if len(Matching([]domain.Rule{{Field: "spam_score", Op: "at_least", Value: "60"}}, m)) != 1 {
		t.Fatal("spam_score >= non corrisposto")
	}
	if len(Matching([]domain.Rule{{Field: "spam_score", Op: "at_least", Value: "70"}}, m)) != 0 {
		t.Fatal("spam_score falso positivo")
	}
	if len(Matching([]domain.Rule{{Field: "spam_score", Op: "at_least", Value: "abc"}}, m)) != 0 {
		t.Fatal("valore non numerico deve non corrispondere")
	}
}

func TestStoreCRUDAndAccountFilter(t *testing.T) {
	s := testStore(t)

	global := domain.Rule{Name: "globale", Enabled: true, Field: "subject", Op: "contains", Value: "x", Action: "star"}
	if err := s.Save(&global); err != nil {
		t.Fatalf("save global: %v", err)
	}
	if global.ID == "" || global.Position != 1 {
		t.Fatalf("id/position non assegnati: %+v", global)
	}
	specific := domain.Rule{Account: "a1", Name: "specifica", Enabled: true, Field: "from", Op: "contains", Value: "y", Action: "mark_read"}
	if err := s.Save(&specific); err != nil {
		t.Fatalf("save specific: %v", err)
	}
	if specific.Position != 2 {
		t.Fatalf("position append fallita: %d", specific.Position)
	}
	disabled := domain.Rule{Account: "a1", Name: "spenta", Enabled: false, Field: "to", Op: "any", Action: "star"}
	if err := s.Save(&disabled); err != nil {
		t.Fatalf("save disabled: %v", err)
	}

	got, err := s.ForAccount("a1")
	if err != nil {
		t.Fatalf("ForAccount: %v", err)
	}
	if len(got) != 2 || got[0].Name != "globale" || got[1].Name != "specifica" {
		t.Fatalf("ForAccount = %+v", got)
	}
	gotOther, _ := s.ForAccount("a2")
	if len(gotOther) != 1 || gotOther[0].Name != "globale" {
		t.Fatalf("regole globali perse: %+v", gotOther)
	}

	all, _ := s.All()
	if len(all) != 3 {
		t.Fatalf("All = %d regole", len(all))
	}

	if err := s.Delete(specific.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all, _ = s.All()
	if len(all) != 2 {
		t.Fatalf("delete non applicata: %d", len(all))
	}

	if _, err := s.Get("r-mancante"); err == nil {
		t.Fatal("Get su id inesistente deve fallire")
	}
}
