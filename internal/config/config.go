package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Config holds process-level settings read from the environment. Everything
// that can be changed at runtime lives in the database and the web UI.
type Config struct {
	Host          string
	Port          int
	DataDir       string
	AdminUsername string
	AdminPassword string
	TrustProxy    bool
	CookieSecure  string // auto | true | false
	PanelURL      string
	PanelToken    string
	UpdateCheck   bool
	LogLevel      slog.Level
}

func Load() Config {
	c := Config{
		Host:          env("APP_HOST", "0.0.0.0"),
		Port:          envInt("APP_PORT", 3100),
		DataDir:       env("DATA_DIR", "./data"),
		AdminUsername: os.Getenv("ADMIN_USERNAME"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		TrustProxy:    envBool("TRUST_PROXY", false),
		CookieSecure:  strings.ToLower(env("COOKIE_SECURE", "auto")),
		PanelURL:      os.Getenv("REMNAWAVE_PANEL_URL"),
		PanelToken:    os.Getenv("REMNAWAVE_API_TOKEN"),
		UpdateCheck:   envBool("UPDATE_CHECK", true),
		LogLevel:      slog.LevelInfo,
	}
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "warn", "warning":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	}
	return c
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return def
}
