package httpapi

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	loginFailureWindow = 15 * time.Minute
	loginFailureLimit  = 5
	loginLockDuration  = 15 * time.Minute
	loginFailureDelay  = 300 * time.Millisecond
)

type loginAttempt struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
	lastSeen    time.Time
}

// loginRateLimiter is a small in-memory limiter keyed by client IP. It locks
// an IP for 15 minutes after 5 failed logins inside a 15 minute window. A
// successful login clears the counter.
type loginRateLimiter struct {
	mu          sync.Mutex
	attempts    map[string]*loginAttempt
	now         func() time.Time
	lastCleanup time.Time
}

func newLoginRateLimiter() *loginRateLimiter {
	return &loginRateLimiter{
		attempts: make(map[string]*loginAttempt),
		now:      time.Now,
	}
}

func (l *loginRateLimiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// Check reports whether a login attempt from ip is allowed. When it is not,
// the returned duration is the remaining lock time.
func (l *loginRateLimiter) Check(ip string) (bool, time.Duration) {
	now := l.clock()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)

	attempt := l.attempts[ip]
	if attempt == nil {
		return true, 0
	}
	if !attempt.lockedUntil.IsZero() {
		if now.Before(attempt.lockedUntil) {
			return false, attempt.lockedUntil.Sub(now)
		}
		// The lock expired; start from a clean slate.
		delete(l.attempts, ip)
		return true, 0
	}
	if now.Sub(attempt.windowStart) > loginFailureWindow {
		delete(l.attempts, ip)
		return true, 0
	}
	return true, 0
}

// Fail records one failed login for ip and locks the IP once the threshold is
// reached.
func (l *loginRateLimiter) Fail(ip string) {
	now := l.clock()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)

	attempt := l.attempts[ip]
	if attempt == nil {
		attempt = &loginAttempt{windowStart: now}
		l.attempts[ip] = attempt
	} else if now.Sub(attempt.windowStart) > loginFailureWindow {
		attempt.failures = 0
		attempt.windowStart = now
		attempt.lockedUntil = time.Time{}
	}

	attempt.failures++
	attempt.lastSeen = now
	if attempt.failures >= loginFailureLimit {
		attempt.lockedUntil = now.Add(loginLockDuration)
	}
}

// Success clears the failure counter for ip.
func (l *loginRateLimiter) Success(ip string) {
	l.mu.Lock()
	delete(l.attempts, ip)
	l.mu.Unlock()
}

// cleanupLocked drops idle entries; it runs at most once every 10 minutes.
// The caller must hold l.mu.
func (l *loginRateLimiter) cleanupLocked(now time.Time) {
	if !l.lastCleanup.IsZero() && now.Sub(l.lastCleanup) < 10*time.Minute {
		return
	}
	l.lastCleanup = now
	for ip, attempt := range l.attempts {
		if attempt.lockedUntil.After(now) {
			continue
		}
		if now.Sub(attempt.lastSeen) > time.Hour {
			delete(l.attempts, ip)
		}
	}
}

// loginClientIP extracts the client IP from RemoteAddr. It deliberately does
// not trust X-Forwarded-For, which can be spoofed by clients.
func loginClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	remote := strings.TrimSpace(r.RemoteAddr)
	if remote == "" {
		return "unknown"
	}
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return strings.Trim(remote, "[]")
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeJSON(w, http.StatusTooManyRequests, map[string]string{
		"error": "登录失败次数过多，请稍后再试",
		"code":  "rate_limited",
	})
}
