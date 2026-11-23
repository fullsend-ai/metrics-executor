package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/fullsend-ai/metrics-executor/internal/cache"
	"github.com/fullsend-ai/metrics-executor/internal/config"
	"github.com/fullsend-ai/metrics-executor/internal/engine"
	"github.com/fullsend-ai/metrics-executor/internal/logging"
	"github.com/fullsend-ai/metrics-executor/internal/server"
	"github.com/fullsend-ai/metrics-executor/internal/store"
)

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	// Workflows can run several sequential query stages plus two transforms,
	// so the write timeout is deliberately generous.
	writeTimeout = 5 * time.Minute
	idleTimeout  = 120 * time.Second
	// Shorter than the default Kubernetes terminationGracePeriodSeconds (30s).
	shutdownTimeout = 25 * time.Second
)

func main() {
	cfg := config.Load()

	logger := logging.Setup(cfg.LogLevel, cfg.LogFormat, os.Stdout)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

// run returns instead of calling os.Exit so that deferred cleanup (cache
// refresher, database pools) always executes.
func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	var (
		wfStore  server.WorkflowStore
		wfEngine server.WorkflowEngine
		pingers  []server.Pinger
	)

	if cfg.HasDB() {
		dashDSN, err := cfg.DashboardDSN()
		if err != nil {
			return fmt.Errorf("dashboard DSN: %w", err)
		}
		dashDB, err := openDB(dashDSN)
		if err != nil {
			return fmt.Errorf("dashboard database: %w", err)
		}
		defer dashDB.Close()

		devlakeDB := dashDB
		devlakeDSN, err := cfg.DevlakeDSN()
		if err != nil {
			return fmt.Errorf("devlake DSN: %w", err)
		}
		if devlakeDSN != dashDSN {
			devlakeDB, err = openDB(devlakeDSN)
			if err != nil {
				return fmt.Errorf("devlake database: %w", err)
			}
			defer devlakeDB.Close()
		}

		// Log database connections without exposing passwords
		dashHost := cfg.DashboardDBUser + "@" + cfg.DashboardDBHost + ":" + cfg.DashboardDBPort + "/" + cfg.DashboardDBName
		devlakeHost := cfg.DevlakeDBUser + "@" + cfg.DevlakeDBHost + ":" + cfg.DevlakeDBPort + "/" + cfg.DevlakeDBName
		logger.Info("database connections established", "dashboard", dashHost, "devlake", devlakeHost)

		wfCache := cache.New(store.NewMySQLStore(dashDB))
		wfCache.Start(cfg.RefreshInterval)
		defer wfCache.Stop()
		logger.Info("cache refresh interval", "interval", cfg.RefreshInterval)

		wfStore = wfCache
		wfEngine = engine.New(engine.NewMySQLRunner(devlakeDB), engine.WithScriptTimeout(cfg.ScriptTimeout))
		logger.Info("script timeout", "timeout", cfg.ScriptTimeout)

		if devlakeDB == dashDB {
			pingers = []server.Pinger{dashDB}
		} else {
			pingers = []server.Pinger{dashDB, devlakeDB}
		}
	}

	srv := server.NewWithOptions(server.Options{
		RequestTimeout:          cfg.RequestTimeout,
		MaxConcurrentExecutions: cfg.MaxConcurrentExecutions,
		AllowedOrigins:          cfg.AllowedOrigins,
		LogClientIP:             cfg.LogClientIP,
	}, wfStore, wfEngine, pingers...)
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", "port", cfg.Port)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutdown signal received, draining connections", "timeout", shutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server error: %w", err)
	}
	logger.Info("server stopped")
	return nil
}

func openDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return db, nil
}
