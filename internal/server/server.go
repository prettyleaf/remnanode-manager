package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/prettyleaf/remnanode-manager/internal/buildinfo"
	"github.com/prettyleaf/remnanode-manager/internal/checker"
	"github.com/prettyleaf/remnanode-manager/internal/config"
	"github.com/prettyleaf/remnanode-manager/internal/notify"
	"github.com/prettyleaf/remnanode-manager/internal/settings"
	"github.com/prettyleaf/remnanode-manager/internal/store"
)

type Server struct {
	cfg     config.Config
	store   *store.Store
	checker *checker.Checker
	updater *buildinfo.Updater
	log     *slog.Logger
	version string
	ui      fs.FS
	limiter *loginLimiter
}

func New(cfg config.Config, st *store.Store, ch *checker.Checker, updater *buildinfo.Updater, log *slog.Logger, ui fs.FS) *Server {
	return &Server{cfg: cfg, store: st, checker: ch, updater: updater, log: log, version: buildinfo.Version, ui: ui, limiter: newLoginLimiter()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.version})
	})
	mux.HandleFunc("GET /api/auth/state", s.handleAuthState)
	mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)

	mux.HandleFunc("GET /api/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("PUT /api/me", s.requireAuth(s.handleUpdateMe))

	mux.HandleFunc("GET /api/status", s.requireAuth(s.handleStatus))
	mux.HandleFunc("GET /api/about", s.requireAuth(s.handleAbout))
	mux.HandleFunc("POST /api/check", s.requireAuth(s.handleCheckNow))
	mux.HandleFunc("GET /api/events", s.requireAuth(s.handleEvents))

	mux.HandleFunc("POST /api/hosts/{uuid}/enable", s.requireAuth(s.handleHostEnable))
	mux.HandleFunc("POST /api/hosts/{uuid}/forget", s.requireAuth(s.handleHostForget))
	mux.HandleFunc("POST /api/hosts/{uuid}/exclude", s.requireAuth(s.handleExclude("host")))
	mux.HandleFunc("GET /api/hosts/{uuid}/references", s.requireAuth(s.handleHostReferences))
	mux.HandleFunc("POST /api/nodes/{uuid}/exclude", s.requireAuth(s.handleExclude("node")))

	mux.HandleFunc("GET /api/settings", s.requireAuth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", s.requireAuth(s.handlePutSettings))
	mux.HandleFunc("POST /api/settings/test-panel", s.requireAuth(s.handleTestPanel))
	mux.HandleFunc("POST /api/settings/test-telegram", s.requireAuth(s.handleTestTelegram))
	mux.HandleFunc("GET /api/templates", s.requireAuth(s.handleTemplates))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	mux.HandleFunc("/", s.handleUI)

	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")

		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
			// JSON-only mutations: browsers cannot send these cross-site
			// without a CORS preflight, which this server never approves.
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if ct != "application/json" {
					writeError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- auth ----

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.AdminCount(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{"setupRequired": n == 0, "authenticated": false, "version": s.version}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if sess, err := s.store.Session(r.Context(), hashToken(c.Value)); err == nil {
			if a, err := s.store.AdminByID(r.Context(), sess.AdminID); err == nil {
				resp["authenticated"] = true
				resp["username"] = a.Username
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !readJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if err := validCredentials(req.Username, req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := s.store.CreateFirstAdmin(r.Context(), req.Username, hash)
	if err != nil {
		writeError(w, http.StatusConflict, "setup already completed")
		return
	}
	s.audit(r, "admin_created", "Admin account created: "+req.Username, map[string]any{"username": req.Username})
	if err := s.startSession(w, r, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": req.Username})
}

// dummyHash keeps login timing similar for unknown usernames.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("remnanode-manager"), 12)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.cfg.TrustProxy)
	if wait := s.limiter.blocked(ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var req credentials
	if !readJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	admin, err := s.store.AdminByUsername(r.Context(), req.Username)
	hash := dummyHash
	if err == nil {
		hash = []byte(admin.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(req.Password)) != nil || err != nil {
		s.limiter.fail(ip)
		s.audit(r, "login_failed", "Failed login attempt for "+req.Username+" from "+ip, map[string]any{"username": req.Username, "ip": ip})
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.limiter.success(ip)
	if err := s.startSession(w, r, admin.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "login", "Admin "+admin.Username+" logged in from "+ip, map[string]any{"username": admin.Username, "ip": ip})
	writeJSON(w, http.StatusOK, map[string]any{"username": admin.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"username": currentAuth(r).admin.Username})
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	auth := currentAuth(r)
	if bcrypt.CompareHashAndPassword([]byte(auth.admin.PasswordHash), []byte(req.CurrentPassword)) != nil {
		writeError(w, http.StatusBadRequest, "current password is incorrect")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = auth.admin.Username
	}
	password := req.NewPassword
	if password == "" {
		password = req.CurrentPassword
	}
	if err := validCredentials(username, password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := HashPassword(password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.UpdateAdmin(r.Context(), auth.admin.ID, username, hash); err != nil {
		writeError(w, http.StatusBadRequest, "username is already taken")
		return
	}
	if req.NewPassword != "" {
		_ = s.store.DeleteAdminSessions(r.Context(), auth.admin.ID, auth.tokenHash)
	}
	s.audit(r, "account_updated", "Admin account updated: "+username, map[string]any{"username": username})
	writeJSON(w, http.StatusOK, map[string]any{"username": username})
}

// ---- status & events ----

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Settings()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":  s.version,
		"snapshot": s.checker.Snapshot(),
		"checker": map[string]any{
			"enabled":         cfg.Checker.Enabled,
			"dryRun":          cfg.Checker.DryRun,
			"policy":          cfg.Checker.Policy,
			"downMinutes":     cfg.Checker.DownMinutes,
			"recoveryMinutes": cfg.Checker.RecoveryMinutes,
			"intervalSeconds": cfg.Checker.IntervalSeconds,
			"autoEnable":      cfg.Checker.AutoEnable,
			"mihomo":          cfg.Mihomo.Enabled,
			"telegram":        cfg.Telegram.Enabled,
			"configured":      cfg.PanelConfigured(),
		},
	})
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"build":        buildinfo.Get(),
		"update":       s.updater.Status(),
		"panelVersion": s.checker.Snapshot().PanelVersion,
	})
}

func (s *Server) handleCheckNow(w http.ResponseWriter, r *http.Request) {
	s.checker.Trigger()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	events, err := s.store.Events(r.Context(), store.EventFilter{
		Type: q.Get("type"), Level: q.Get("level"), Search: q.Get("search"), Before: before, Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// ---- hosts & nodes ----

func (s *Server) handleHostEnable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if err := s.checker.EnableNow(ctx, r.PathValue("uuid")); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, checker.ErrNotManaged) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.checker.Trigger()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleHostForget(w http.ResponseWriter, r *http.Request) {
	if err := s.checker.Forget(r.Context(), r.PathValue("uuid")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, checker.ErrNotManaged) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.checker.Trigger()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleExclude(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Excluded bool `json:"excluded"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		id := r.PathValue("uuid")
		cfg := s.store.Settings()
		list := &cfg.Checker.ExcludedHosts
		if kind == "node" {
			list = &cfg.Checker.ExcludedNodes
		}
		out := make([]string, 0, len(*list)+1)
		for _, v := range *list {
			if v != id {
				out = append(out, v)
			}
		}
		if req.Excluded {
			out = append(out, id)
		}
		*list = out
		if err := s.store.SaveSettings(r.Context(), cfg); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.checker.Trigger()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func (s *Server) handleHostReferences(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("uuid")
	remark := ""
	for _, h := range s.checker.Snapshot().Hosts {
		if h.UUID == id {
			remark = h.Remark
		}
	}
	if remark == "" {
		writeError(w, http.StatusNotFound, "host not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	refs, err := s.checker.TemplateReferences(ctx, remark)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"remark": remark, "templates": refs})
}

// ---- settings ----

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Settings()
	secrets := map[string]bool{"panelToken": cfg.Panel.Token != "", "botToken": cfg.Telegram.BotToken != ""}
	cfg.Panel.Token = ""
	cfg.Telegram.BotToken = ""
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":         cfg,
		"secrets":          secrets,
		"eventTypes":       settings.EventTypes,
		"defaultTemplates": settings.DefaultTemplates,
		"templateTypes":    settings.MihomoTemplateTypes,
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var next settings.Settings
	if !readJSON(w, r, &next) {
		return
	}
	cur := s.store.Settings()
	if next.Panel.Token == "" {
		next.Panel.Token = cur.Panel.Token
	}
	if next.Telegram.BotToken == "" {
		next.Telegram.BotToken = cur.Telegram.BotToken
	}
	// Exclusions have dedicated endpoints; a stale settings form must not
	// overwrite them.
	next.Checker.ExcludedNodes = cur.Checker.ExcludedNodes
	next.Checker.ExcludedHosts = cur.Checker.ExcludedHosts
	if err := s.store.SaveSettings(r.Context(), next); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "settings_updated", "Settings updated by "+currentAuth(r).admin.Username, map[string]any{"username": currentAuth(r).admin.Username})
	s.checker.Trigger()
	s.handleGetSettings(w, r)
}

func (s *Server) handleTestPanel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Panel settings.Panel `json:"panel"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	cfg := s.store.Settings()
	if req.Panel.Token == "" {
		req.Panel.Token = cfg.Panel.Token
	}
	cfg.Panel = req.Panel
	cfg.Normalize()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	info, err := s.checker.TestPanel(ctx, cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleTestTelegram(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Telegram settings.Telegram `json:"telegram"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	cfg := s.store.Settings()
	if req.Telegram.BotToken == "" {
		req.Telegram.BotToken = cfg.Telegram.BotToken
	}
	cfg.Telegram = req.Telegram
	cfg.Normalize()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	text := "✅ <b>Remnanode Manager</b>\n\nТестовое сообщение: уведомления настроены верно."
	if err := notify.SendTest(ctx, cfg.Telegram, text); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	list, err := s.checker.Templates(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": list})
}

// ---- UI ----

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" && name != "index.html" {
		if f, err := s.ui.Open(name); err == nil {
			stat, _ := f.Stat()
			f.Close()
			if stat != nil && !stat.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, s.ui, name)
				return
			}
		}
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
	}
	index, err := fs.ReadFile(s.ui, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "Web UI is not built. Run `pnpm --dir web build` and rebuild the binary.\n")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}

// ---- helpers ----

func (s *Server) audit(r *http.Request, typ, msg string, meta map[string]any) {
	level := "info"
	if typ == "login_failed" {
		level = "warn"
	}
	if err := s.store.AddEvent(r.Context(), store.Event{Level: level, Type: typ, Message: msg, Meta: meta}); err != nil {
		s.log.Error("store event", "err", err)
	}
	s.log.Info(msg, "type", typ)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
