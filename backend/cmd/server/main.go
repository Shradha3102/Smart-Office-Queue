// Command server runs the Smart Office Queue & Token Management API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata" // embed tz database so APP_TIMEZONE works in minimal containers

	"smartqueue/internal/auth"
	"smartqueue/internal/config"
	"smartqueue/internal/db"
	"smartqueue/internal/httpapi"
	"smartqueue/internal/queue"
	"smartqueue/internal/realtime"
	"smartqueue/internal/seed"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	if err := seed.Run(ctx, pool, seed.Options{
		AdminEmail: cfg.AdminEmail, AdminPassword: cfg.AdminPassword,
		DemoStaff: cfg.SeedDemoStaff, StaffPassword: cfg.StaffPassword,
	}); err != nil {
		return err
	}

	allow := httpapi.OriginMatcher(cfg.CORSOrigins)
	hub := realtime.NewHub(allow)
	authSvc := auth.NewService(pool, cfg.JWTSecret, cfg.JWTTTL)
	queueSvc := queue.NewService(pool, cfg.Location, hub)

	// Housekeeping: tokens from previous business days are expired.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			if _, err := queueSvc.ExpireStale(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("expire stale tokens", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.NewRouter(pool, authSvc, queueSvc, hub, httpapi.Options{
			AllowOrigin: allow, TrustProxy: cfg.TrustProxy,
			TokenRatePerMin: cfg.TokenRatePerMin, LoginRatePerMin: cfg.LoginRatePerMin,
			WebDir: cfg.WebDir,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server listening", "addr", srv.Addr, "timezone", cfg.Location.String())
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}
	hub.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
