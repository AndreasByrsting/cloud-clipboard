package httpapi

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testSessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == defaultSessionCookieName {
			return cookie
		}
	}
	t.Fatalf("session cookie %q was not set", defaultSessionCookieName)
	return nil
}

func sessionStillValid(store *SessionStore, cookie *http.Cookie) bool {
	req := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	_, ok := store.Get(req)
	return ok
}

func TestSessionTTL(t *testing.T) {
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	store := NewSessionStore()
	store.now = func() time.Time { return now }

	create := func(token string) *http.Cookie {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
		store.Create(token, false, rec, req)
		return testSessionCookie(t, rec)
	}

	idleCookie := create("token-idle")
	if !sessionStillValid(store, idleCookie) {
		t.Fatal("session should be valid right after creation")
	}
	now = now.Add(sessionIdleTimeout + time.Minute)
	if sessionStillValid(store, idleCookie) {
		t.Fatal("session should expire after the idle timeout")
	}

	now = time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	absoluteCookie := create("token-absolute")
	// Keep the session active every hour so only the absolute TTL can expire it.
	for i := 0; i < int(sessionAbsoluteTimeout/time.Hour)-1; i++ {
		now = now.Add(time.Hour)
		if !sessionStillValid(store, absoluteCookie) {
			t.Fatalf("session should remain valid at hour %d", i+1)
		}
	}
	now = now.Add(2*time.Hour + time.Minute)
	if sessionStillValid(store, absoluteCookie) {
		t.Fatal("session should expire after the absolute timeout even when active")
	}
}

func TestSessionCookieFlags(t *testing.T) {
	cases := []struct {
		name       string
		withTLS    bool
		forwarded  string
		wantSecure bool
	}{
		{name: "plain http", wantSecure: false},
		{name: "tls", withTLS: true, wantSecure: true},
		{name: "proxy https first value", forwarded: "https, http", wantSecure: true},
		{name: "proxy https uppercase", forwarded: "HTTPS", wantSecure: true},
		{name: "proxy http", forwarded: "http", wantSecure: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewSessionStore()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
			if tc.withTLS {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}
			store.Create("token", false, rec, req)

			cookie := testSessionCookie(t, rec)
			if cookie.Secure != tc.wantSecure {
				t.Fatalf("Secure = %v, want %v", cookie.Secure, tc.wantSecure)
			}
			if !cookie.HttpOnly {
				t.Fatal("cookie must be HttpOnly")
			}
			if cookie.SameSite != http.SameSiteStrictMode {
				t.Fatalf("SameSite = %v, want Strict", cookie.SameSite)
			}
			if cookie.Path != "/" {
				t.Fatalf("Path = %q, want /", cookie.Path)
			}
			if cookie.MaxAge != int(sessionAbsoluteTimeout/time.Second) {
				t.Fatalf("MaxAge = %d, want %d", cookie.MaxAge, int(sessionAbsoluteTimeout/time.Second))
			}
		})
	}
}

func TestRequirePasswordChangeGate(t *testing.T) {
	store := NewSessionStore()
	rec := httptest.NewRecorder()
	store.Create("token", true, rec, httptest.NewRequest(http.MethodPost, "/api/admin/login", nil))
	cookie := testSessionCookie(t, rec)

	run := func(path string) (*httptest.ResponseRecorder, bool) {
		called := false
		handler := store.Require(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
		w := httptest.NewRecorder()
		handler(w, req)
		return w, called
	}

	w, called := run("/api/admin/settings")
	if called {
		t.Fatal("handler must not run while a password change is required")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != "请先修改初始管理员密码" {
		t.Fatalf("error = %q", body["error"])
	}
	if body["code"] != "password_change_required" {
		t.Fatalf("code = %q", body["code"])
	}

	for _, path := range []string{"/api/admin/password", "/api/admin/logout", "/api/admin/session"} {
		if _, called := run(path); !called {
			t.Fatalf("%s should be allowed while a password change is required", path)
		}
	}
}

func TestRequireUnauthenticated(t *testing.T) {
	store := NewSessionStore()
	handler := store.Require(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run without a session")
	})
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestLoginRateLimiter(t *testing.T) {
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	limiter := newLoginRateLimiter()
	limiter.now = func() time.Time { return now }

	ip := "203.0.113.9"
	for i := 0; i < loginFailureLimit; i++ {
		if allowed, _ := limiter.Check(ip); !allowed {
			t.Fatalf("attempt %d should be allowed before the lock", i+1)
		}
		limiter.Fail(ip)
	}

	allowed, retryAfter := limiter.Check(ip)
	if allowed {
		t.Fatal("login should be locked after reaching the failure limit")
	}
	if retryAfter <= 0 || retryAfter > loginLockDuration {
		t.Fatalf("retryAfter = %v, want (0, %v]", retryAfter, loginLockDuration)
	}

	now = now.Add(loginLockDuration + time.Second)
	if allowed, _ := limiter.Check(ip); !allowed {
		t.Fatal("login should be allowed after the lock expires")
	}

	limiter.Fail(ip)
	limiter.Success(ip)
	if _, exists := limiter.attempts[ip]; exists {
		t.Fatal("successful login should clear the failure counter")
	}
}

func TestLoginClientIP(t *testing.T) {
	cases := []struct {
		remote string
		want   string
	}{
		{remote: "192.0.2.1:4567", want: "192.0.2.1"},
		{remote: "[2001:db8::1]:443", want: "2001:db8::1"},
		{remote: "192.0.2.1", want: "192.0.2.1"},
		{remote: "", want: "unknown"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
		req.RemoteAddr = tc.remote
		if got := loginClientIP(req); got != tc.want {
			t.Fatalf("loginClientIP(%q) = %q, want %q", tc.remote, got, tc.want)
		}
	}
}
