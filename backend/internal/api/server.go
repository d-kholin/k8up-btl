package api

import (
        "context"
        "encoding/json"
        "fmt"
        "io"
        "log/slog"
        "net/http"
        "path"
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
        "github.com/d-kholin/k8up-gui/internal/restore"
        "github.com/d-kholin/k8up-gui/internal/resticcmd"
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
                Restic:  &resticcmd.Runner{Binary: cfg.ResticBinary},
                Log:     log,
                sseSubs: map[chan []byte]struct{}{},
        }
        orch.OnUpdate = func(st restore.State) {
                b, _ := json.Marshal(map[string]any{"type": "restore", "data": st})
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
        mux.HandleFunc("POST /api/v1/restores/{id}/resume-argo", s.handleResumeArgoByRestore)
        mux.HandleFunc("POST /api/v1/argo/{namespace}/{name}/resume", s.handleResumeArgo)
        mux.HandleFunc("GET /api/v1/interrupted", s.handleInterrupted)
        mux.HandleFunc("GET /api/v1/snapshots/{namespace}/{name}/files", s.handleListFiles)
        mux.HandleFunc("GET /api/v1/snapshots/{namespace}/{name}/download", s.handleDownload)
        mux.HandleFunc("POST /api/v1/backups", s.handleCreateBackup)
        mux.HandleFunc("POST /api/v1/checks", s.handleCreateCheck)
        mux.HandleFunc("GET /api/v1/audit", s.handleAudit)
        mux.HandleFunc("GET /api/v1/meta", s.handleMeta)
        mux.HandleFunc("GET /api/v1/events", s.handleSSE)

        var h http.Handler = mux
        h = auth.Middleware(s.Cfg)(h)
        h = s.withCORS(h)

        if s.Cfg.StaticDir != "" {
                fs := http.FileServer(http.Dir(s.Cfg.StaticDir))
                h = s.withSPA(h, fs)
        }
        return h
}

func (s *Server) withCORS(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                origin := r.Header.Get("Origin")
                if origin != "" {
                        w.Header().Set("Access-Control-Allow-Origin", origin)
                        w.Header().Set("Vary", "Origin")
                }
                w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, "+s.Cfg.AuthUserHeader+", "+s.Cfg.AuthEmailHeader)
                w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
                if r.Method == http.MethodOptions {
                        w.WriteHeader(http.StatusNoContent)
                        return
                }
                next.ServeHTTP(w, r)
        })
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
        type named struct {
                name string
                gvr  schema.GroupVersionResource
        }
        out := map[string]any{}
        for _, n := range []named{
                {"backups", k8s.GVRBackup},
                {"restores", k8s.GVRRestore},
                {"checks", k8s.GVRCheck},
                {"prunes", k8s.GVRPrune},
        } {
                list, err := s.K8s.ListResource(ctx, n.gvr, "")
                if err != nil {
                        out[n.name] = map[string]any{"error": err.Error()}
                        continue
                }
                out[n.name] = summarizeList(list.Items)
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
        id := r.PathValue("id")
        st, ok := s.Orch.Get(id)
        if !ok || st.Application == nil {
                http.Error(w, "restore or argo app not found", http.StatusNotFound)
                return
        }
        if err := s.Orch.ManualResumeArgo(r.Context(), st.Application.Namespace, st.Application.Name); err != nil {
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
        if p == "" || p == "/" {
                http.Error(w, "path query required", http.StatusBadRequest)
                return
        }
        repo, snapID, err := s.repoEnvForSnapshot(r.Context(), ns, name)
        if err != nil {
                s.writeErr(w, err, http.StatusBadRequest)
                return
        }
        base := path.Base(p)
        w.Header().Set("Content-Type", "application/octet-stream")
        w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, base))
        cw := &countingWriter{w: w}
        if err := s.Restic.Dump(r.Context(), repo, snapID, p, cw); err != nil {
                s.Log.Error("dump failed", "err", err)
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
                })
        }
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
