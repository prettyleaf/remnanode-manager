package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/prettyleaf/remnanode-manager/internal/store"
)

const (
	sessionCookie = "rnm_session"
	sessionTTL    = 7 * 24 * time.Hour
)

type ctxKey struct{}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(h), err
}

func validCredentials(username, password string) error {
	if l := len(strings.TrimSpace(username)); l < 3 || l > 64 {
		return errors.New("username must be 3-64 characters")
	}
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len(password) > 72 {
		return errors.New("password must be at most 72 bytes")
	}
	return nil
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, adminID int64) error {
	token, err := newToken()
	if err != nil {
		return err
	}
	if err := s.store.CreateSession(r.Context(), hashToken(token), adminID, sessionTTL, clientIP(r, s.cfg.TrustProxy), r.UserAgent()); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return nil
}

func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: s.secureCookie(r), SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

func (s *Server) secureCookie(r *http.Request) bool {
	switch s.cfg.CookieSecure {
	case "true":
		return true
	case "false":
		return false
	}
	return r.TLS != nil || (s.cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}

type authInfo struct {
	admin     *store.Admin
	tokenHash string
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		th := hashToken(c.Value)
		sess, err := s.store.Session(r.Context(), th)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		admin, err := s.store.AdminByID(r.Context(), sess.AdminID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// Sliding expiration, refreshed at most once an hour.
		if time.Until(sess.ExpiresAt) < sessionTTL-time.Hour {
			_ = s.store.TouchSession(r.Context(), th, sessionTTL)
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, &authInfo{admin: admin, tokenHash: th})
		next(w, r.WithContext(ctx))
	}
}

func currentAuth(r *http.Request) *authInfo {
	a, _ := r.Context().Value(ctxKey{}).(*authInfo)
	return a
}

// loginLimiter slows down password guessing per client IP.
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*limitEntry
}

type limitEntry struct {
	failures int
	until    time.Time
	last     time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{entries: map[string]*limitEntry{}} }

func (l *loginLimiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[ip]
	if e == nil {
		return 0
	}
	return max(time.Until(e.until), 0)
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, e := range l.entries {
		if now.Sub(e.last) > time.Hour {
			delete(l.entries, k)
		}
	}
	e := l.entries[ip]
	if e == nil {
		e = &limitEntry{}
		l.entries[ip] = e
	}
	e.failures++
	e.last = now
	if e.failures >= 5 {
		// 30s, 60s, 120s ... capped at 15 minutes.
		backoff := 30 * time.Second << min(e.failures-5, 5)
		e.until = now.Add(min(backoff, 15*time.Minute))
	}
}

func (l *loginLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, ip)
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
