package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/d-kholin/k8up-gui/internal/api"
	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/config"
	"github.com/d-kholin/k8up-gui/internal/k8s"
	"github.com/d-kholin/k8up-gui/internal/restore"
)

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	slog.SetDefault(log)

	store, err := audit.Open(cfg.AuditDBPath)
	if err != nil {
		log.Error("audit db", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	var clients *k8s.Clients
	clients, err = k8s.New(cfg.Kubeconfig)
	if err != nil {
		log.Warn("kubernetes client unavailable; running with limited API", "err", err)
		clients = nil
	}

	// Orchestrator tolerates nil clients for local UI/API smoke without kubeconfig.
	orch := restore.NewOrchestrator(clients, cfg.ArgoCDNamespace, cfg.ScaleDownTimeout, cfg.RestoreTimeout, store, log)
	// Hydrate completed/historical restore jobs from SQLite on the data PVC.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := orch.LoadPersisted(ctx); err != nil {
			log.Warn("load persisted restores", "err", err)
		} else {
			log.Info("loaded persisted restore jobs", "count", len(orch.List()))
		}
		cancel()
	}
	if clients != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		interrupted, err := orch.ScanInterrupted(ctx)
		cancel()
		if err != nil {
			log.Warn("interrupted restore scan failed", "err", err)
		} else if len(interrupted) > 0 {
			log.Warn("found interrupted restores with Argo still paused", "count", len(interrupted))
			for _, st := range interrupted {
				app := ""
				if st.Application != nil {
					app = st.Application.Namespace + "/" + st.Application.Name
				}
				log.Warn("interrupted restore", "restoreId", st.RestoreID, "app", app, "step", st.Step)
			}
		}
	}

	// periodic audit prune
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for range t.C {
			n, err := store.PruneOlderThan(context.Background(), cfg.AuditRetention)
			if err != nil {
				log.Error("audit prune", "err", err)
				continue
			}
			if n > 0 {
				log.Info("pruned audit entries", "count", n)
			}
		}
	}()

	srv := api.NewServer(cfg, clients, orch, store, log)
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr, "devAuth", cfg.DevMode())
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server", "err", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
