package service

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"cloud-clipboard/internal/config"
	"cloud-clipboard/internal/store"
)

type AdminService struct {
	store *store.AdminStore
}

func NewAdminService(adminStore *store.AdminStore) *AdminService {
	return &AdminService{store: adminStore}
}

func (s *AdminService) EnsureInitialized(resetPassword string, now time.Time) error {
	if resetPassword != "" {
		hash, err := hashPassword(resetPassword)
		if err != nil {
			return err
		}
		return s.store.Upsert(hash, false, now)
	}

	_, err := s.store.Get()
	if err == nil {
		return nil
	}
	if err != store.ErrNotFound {
		return err
	}

	hash, err := hashPassword(config.DefaultAdminPassword)
	if err != nil {
		return err
	}
	return s.store.Upsert(hash, true, now)
}

func (s *AdminService) Status() (store.AdminCredential, bool, error) {
	cred, err := s.store.Get()
	if err == store.ErrNotFound {
		return store.AdminCredential{}, false, nil
	}
	if err != nil {
		return store.AdminCredential{}, false, err
	}
	return cred, true, nil
}

func (s *AdminService) Login(password string, now time.Time) (bool, store.AdminCredential, error) {
	cred, err := s.store.Get()
	if err != nil {
		return false, store.AdminCredential{}, err
	}

	matched, needsUpgrade, err := verifyPassword(cred.PasswordHash, password)
	if err != nil {
		return false, store.AdminCredential{}, err
	}
	if !matched {
		return false, store.AdminCredential{}, nil
	}
	if needsUpgrade {
		upgradedHash, err := hashPassword(password)
		if err != nil {
			return false, store.AdminCredential{}, err
		}
		if err := s.store.Upsert(upgradedHash, cred.DefaultPasswordActive, now); err != nil {
			return false, store.AdminCredential{}, err
		}
		cred.PasswordHash = upgradedHash
	}
	return true, cred, nil
}

func (s *AdminService) ChangePassword(newPassword string, now time.Time) error {
	if len(strings.TrimSpace(newPassword)) < 8 {
		return errors.New("new password must be at least 8 characters")
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.store.Upsert(hash, false, now)
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	const iterations = 120000
	hash := derivePasswordHash(password, salt, iterations)
	return fmt.Sprintf("pbkdf-sha256$%d$%s$%s", iterations, base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(hash)), nil
}

func verifyPassword(encodedHash string, password string) (matched bool, needsUpgrade bool, err error) {
	if strings.HasPrefix(encodedHash, "pbkdf-sha256$") {
		parts := strings.Split(encodedHash, "$")
		if len(parts) != 4 {
			return false, false, fmt.Errorf("invalid password hash format")
		}
		iterations := 0
		if _, err := fmt.Sscanf(parts[1], "%d", &iterations); err != nil || iterations <= 0 {
			return false, false, fmt.Errorf("invalid password hash iterations")
		}
		salt, err := base64.StdEncoding.DecodeString(parts[2])
		if err != nil {
			return false, false, err
		}
		expected, err := base64.StdEncoding.DecodeString(parts[3])
		if err != nil {
			return false, false, err
		}
		actual := derivePasswordHash(password, salt, iterations)
		return subtle.ConstantTimeCompare(expected, actual) == 1, false, nil
	}
	legacy := legacyHashPassword(password)
	return subtle.ConstantTimeCompare([]byte(encodedHash), []byte(legacy)) == 1, true, nil
}

func legacyHashPassword(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

func derivePasswordHash(password string, salt []byte, iterations int) []byte {
	block := append(append([]byte{}, salt...), []byte(password)...)
	sum := sha256.Sum256(block)
	result := sum[:]
	for i := 1; i < iterations; i++ {
		next := sha256.Sum256(append(append([]byte{}, result...), salt...))
		result = next[:]
	}
	final := make([]byte, len(result))
	copy(final, result)
	return final
}

func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
