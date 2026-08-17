package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/auth"
	"github.com/d-kholin/k8up-gui/internal/config"
	"github.com/d-kholin/k8up-gui/internal/k8s"
	"github.com/d-kholin/k8up-gui/internal/resticcmd"
	"github.com/d-kholin/k8up-gui/internal/restore"
)

type Server struct {
	Cfg    config.Config
	K8s    *k8s.Clients
	Orch   *restore.Orchestrator
	Audit  *audit.Store
	Restic *resticcmd.Runner
	Log    *slog.Logger

	sseMu   sync.Mutex
	sseSubs map[chan []byte]struct{}
}

func NewServer(cfg config.Config, clients *k8s.Clients, orch *restore.Orchestrator, store *audit.Store, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		Cfg:     cfg,
		K8s:     clients,
		Orch:    orch,
		Audit:   store,
		Restic:  &resticcmd.Runner{Binary: cfg.ResticBinary, CacheDir: cfg.ResticCacheDir},
		Log:     log,
		sseSubs: map[chan []byte]struct{}{},
	}
	orch.OnUpdate = func(st restore.State) {
		b, _ := json.Marshal(map[string]any{"type": "restore", "data": st})
		s.broadcast(b)
	}
	orch.OnLog = func(restoreID, line string) {
		b, _ := json.Marshal(map[string]any{
			"type":      "restore-log",
			"restoreId": restoreID,
			"line":      line,
			"ts":        time.Now().UTC().Format(time.RFC3339Nano),
		})
		s.broadcast(b)
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/v1/me", s.handleMe)
	mux.HandleFunc("GET /api/v1/schedules", s.handleListSchedules)
	mux.HandleFunc("GET /api/v1/snapshots", s.handleListSnapshots)
	mux.HandleFunc("GET /api/v1/jobs", s.handleListJobs)
	mux.HandleFunc("GET /api/v1/restores", s.handleListRestores)
	mux.HandleFunc("POST /api/v1/restores", s.handleStartRestore)
	mux.HandleFunc("GET /api/v1/restores/{id}", s.handleGetRestore)
	mux.HandleFunc("GET /api/v1/restores/{id}/logs", s.handleRestoreLogs)
	mux.HandleFunc("POST /api/v1/restores/{id}/resume-argo", s.handleResumeArgoByRestore)
	mux.HandleFunc("POST /api/v1/argo/{namespace}/{name}/resume", s.handleResumeArgo)
	mux.HandleFunc("GET /api/v1/interrupted", s.handleInterrupted)
	mux.HandleFunc("GET /api/v1/snapshots/{namespace}/{name}/files", s.handleListFiles)
	mux.HandleFunc("GET /api/v1/snapshots/{namespace}/{name}/download", s.handleDownload)
	mux.HandleFunc("POST /api/v1/backups", s.handleCreateBackup)
	mux.HandleFunc("POST /api/v1/checks", s.handleCreateCheck)
	mux.HandleFunc("GET /api/v1/audit", s.handleAudit)
	mux.HandleFunc("GET /api/v1/history/backups", s.handleBackupHistory)
	mux.HandleFunc("GET /api/v1/pvcs", s.handleListPVCs)
	mux.HandleFunc("GET /api/v1/meta", s.handleMeta)
	mux.HandleFunc("GET /api/v1/stats/storage", s.handleStorageStats)
	mux.HandleFunc("GET /api/v1/events", s.handleSSE)

	// No CORS middleware on purpose: the SPA is served same-origin by this
	// backend (Vite proxies /api in dev), and the API is otherwise
	// unauthenticated — reflecting Origin would let any website drive it.
	var h http.Handler = mux
	h = auth.Middleware(s.Cfg)(h)

	if s.Cfg.StaticDir != "" {
		fs := http.FileServer(http.Dir(s.Cfg.StaticDir))
		h = s.withSPA(h, fs)
	}
	return h
}

