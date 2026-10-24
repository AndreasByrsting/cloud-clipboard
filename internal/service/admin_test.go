package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cloud-clipboard/internal/db"
	"cloud-clipboard/internal/store"
)

func newTestAdminService(t *testing.T) (*AdminService, *store.AdminStore) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	schema, err := os.ReadFile(filepath.Join("..", "..", "sql", "init.sql"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if err := db.Migrate(database, string(schema)); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	adminStore := store.NewAdminStore(database)
	return NewAdminService(adminStore), adminStore
}

func TestChangePasswordRequiresCurrentPassword(t *testing.T) {
	svc, adminStore := newTestAdminService(t)
	now := time.Now()

	oldHash, err := hashPassword("old-password")
	if err != nil {
		t.Fatalf("hash old password: %v", err)
	}
	if err := adminStore.Upsert(oldHash, true, now); err != nil {
		t.Fatalf("seed admin credentials: %v", err)
	}

	if err := svc.ChangePassword("wrong-password", "new-password", now); err == nil || err.Error() != "current password is incorrect" {
		t.Fatalf("ChangePassword with wrong current password = %v", err)
	}

	cred, err := adminStore.Get()
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	if !cred.DefaultPasswordActive {
		t.Fatal("failed password change must not clear DefaultPasswordActive")
	}

	if err := svc.ChangePassword("old-password", "short", now); err == nil {
		t.Fatal("expected an error for a new password shorter than 8 characters")
	}

	if err := svc.ChangePassword("old-password", "new-password", now); err != nil {
		t.Fatalf("ChangePassword with correct current password: %v", err)
	}

	cred, err = adminStore.Get()
	if err != nil {
		t.Fatalf("read credentials after change: %v", err)
	}
	if cred.DefaultPasswordActive {
		t.Fatal("DefaultPasswordActive should be false after a successful password change")
	}

	matched, needsUpgrade, err := verifyPassword(cred.PasswordHash, "new-password")
	if err != nil {
		t.Fatalf("verify new password: %v", err)
	}
	if !matched || needsUpgrade {
		t.Fatalf("new password verify = (%v, %v), want (true, false)", matched, needsUpgrade)
	}

	matched, _, err = verifyPassword(cred.PasswordHash, "old-password")
	if err != nil {
		t.Fatalf("verify old password: %v", err)
	}
	if matched {
		t.Fatal("old password must no longer match")
	}
}

func TestVerifyLegacyPassword(t *testing.T) {
	encoded := legacyHashPassword("legacy-password")

	matched, needsUpgrade, err := verifyPassword(encoded, "legacy-password")
	if err != nil {
		t.Fatalf("verify legacy password: %v", err)
	}
	if !matched || !needsUpgrade {
		t.Fatalf("legacy verify = (%v, %v), want (true, true)", matched, needsUpgrade)
	}

	matched, _, err = verifyPassword(encoded, "other-password")
	if err != nil {
		t.Fatalf("verify mismatched legacy password: %v", err)
	}
	if matched {
		t.Fatal("mismatched legacy password must not verify")
	}
}
