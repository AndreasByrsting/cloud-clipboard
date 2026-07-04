package store

import (
	"database/sql"
	"errors"
	"time"
)

type AdminCredential struct {
	PasswordHash          string `json:"-"`
	DefaultPasswordActive bool   `json:"defaultPasswordActive"`
	UpdatedAt             int64  `json:"updatedAt"`
}

type AdminStore struct {
	db *sql.DB
}

func NewAdminStore(db *sql.DB) *AdminStore {
	return &AdminStore{db: db}
}

func (s *AdminStore) Get() (AdminCredential, error) {
	var cred AdminCredential
	var defaultActive int
	err := s.db.QueryRow(`SELECT password_hash, default_password_active, updated_at FROM admin_credentials WHERE id = 1`).Scan(&cred.PasswordHash, &defaultActive, &cred.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AdminCredential{}, ErrNotFound
		}
		return AdminCredential{}, err
	}
	cred.DefaultPasswordActive = defaultActive == 1
	return cred, nil
}

func (s *AdminStore) Upsert(passwordHash string, defaultActive bool, now time.Time) error {
	defaultFlag := 0
	if defaultActive {
		defaultFlag = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO admin_credentials(id, password_hash, default_password_active, updated_at)
		VALUES(1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			password_hash = excluded.password_hash,
			default_password_active = excluded.default_password_active,
			updated_at = excluded.updated_at
	`, passwordHash, defaultFlag, now.Unix())
	return err
}
