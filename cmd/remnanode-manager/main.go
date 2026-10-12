package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prettyleaf/remnanode-manager/internal/buildinfo"
	"github.com/prettyleaf/remnanode-manager/internal/checker"
	"github.com/prettyleaf/remnanode-manager/internal/config"
	"github.com/prettyleaf/remnanode-manager/internal/notify"
	"github.com/prettyleaf/remnanode-manager/internal/server"
	"github.com/prettyleaf/remnanode-manager/internal/settings"
	"github.com/prettyleaf/remnanode-manager/internal/store"
	"github.com/prettyleaf/remnanode-manager/internal/webui"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			b := buildinfo.Get()
			fmt.Printf("%s (%s, %s) built %s, %s %s\n", b.Version, b.Branch, b.Commit, b.BuildTime, b.GoVersion, b.Platform)
			return
		case "healthcheck":
			os.Exit(healthcheck())
		case "reset-admin":
			os.Exit(resetAdmin())
		}
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bootstrap(ctx, cfg, st, log); err != nil {
		return err
	}

	version := buildinfo.Version
	tg := notify.NewTelegram(func() settings.Telegram { return st.Settings().Telegram }, log)
	// Delivery errors would otherwise only reach the container log.
	tg.OnFailure(func(typ string, err error) {
		e := store.Event{Level: "error", Type: "telegram_failed",
			Message: fmt.Sprintf("Telegram notification %q was not delivered: %s", typ, err),
			Meta:    map[string]any{"event": typ, "error": err.Error()}}
		if err := st.AddEvent(ctx, e); err != nil {
			log.Error("store event", "err", err)
		}
	})
	ch := checker.New(st, tg, log, version)
	updater := buildinfo.NewUpdater(cfg.UpdateCheck, log)
	srv := server.New(cfg, st, ch, updater, log, webui.FS())

	go tg.Run(ctx)
	go ch.Run(ctx)
	go updater.Run(ctx)

	httpSrv := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		b := buildinfo.Get()
		log.Info("remnanode-manager started", "version", b.Version, "commit", b.Commit, "branch", b.Branch, "addr", httpSrv.Addr, "data", cfg.DataDir)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

// bootstrap applies optional environment overrides on start.
func bootstrap(ctx context.Context, cfg config.Config, st *store.Store, log *slog.Logger) error {
	if cfg.AdminUsername != "" && cfg.AdminPassword != "" {
		hash, err := server.HashPassword(cfg.AdminPassword)
		if err != nil {
			return err
		}
		if admin, err := st.AdminByUsername(ctx, cfg.AdminUsername); err == nil {
			if err := st.UpdateAdmin(ctx, admin.ID, admin.Username, hash); err != nil {
				return err
			}
			log.Info("admin password set from environment", "username", cfg.AdminUsername)
		} else if errors.Is(err, store.ErrNotFound) {
			if _, err := st.CreateAdmin(ctx, cfg.AdminUsername, hash); err != nil {
				return err
			}
			log.Info("admin created from environment", "username", cfg.AdminUsername)
		} else {
			return err
		}
	}

	s := st.Settings()
	changed := false
	if s.Panel.URL == "" && cfg.PanelURL != "" {
		s.Panel.URL = cfg.PanelURL
		changed = true
	}
	if s.Panel.Token == "" && cfg.PanelToken != "" {
		s.Panel.Token = cfg.PanelToken
		changed = true
	}
	if changed {
		if err := st.SaveSettings(ctx, s); err != nil {
			return fmt.Errorf("apply panel settings from environment: %w", err)
		}
		log.Info("panel connection seeded from environment")
	}
	return nil
}

func healthcheck() int {
	cfg := config.Load()
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port)) + "/api/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// resetAdmin removes all admin accounts so the setup screen appears again.
// Usage: docker exec -it remnanode-manager remnanode-manager reset-admin
func resetAdmin() int {
	cfg := config.Load()
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer st.Close()
	if err := st.DeleteAdmins(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("All admin accounts removed. Open the web UI to create a new one.")
	return 0
}
