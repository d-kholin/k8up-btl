package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ClusterStatus reports whether the backend can actually reach the Kubernetes
// API. Without this, degraded mode (nil clients / dead connection) looks
// identical to "no snapshots" in every list endpoint.
type ClusterStatus struct {
	Connected bool      `json:"connected"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
}

const clusterCheckInterval = 30 * time.Second

// StartClusterMonitor probes the Kubernetes API once immediately and then on
// an interval, caching the result for /healthz, /readyz, /metrics and /meta.
func (s *Server) StartClusterMonitor(ctx context.Context) {
	s.checkCluster(ctx)
	go func() {
		t := time.NewTicker(clusterCheckInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.checkCluster(ctx)
			}
		}
	}()
}

func (s *Server) checkCluster(ctx context.Context) {
	var errStr string
	ok := false
	if s.K8s == nil {
		errStr = "kubernetes client not configured (no kubeconfig / in-cluster config)"
	} else {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		// /version is readable by every authenticated principal and cheap.
		err := s.K8s.Typed.Discovery().RESTClient().Get().AbsPath("/version").Do(cctx).Error()
		cancel()
		if err != nil {
			errStr = err.Error()
		} else {
			ok = true
		}
	}
	s.clusterMu.Lock()
	changed := s.clusterOK != ok
	s.clusterOK = ok
	s.clusterErr = errStr
	s.clusterAt = time.Now().UTC()
	s.clusterMu.Unlock()
	if changed {
		if ok {
			s.Log.Info("kubernetes API reachable")
		} else {
			s.Log.Warn("kubernetes API unreachable — running degraded", "err", errStr)
		}
	}
}

func (s *Server) clusterStatus() ClusterStatus {
	s.clusterMu.RLock()
	defer s.clusterMu.RUnlock()
	if s.clusterAt.IsZero() {
		// Monitor not started (tests) — report the static client presence.
		return ClusterStatus{Connected: s.K8s != nil}
	}
	return ClusterStatus{Connected: s.clusterOK, Error: s.clusterErr, CheckedAt: s.clusterAt}
}

// handleMetrics exposes app-level metrics in Prometheus text format. Kept
// dependency-free on purpose — the metric set is small and stable.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder

	cl := s.clusterStatus()
	writeMetric(&b, "k8upbtl_cluster_connected", "Whether the Kubernetes API is reachable (1) or not (0).", "gauge")
	fmt.Fprintf(&b, "k8upbtl_cluster_connected %d\n", boolToInt(cl.Connected))

	active := 0
	for _, st := range s.Orch.List() {
		if st.Step != "done" && st.Step != "failed" {
			active++
		}
	}
	writeMetric(&b, "k8upbtl_restore_active", "Number of GUI restores currently in flight.", "gauge")
	fmt.Fprintf(&b, "k8upbtl_restore_active %d\n", active)

	writeMetric(&b, "k8upbtl_notify_channels", "Number of configured notification channels.", "gauge")
	fmt.Fprintf(&b, "k8upbtl_notify_channels %d\n", len(s.Notify.ChannelNames()))

	if s.Audit != nil {
		if sum, err := s.Audit.Summary(r.Context()); err == nil {
			writeMetric(&b, "k8upbtl_restores_total", "GUI-orchestrated restores recorded in the audit log, by status.", "counter")
			fmt.Fprintf(&b, "k8upbtl_restores_total{status=\"success\"} %d\n", sum.RestoreOK)
			fmt.Fprintf(&b, "k8upbtl_restores_total{status=\"failed\"} %d\n", sum.RestoreFail)
			writeMetric(&b, "k8upbtl_download_bytes_total", "Bytes served through snapshot browse/download.", "counter")
			fmt.Fprintf(&b, "k8upbtl_download_bytes_total %d\n", sum.DownloadBytes)
		}
		if counts, err := s.Audit.CountBackupEvents(r.Context()); err == nil {
			writeMetric(&b, "k8upbtl_backup_events_total", "Durable K8up job outcomes recorded by the history sweeper.", "counter")
			keys := make([]string, 0, len(counts))
			for k := range counts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				kind, status, ok := strings.Cut(k, "|")
				if !ok {
					continue
				}
				fmt.Fprintf(&b, "k8upbtl_backup_events_total{kind=%q,status=%q} %d\n", kind, status, counts[k])
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func writeMetric(b *strings.Builder, name, help, typ string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
