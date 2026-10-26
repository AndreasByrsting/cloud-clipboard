package httpapi

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultSessionCookieName = "cloud_clipboard_session"
	sessionIdleTimeout       = 2 * time.Hour
	sessionAbsoluteTimeout   = 24 * time.Hour
)

type Session struct {
	Token                  string
	CreatedAt              time.Time
	LastSeenAt             time.Time
	PasswordChangeRequired bool
}

type SessionStore struct {
	mu         sync.RWMutex
	tokens     map[string]Session
	cookieName string
	now        func() time.Time
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		tokens:     make(map[string]Session),
		cookieName: defaultSessionCookieName,
	}
}

func (s *SessionStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Create stores a new admin session and writes the session cookie. The cookie
// is marked Secure when the request arrived over TLS or through a proxy that
// reported https via the first X-Forwarded-Proto value.
func (s *SessionStore) Create(token string, passwordChangeRequired bool, w http.ResponseWriter, r *http.Request) {
	now := s.clock()
	s.mu.Lock()
	s.tokens[token] = Session{
		Token:                  token,
		CreatedAt:              now,
		LastSeenAt:             now,
		PasswordChangeRequired: passwordChangeRequired,
	}
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    token,
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteStrictMode,
		Secure:   sessionCookieSecure(r),
		MaxAge:   int(sessionAbsoluteTimeout / time.Second),
	})
}

// Get returns a live session. Sessions expire after 24h absolute or 2h idle;
// expired entries are removed eagerly on access.
func (s *SessionStore) Get(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie(s.cookieName)
	if err != nil {
		return Session{}, false
	}

	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.tokens[cookie.Value]
	if !ok {
		return Session{}, false
	}
	if now.Sub(session.CreatedAt) > sessionAbsoluteTimeout || now.Sub(session.LastSeenAt) > sessionIdleTimeout {
		delete(s.tokens, cookie.Value)
		return Session{}, false
	}

	session.LastSeenAt = now
	s.tokens[cookie.Value] = session
	return session, true
}

func (s *SessionStore) Destroy(r *http.Request, w http.ResponseWriter) {
	secure := sessionCookieSecure(r)
	if cookie, err := r.Cookie(s.cookieName); err == nil {
		s.mu.Lock()
		delete(s.tokens, cookie.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		MaxAge:   -1,
	})
}

// DestroyAll invalidates every admin session. It is called after a password
// change so that previously issued cookies can no longer be used.
func (s *SessionStore) DestroyAll() {
	s.mu.Lock()
	s.tokens = make(map[string]Session)
	s.mu.Unlock()
}

func (s *SessionStore) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := s.Get(r)
		if !ok {
			errorJSON(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if session.PasswordChangeRequired && !passwordChangeAllowedPath(r.URL.Path) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "请先修改初始管理员密码",
				"code":  "password_change_required",
			})
			return
		}
		next(w, r)
	}
}

func passwordChangeAllowedPath(path string) bool {
	switch path {
	case "/api/admin/password", "/api/admin/logout", "/api/admin/session":
		return true
	default:
		return false
	}
}

func sessionCookieSecure(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}
