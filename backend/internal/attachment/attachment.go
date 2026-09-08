// Package attachment stores attachment metadata and content.
package attachment

import (
	"database/sql"

	"mail/backend/internal/domain"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// Create records attachment metadata for a message.
func (s *Store) Create(a domain.Attachment) error {
	_, err := s.db.Exec(
		`INSERT INTO attachments(id,message,name,size,content_type) VALUES(?,?,?,?,?)`,
		a.ID, a.MessageID, a.Name, a.Size, a.ContentType,
	)
	return err
}

// ListByMessage returns attachments for a message.
func (s *Store) ListByMessage(messageID string) ([]domain.Attachment, error) {
	rows, err := s.db.Query(
		`SELECT id,message,name,size,content_type FROM attachments WHERE message=?`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Attachment
	for rows.Next() {
		var a domain.Attachment
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Name, &a.Size, &a.ContentType); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM attachments WHERE id=?`, id)
	return err
}
