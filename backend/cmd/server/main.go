package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/d-kholin/k8up-gui/internal/api"
	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/config"
	"github.com/d-kholin/k8up-gui/internal/history"
	"github.com/d-kholin/k8up-gui/internal/k8s"
	"github.com/d-kholin/k8up-gui/internal/notify"
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

	// Notifications: env (git) config is the baseline; UI overrides persisted
	// in SQLite win field-by-field. The manager always exists so the Settings
	// page can enable channels at runtime.
	overrides, err := api.LoadNotifyOverrides(context.Background(), store)
	if err != nil {
		log.Warn("notify overrides unreadable; using env config only", "err", err)
	}
	notifier := notify.NewManager(log, notify.Build(cfg.WithNotifyOverrides(overrides))...)
	if notifier.Enabled() {
		log.Info("notifications enabled", "channels", notifier.ChannelNames())
	}

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
			if e, ok := notify.InterruptedRestoresEvent(interrupted); ok {
				notifier.Go(e)
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
			hn, err := store.PruneBackupEventsOlderThan(context.Background(), cfg.HistoryRetention)
			if err != nil {
				log.Error("backup history prune", "err", err)
				continue
			}
			if hn > 0 {
				log.Info("pruned backup history events", "count", hn)
			}
		}
	}()

	srv := api.NewServer(cfg, clients, orch, store, log)
	srv.Notify = notifier
	srv.StartClusterMonitor(context.Background())

	// Alert on GUI-orchestrated restores reaching a terminal step. Chained onto
	// the SSE publisher NewServer installed; dedupe because publish can re-emit
	// a terminal state (e.g. manual Argo resume updates a finished restore).
	// Installed unconditionally — notifier.Go no-ops while no channel is
	// configured, and the Settings page can enable channels at runtime.
	{
		notifiedRestores := map[string]bool{}
		var notifiedMu sync.Mutex
		prevUpdate := orch.OnUpdate
		orch.OnUpdate = func(st restore.State) {
			if prevUpdate != nil {
				prevUpdate(st)
			}
			e, terminal := notify.RestoreOutcomeEvent(st)
			if !terminal || (e.Severity == notify.SeveritySuccess && !cfg.NotifyRestoreSuccess) {
				return
			}
			notifiedMu.Lock()
			seen := notifiedRestores[st.RestoreID]
			notifiedRestores[st.RestoreID] = true
			notifiedMu.Unlock()
			if !seen {
				notifier.Go(e)
			}
		}
	}

	// Durable backup run history: sweep K8up job CRs into SQLite so outcomes
	// survive K8up's keepJobs garbage collection; push changes to SSE clients.
	if clients != nil {
		rec := &history.Recorder{
			Clients:  clients,
			Store:    store,
			Log:      log,
			Interval: cfg.HistoryPollInterval,
			OnEvent:  srv.BroadcastBackupEvent,
		}
		rec.OnTransition = func(e audit.BackupEvent) {
			// GUI-orchestrated Restore CR failures already alert via the
			// orchestrator hook above with richer context; still alert here
			// for Restore CRs created outside the GUI.
			if e.Status == "failed" {
				if e.Kind == "Restore" && orchestratorOwnsRestore(orch, e) {
					return
				}
				notifier.Go(notify.JobFailureEvent(e))
			}
		}
		go rec.Run(context.Background())
	}

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

// orchestratorOwnsRestore reports whether a failed Restore CR belongs to a
// GUI-orchestrated restore (which already sends its own richer notification).
func orchestratorOwnsRestore(orch *restore.Orchestrator, e audit.BackupEvent) bool {
	for _, st := range orch.List() {
		if st.RestoreCRName == e.Name && st.PVCNamespace == e.Namespace {
			return true
		}
	}
	return false
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
