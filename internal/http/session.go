package httpapi

import (
	"net/http"
	"sync"
	"time"
)

type Session struct {
	Token      string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

type SessionStore struct {
	mu         sync.RWMutex
	tokens     map[string]Session
	cookieName string
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		tokens:     make(map[string]Session),
		cookieName: "cloud_clipboard_session",
	}
}

func (s *SessionStore) Create(token string, w http.ResponseWriter) {
	now := time.Now().UTC()
	s.mu.Lock()
	s.tokens[token] = Session{Token: token, CreatedAt: now, LastSeenAt: now}
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    token,
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
		MaxAge:   60 * 60 * 24,
	})
}

func (s *SessionStore) Get(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie(s.cookieName)
	if err != nil {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.tokens[cookie.Value]
	if !ok {
		return Session{}, false
	}
	session.LastSeenAt = time.Now().UTC()
	s.tokens[cookie.Value] = session
	return session, true
}

func (s *SessionStore) Destroy(r *http.Request, w http.ResponseWriter) {
	cookie, err := r.Cookie(s.cookieName)
	if err == nil {
		s.mu.Lock()
		delete(s.tokens, cookie.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
		MaxAge:   -1,
	})
}

func (s *SessionStore) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.Get(r); !ok {
			errorJSON(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next(w, r)
	}
}