func (s *Server) withSPA(api http.Handler, fs http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			api.ServeHTTP(w, r)
			return
		}
		if path.Ext(r.URL.Path) == "" {
			http.ServeFile(w, r, path.Join(s.Cfg.StaticDir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleListPVCs(w http.ResponseWriter, r *http.Request) {
	if s.K8s == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	refs, err := s.K8s.ListPVCRefs(r.Context())
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	if refs == nil {
		refs = []k8s.PVCRef{}
	}
	writeJSON(w, http.StatusOK, refs)
}

// BroadcastBackupEvent pushes a recorded job outcome to SSE subscribers so the
// dashboard activity view updates live.
func (s *Server) BroadcastBackupEvent(e audit.BackupEvent) {
	b, _ := json.Marshal(map[string]any{"type": "backup-event", "data": e})
	s.broadcast(b)
}

// handleBackupHistory serves the durable K8up job run history recorded by the
// history sweeper (survives K8up's keepJobs CR cleanup).
func (s *Server) handleBackupHistory(w http.ResponseWriter, r *http.Request) {
	days := 366
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 730 {
			days = n
		}
	}
	kind := r.URL.Query().Get("kind")
	since := time.Now().UTC().AddDate(0, 0, -days)
	events, err := s.Audit.ListBackupEvents(r.Context(), since, kind)
	if err != nil {
		s.writeErr(w, err, http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []audit.BackupEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"since": since, "events": events})
}

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"grafanaDashboardUrl":  s.Cfg.GrafanaDashboardURL,
		"prometheusConfigured": s.Cfg.PrometheusURL != "",
		"argocdNamespace":      s.Cfg.ArgoCDNamespace,
	})
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	if s.K8s == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	list, err := s.K8s.ListResource(r.Context(), k8s.GVRSchedule, "")
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, summarizeList(list.Items))
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	if s.K8s == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	ns := r.URL.Query().Get("namespace")
	list, err := s.K8s.ListResource(r.Context(), k8s.GVRSnapshot, ns)
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, summarizeList(list.Items))
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if s.K8s == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	ctx := r.Context()
	kinds := []struct {
		name string
		gvr  schema.GroupVersionResource
	}{
		{"backups", k8s.GVRBackup},
		{"restores", k8s.GVRRestore},
		{"checks", k8s.GVRCheck},
		{"prunes", k8s.GVRPrune},
	}
	// The four cluster-wide LISTs are independent; run them concurrently so
	// response latency is the slowest list, not the sum of all four.
	results := make([]any, len(kinds))
	var wg sync.WaitGroup
	for i, n := range kinds {
		wg.Add(1)
		go func(i int, gvr schema.GroupVersionResource) {
			defer wg.Done()
			list, err := s.K8s.ListResource(ctx, gvr, "")
			if err != nil {
				results[i] = map[string]any{"error": err.Error()}
				return
			}
			results[i] = summarizeList(list.Items)
		}(i, n.gvr)
	}
	wg.Wait()
	out := map[string]any{}
	for i, n := range kinds {
		out[n.name] = results[i]
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListRestores(w http.ResponseWriter, _ *http.Request) {
	out := s.Orch.List()
	if out == nil {
		out = []restore.State{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, ok := s.Orch.Get(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleRestoreLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.Orch.Get(id); !ok {
		// still return buffer if any
	}
	lines := []string{}
	if s.Orch.Logs != nil {
		lines = s.Orch.Logs.Snapshot(id)
	}
	// Fall back to durable logs on /data after pod restart.
	if len(lines) == 0 && s.Audit != nil {
		if persisted, err := s.Audit.GetRestoreLogs(r.Context(), id); err == nil && len(persisted) > 0 {
			lines = persisted
			if s.Orch.Logs != nil {
				s.Orch.Logs.Seed(id, persisted)
			}
		}
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"restoreId": id,
		"lines":     lines,
	})
}

func (s *Server) handleStartRestore(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	var req restore.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	req.Actor = u.Username
	if s.K8s == nil {
		http.Error(w, "kubernetes client not configured", http.StatusServiceUnavailable)
		return
	}
	st, err := s.Orch.Start(r.Context(), req)
	if err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

func (s *Server) handleResumeArgo(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	if err := s.Orch.ManualResumeArgo(r.Context(), ns, name); err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

func (s *Server) handleResumeArgoByRestore(w http.ResponseWriter, r *http.Request) {
	// id is optional context; global controller resume does not need Application
	if err := s.Orch.ManualResumeArgo(r.Context(), s.Cfg.ArgoCDNamespace, "application-controller"); err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

func (s *Server) handleInterrupted(w http.ResponseWriter, r *http.Request) {
	if s.K8s == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	list, err := s.Orch.ScanInterrupted(r.Context())
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	if list == nil {
		list = []restore.State{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/"
	}
	repo, snapID, err := s.repoEnvForSnapshot(r.Context(), ns, name)
	if err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}
	nodes, err := s.Restic.List(r.Context(), repo, snapID, p)
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/"
	}
	p = resticcmd.NormalizeBrowsePath(p)
	archive := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("archive")))
	// folder=1 or archive=zip|tar → directory / full-snapshot archive dump
	if r.URL.Query().Get("folder") == "1" && archive == "" {
		archive = "zip"
	}
	switch archive {
	case "", "zip", "tar":
	default:
		http.Error(w, `archive must be zip or tar`, http.StatusBadRequest)
		return
	}
	// Whole snapshot always needs an archive format.
	if p == "/" && archive == "" {
		archive = "zip"
	}

	repo, snapID, err := s.repoEnvForSnapshot(r.Context(), ns, name)
	if err != nil {
		s.writeErr(w, err, http.StatusBadRequest)
		return
	}

	filename, contentType := downloadFilename(ns, name, snapID, p, archive)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	// Hint proxies not to buffer entire archive.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	cw := &countingWriter{w: w}
	if err := s.Restic.Dump(r.Context(), repo, snapID, p, cw, resticcmd.DumpOptions{Archive: archive}); err != nil {
		s.Log.Error("dump failed", "err", err, "path", p, "archive", archive)
		// Headers may already be flushed; best-effort error surface.
		if cw.n == 0 {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		if s.Audit != nil {
			_, _ = s.Audit.Insert(r.Context(), audit.Entry{
				Kind:      "download",
				Actor:     u.Username,
				Namespace: ns,
				Snapshot:  snapID,
				Path:      p,
				Status:    "error",
				Detail:    err.Error(),
			})
		}
		return
	}
	if s.Audit != nil {
		_, _ = s.Audit.Insert(r.Context(), audit.Entry{
			Kind:      "download",
			Actor:     u.Username,
			Namespace: ns,
			Snapshot:  snapID,
			Path:      p,
			Status:    "success",
			Bytes:     cw.n,
			Detail:    archiveLabel(archive),
		})
	}
}

func archiveLabel(archive string) string {
	if archive == "" {
		return "file"
	}
	return "archive:" + archive
}

// downloadFilename builds a safe Content-Disposition filename.
func downloadFilename(ns, snapName, snapID, browsePath, archive string) (filename, contentType string) {
	contentType = "application/octet-stream"
	ext := ""
	switch archive {
	case "zip":
		ext = ".zip"
		contentType = "application/zip"
	case "tar":
		ext = ".tar"
		contentType = "application/x-tar"
	}

	var base string
	switch {
	case browsePath == "/" || browsePath == "":
		id := snapID
		if len(id) > 12 {
			id = id[:12]
		}
		if id == "" {
			id = snapName
		}
		base = fmt.Sprintf("%s-%s-snapshot", sanitizeFilename(ns), sanitizeFilename(id))
	default:
		base = sanitizeFilename(path.Base(browsePath))
		if base == "" || base == "." || base == "/" {
			base = "download"
		}
	}
	if ext != "" && !strings.HasSuffix(strings.ToLower(base), ext) {
		base += ext
	}
	return base, contentType
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "download"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '\\':
			b.WriteByte('-')
		default:
			// drop
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "download"
	}
	return out
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	s.createJob(w, r, "Backup", k8s.GVRBackup)
}

func (s *Server) handleCreateCheck(w http.ResponseWriter, r *http.Request) {
	s.createJob(w, r, "Check", k8s.GVRCheck)
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request, kind string, gvr schema.GroupVersionResource) {
	u, _ := auth.FromContext(r.Context())
	var body struct {
		Namespace string         `json:"namespace"`
		Name      string         `json:"name"`
		Spec      map[string]any `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Namespace == "" {
		http.Error(w, "namespace required", http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		body.Name = fmt.Sprintf("gui-%s-%s", strings.ToLower(kind), uuid.NewString()[:8])
	}
	if body.Spec == nil {
		body.Spec = map[string]any{}
	}
	if s.K8s == nil {
		http.Error(w, "kubernetes client not configured", http.StatusServiceUnavailable)
		return
	}
	obj, err := s.K8s.CreateSimpleJobCR(r.Context(), gvr, kind, body.Namespace, body.Name, body.Spec)
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	if s.Audit != nil {
		_, _ = s.Audit.Insert(r.Context(), audit.Entry{
			Kind:      strings.ToLower(kind),
			Actor:     u.Username,
			Namespace: body.Namespace,
			Status:    "created",
			Detail:    body.Name,
		})
	}
	writeJSON(w, http.StatusCreated, summarizeObj(obj))
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	entries, err := s.Audit.List(r.Context(), audit.ListFilter{Kind: kind, Limit: 200})
	if err != nil {
		s.writeErr(w, err, http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []audit.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "sse unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := make(chan []byte, 8)
	s.sseMu.Lock()
	s.sseSubs[ch] = struct{}{}
	s.sseMu.Unlock()
	defer func() {
		s.sseMu.Lock()
		delete(s.sseSubs, ch)
		s.sseMu.Unlock()
	}()
	_, _ = fmt.Fprintf(w, "event: ready\ndata: {}\n\n")
	flusher.Flush()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) broadcast(msg []byte) {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	for ch := range s.sseSubs {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *Server) repoEnvForSnapshot(ctx context.Context, ns, name string) (resticcmd.RepoEnv, string, error) {
	if s.K8s == nil {
		return resticcmd.RepoEnv{}, "", fmt.Errorf("kubernetes client not configured")
	}
	snap, err := s.K8s.GetSnapshot(ctx, ns, name)
	if err != nil {
		return resticcmd.RepoEnv{}, "", err
	}
	snapID, _, _ := nestedString(snap.Object, "spec", "id")
	if snapID == "" {
		snapID, _, _ = nestedString(snap.Object, "status", "id")
	}
	if snapID == "" {
		snapID = name
	}

	// K8up Snapshot CR on this cluster: spec.repository is a full restic URI
	// e.g. s3:https://garage.../naku-k8up/<ns>
	repoURL, _, _ := nestedString(snap.Object, "spec", "repository")
	if repoURL == "" {
		repoURL, _, _ = nestedString(snap.Object, "status", "repository")
	}

	env := []string{}

	// Prefer cluster-global K8up secret (homelab: k8up/k8up-global).
	if s.Cfg.K8upGlobalSecretName != "" {
		sec, err := s.K8s.GetSecret(ctx, s.Cfg.K8upGlobalSecretNS, s.Cfg.K8upGlobalSecretName)
		if err != nil {
			return resticcmd.RepoEnv{}, "", fmt.Errorf("k8up global secret %s/%s: %w", s.Cfg.K8upGlobalSecretNS, s.Cfg.K8upGlobalSecretName, err)
		}
		// Map K8up operator env keys → restic/AWS env.
		get := func(k string) string {
			if v, ok := sec.Data[k]; ok {
				return string(v)
			}
			return ""
		}
		if pw := get("BACKUP_GLOBALREPOPASSWORD"); pw != "" {
			env = append(env, "RESTIC_PASSWORD="+pw)
		}
		if ak := get("BACKUP_GLOBALACCESSKEYID"); ak != "" {
			env = append(env, "AWS_ACCESS_KEY_ID="+ak)
		}
		if sk := get("BACKUP_GLOBALSECRETACCESSKEY"); sk != "" {
			env = append(env, "AWS_SECRET_ACCESS_KEY="+sk)
		}
		// Endpoint is already embedded in snapshot repository URI for S3.
		_ = get("BACKUP_GLOBALS3ENDPOINT")
	}

	// Optional per-snapshot secret refs (other deployments).
	secretName, _, _ := nestedString(snap.Object, "spec", "backend", "repoPasswordSecretRef", "name")
	if secretName != "" {
		sec, err := s.K8s.GetSecret(ctx, ns, secretName)
		if err != nil {
			return resticcmd.RepoEnv{}, "", fmt.Errorf("repo secret: %w", err)
		}
		for k, v := range sec.Data {
			switch k {
			case "password", "RESTIC_PASSWORD", "BACKUP_GLOBALREPOPASSWORD":
				if !hasEnv(env, "RESTIC_PASSWORD") {
					env = append(env, "RESTIC_PASSWORD="+string(v))
				}
			case "AWS_ACCESS_KEY_ID", "BACKUP_GLOBALACCESSKEYID":
				if !hasEnv(env, "AWS_ACCESS_KEY_ID") {
					env = append(env, "AWS_ACCESS_KEY_ID="+string(v))
				}
			case "AWS_SECRET_ACCESS_KEY", "BACKUP_GLOBALSECRETACCESSKEY":
				if !hasEnv(env, "AWS_SECRET_ACCESS_KEY") {
					env = append(env, "AWS_SECRET_ACCESS_KEY="+string(v))
				}
			case "RESTIC_REPOSITORY":
				if repoURL == "" {
					repoURL = string(v)
				}
			default:
				// ignore unknown keys
			}
		}
	}

	if repoURL != "" {
		env = append(env, "RESTIC_REPOSITORY="+repoURL)
	}
	if !hasEnv(env, "RESTIC_REPOSITORY") || !hasEnv(env, "RESTIC_PASSWORD") {
		return resticcmd.RepoEnv{}, "", fmt.Errorf("could not resolve RESTIC_REPOSITORY/PASSWORD from snapshot %s/%s", ns, name)
	}
	return resticcmd.RepoEnv{Env: env}, snapID, nil
}

func hasEnv(env []string, key string) bool {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

func summarizeList(items []unstructured.Unstructured) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for i := range items {
		out = append(out, summarizeObj(&items[i]))
	}
	return out
}

func summarizeObj(obj *unstructured.Unstructured) map[string]any {
	if obj == nil {
		return nil
	}
	return map[string]any{
		"apiVersion":        obj.GetAPIVersion(),
		"kind":              obj.GetKind(),
		"namespace":         obj.GetNamespace(),
		"name":              obj.GetName(),
		"labels":            obj.GetLabels(),
		"creationTimestamp": obj.GetCreationTimestamp().Time,
		"spec":              obj.Object["spec"],
		"status":            obj.Object["status"],
	}
}

func nestedString(obj map[string]any, fields ...string) (string, bool, error) {
	var cur any = obj
	for _, f := range fields {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false, nil
		}
		cur, ok = m[f]
		if !ok {
			return "", false, nil
		}
	}
	v, ok := cur.(string)
	return v, ok, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) writeErr(w http.ResponseWriter, err error, code int) {
	s.Log.Error("request error", "err", err)
	http.Error(w, err.Error(), code)
}
