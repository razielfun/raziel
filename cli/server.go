package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/raziel-ai/raziel/internal/api"
	"github.com/raziel-ai/raziel/internal/config"
	"github.com/raziel-ai/raziel/internal/db"
	"github.com/raziel-ai/raziel/internal/provider"
	"github.com/raziel-ai/raziel/internal/provider/fly"
	"github.com/raziel-ai/raziel/internal/queue"
	"github.com/raziel-ai/raziel/internal/sandbox"
	"github.com/raziel-ai/raziel/internal/storage"
	"github.com/raziel-ai/raziel/internal/worker"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the Raziel API server and background worker",
	RunE:  runServer,
}

func runServer(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	log, _ := zap.NewProduction()
	if cfg.Debug {
		log, _ = zap.NewDevelopment()
	}
	defer log.Sync() //nolint:errcheck

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer database.Close()

	store, err := storage.NewLocalStore(cfg.StoragePath)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	q := queue.NewMemoryQueue(512)

	registry := provider.NewRegistry()
	if cfg.FlyAPIToken != "" {
		registry.Register(fly.New(cfg.FlyAPIToken, cfg.FlyOrg))
		log.Info("fly provider registered")
	} else {
		log.Warn("FLY_API_TOKEN not set — no cloud provider available")
	}

	sbxStore, err := sandbox.DefaultStore()
	if err != nil {
		return fmt.Errorf("sandbox store: %w", err)
	}
	sbxProvider := sandbox.NewProvider(sbxStore)

	server := api.New(cfg, database, store, q, registry, sbxProvider, log)
	w := worker.New(q, database, store, registry, log, cfg.WorkerConcurrency)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start worker
	go w.Start(ctx)
	log.Info("worker started", zap.Int("concurrency", cfg.WorkerConcurrency))

	// Start HTTP server
	httpServer := &http.Server{
		Addr:         server.Addr(),
		Handler:      server,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutCancel()
		httpServer.Shutdown(shutCtx) //nolint:errcheck
	}()

	log.Info("server starting", zap.String("addr", server.Addr()))
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	log.Info("server stopped")
	return nil
}
