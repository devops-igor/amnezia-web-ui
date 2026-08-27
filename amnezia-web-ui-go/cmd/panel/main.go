package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/router"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("Application terminated with error", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	// 1. Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// 2. Setup structured logging
	var level slog.Level
	switch cfg.LogLevel {
	case "DEBUG":
		level = slog.LevelDebug
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	slog.Info("Starting Amnezia Web Panel", "version", cfg.AppVersion, "port", cfg.Port)

	// 3. Initialize SQLite database & translations
	_ = config.LoadTranslations()

	db, err := database.New(cfg.DBPath, cfg.SecretKey)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Error closing database", "err", closeErr)
		}
	}()

	// 4. Background supervisor
	sup := service.NewSupervisor()
	supErrCh := make(chan error, 1)
	go func() {
		supErrCh <- sup.Start(ctx)
	}()

	// 5. Initialize HTTP Router and Server
	r := router.NewRouter(cfg, db)
	srv := router.NewServer(cfg, r, db)

	serverErrCh := make(chan error, 1)
	go func() {
		slog.Info("Listening for HTTP connections", "host", cfg.Host, "port", cfg.Port)
		if err := srv.Start(); err != nil {
			serverErrCh <- err
		}
	}()

	// 6. Block until termination signal or server error
	select {
	case <-ctx.Done():
		slog.Info("Shutdown signal received, draining active connections...")
	case err := <-serverErrCh:
		return fmt.Errorf("HTTP server error: %w", err)
	case err := <-supErrCh:
		if err != nil && err != context.Canceled {
			return fmt.Errorf("background supervisor error: %w", err)
		}
	}

	// 7. Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("HTTP server shutdown encountered error", "err", err)
	}

	if err := sup.Stop(shutdownCtx); err != nil {
		slog.Warn("Supervisor stop encountered error", "err", err)
	}

	slog.Info("Amnezia Web Panel stopped cleanly")
	return nil
}
