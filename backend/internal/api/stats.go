package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/d-kholin/k8up-gui/internal/k8s"
	"github.com/d-kholin/k8up-gui/internal/resticcmd"
)

type repoStats struct {
	Namespace     string  `json:"namespace"`
	Repository    string  `json:"repository"`
	LogicalBytes  int64   `json:"logicalBytes"` // restore-size (unique file content across snaps)
	StoredBytes   int64   `json:"storedBytes"`  // raw-data on backend
	SnapshotCount int     `json:"snapshotCount"`
	DedupRatio    float64 `json:"dedupRatio"` // 1 - stored/logical (0..1), higher = more savings
	Error         string  `json:"error,omitempty"`
}

type storageStatsResponse struct {
	CollectedAt   time.Time   `json:"collectedAt"`
	LogicalBytes  int64       `json:"logicalBytes"`
	StoredBytes   int64       `json:"storedBytes"`
	SavedBytes    int64       `json:"savedBytes"`
	DedupRatio    float64     `json:"dedupRatio"`
	SnapshotCount int         `json:"snapshotCount"`
	Repos         []repoStats `json:"repos"`
	Partial       bool        `json:"partial"`
	Stale         bool        `json:"stale"`     // served from cache while refresh fails
	Computing     bool        `json:"computing"` // background restic stats in progress
	CacheAgeSec   int         `json:"cacheAgeSec"`
	Error         string      `json:"error,omitempty"`
}

type statsCache struct {
	mu      sync.Mutex
	data    *storageStatsResponse
	at      time.Time
	running bool
	path    string // disk cache next to audit db
}

var globalStatsCache statsCache

const statsCacheTTL = 10 * time.Minute

func (s *Server) statsCachePath() string {
	dir := filepath.Dir(s.Cfg.AuditDBPath)
	return filepath.Join(dir, "storage-stats.json")
}

func (s *Server) handleStorageStats(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") == "1"
	globalStatsCache.ensurePath(s.statsCachePath())

	// Fast path: fresh memory cache
	if st, ok := globalStatsCache.get(refresh); ok {
		writeJSON(w, 200, st)
		return
	}

	// Load disk cache into memory if empty
	if disk, ok := globalStatsCache.loadDisk(); ok {
		age := time.Since(globalStatsCache.at)
		if !refresh && age < statsCacheTTL {
			out := *disk
			out.CacheAgeSec = int(age.Seconds())
			out.Stale = false
			out.Computing = false
			writeJSON(w, 200, &out)
			return
		}
		// Return stale disk data immediately and refresh in background
		out := *disk
		out.CacheAgeSec = int(age.Seconds())
		out.Stale = age >= statsCacheTTL || refresh
		started := globalStatsCache.startBackground(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()
			st, err := s.collectStorageStats(ctx)
			if err != nil {
				globalStatsCache.finishErr(err)
				if s.Log != nil {
					s.Log.Warn("storage stats refresh failed", "err", err)
				}
				return
			}
			globalStatsCache.set(st)
			_ = globalStatsCache.saveDisk(st)
		})
		out.Computing = started || globalStatsCache.isRunning()
		writeJSON(w, 200, &out)
		return
	}

	// No cache at all: kick off background compute and return computing shell
	// so the dashboard never blocks on multi-repo restic stats.
	empty := &storageStatsResponse{
		CollectedAt: time.Time{},
		Repos:       []repoStats{},
		Computing:   true,
	}
	started := globalStatsCache.startBackground(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		st, err := s.collectStorageStats(ctx)
		if err != nil {
			globalStatsCache.finishErr(err)
			if s.Log != nil {
				s.Log.Warn("storage stats collect failed", "err", err)
			}
			return
		}
		globalStatsCache.set(st)
		_ = globalStatsCache.saveDisk(st)
	})
	empty.Computing = started || globalStatsCache.isRunning()
	if !empty.Computing {
		// Extremely unlikely race: finished between start and check
		if st, ok := globalStatsCache.get(false); ok {
			writeJSON(w, 200, st)
			return
		}
	}
	writeJSON(w, 200, empty)
}

func (c *statsCache) ensurePath(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path == "" {
		c.path = p
	}
}

func (c *statsCache) isRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

func (c *statsCache) startBackground(fn func()) bool {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return false
	}
	c.running = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			c.running = false
			c.mu.Unlock()
		}()
		fn()
	}()
	return true
}

func (c *statsCache) finishErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil {
		c.data = &storageStatsResponse{
			Repos: []repoStats{},
			Error: err.Error(),
		}
		c.at = time.Now()
		return
	}
	// keep last good numbers, annotate error
	cp := *c.data
	cp.Error = err.Error()
	cp.Stale = true
	c.data = &cp
}

func (c *statsCache) get(force bool) (*storageStatsResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil {
		return nil, false
	}
	// Never treat pure-error shell with no collectedAt as fresh data
	if c.data.CollectedAt.IsZero() && c.data.LogicalBytes == 0 && c.data.StoredBytes == 0 && len(c.data.Repos) == 0 {
		if force {
			return nil, false
		}
		// still allow returning computing/error shell
		if c.running || c.data.Error != "" {
			out := *c.data
			out.Computing = c.running
			out.CacheAgeSec = int(time.Since(c.at).Seconds())
			return &out, true
		}
		return nil, false
	}
	age := time.Since(c.at)
	if !force && age < statsCacheTTL {
		out := *c.data
		out.CacheAgeSec = int(age.Seconds())
		out.Stale = false
		out.Computing = c.running
		return &out, true
	}
	return nil, false
}

func (c *statsCache) getStale() (*storageStatsResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil || c.data.CollectedAt.IsZero() {
		return nil, false
	}
	out := *c.data
	out.CacheAgeSec = int(time.Since(c.at).Seconds())
	out.Computing = c.running
	return &out, true
}

func (c *statsCache) set(st *storageStatsResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st != nil {
		st.Computing = false
		st.Error = ""
	}
	c.data = st
	c.at = time.Now()
}

func (c *statsCache) loadDisk() (*storageStatsResponse, bool) {
	c.mu.Lock()
	path := c.path
	// if memory already has real data, skip disk
	if c.data != nil && !c.data.CollectedAt.IsZero() {
		out := *c.data
		c.mu.Unlock()
		return &out, true
	}
	c.mu.Unlock()
	if path == "" {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var st storageStatsResponse
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, false
	}
	if st.CollectedAt.IsZero() {
		return nil, false
	}
	// Infer mtime if needed
	if fi, err := os.Stat(path); err == nil {
		c.mu.Lock()
		c.data = &st
		c.at = fi.ModTime()
		c.mu.Unlock()
	} else {
		c.mu.Lock()
		c.data = &st
		c.at = st.CollectedAt
		c.mu.Unlock()
	}
	out := st
	return &out, true
}

func (c *statsCache) saveDisk(st *storageStatsResponse) error {
	c.mu.Lock()
	path := c.path
	c.mu.Unlock()
	if path == "" || st == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Server) collectStorageStats(ctx context.Context) (*storageStatsResponse, error) {
	if s.K8s == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}
	// Unique namespaces from schedules (one repo per ns in this cluster).
	schedules, err := s.K8s.ListResource(ctx, k8s.GVRSchedule, "")
	if err != nil {
		return nil, err
	}
	type nsRepo struct {
		ns, repo string
		snaps    int
	}
	byNS := map[string]*nsRepo{}
	for i := range schedules.Items {
		item := &schedules.Items[i]
		ns := item.GetNamespace()
		bucket, _, _ := nestedString(item.Object, "spec", "backend", "s3", "bucket")
		if bucket == "" {
			continue
		}
		byNS[ns] = &nsRepo{ns: ns, repo: bucket}
	}
	// Snapshot counts
	snaps, err := s.K8s.ListResource(ctx, k8s.GVRSnapshot, "")
	if err == nil {
		for i := range snaps.Items {
			ns := snaps.Items[i].GetNamespace()
			if e, ok := byNS[ns]; ok {
				e.snaps++
			} else {
				// discover repo from snapshot
				repoURI, _, _ := nestedString(snaps.Items[i].Object, "spec", "repository")
				bucket := s3BucketFromRepoURI(repoURI)
				if bucket == "" {
					continue
				}
				byNS[ns] = &nsRepo{ns: ns, repo: bucket, snaps: 1}
			}
		}
	}

	// Global secret env once
	baseEnv, err := s.globalResticEnv(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := ""
	for _, e := range baseEnv {
		if strings.HasPrefix(e, "S3_ENDPOINT=") {
			endpoint = strings.TrimPrefix(e, "S3_ENDPOINT=")
		}
	}

	out := &storageStatsResponse{
		CollectedAt: time.Now().UTC(),
		Repos:       make([]repoStats, 0, len(byNS)),
	}

	for _, nr := range byNS {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rs := repoStats{
			Namespace:     nr.ns,
			Repository:    nr.repo,
			SnapshotCount: nr.snaps,
		}
		repoEnv, rerr := s.repoEnvForNamespace(ctx, nr.ns, nr.repo, endpoint, baseEnv)
		if rerr != nil {
			rs.Error = rerr.Error()
			out.Partial = true
			out.Repos = append(out.Repos, rs)
			continue
		}
		// restore-size = logical unique content; raw-data = bytes in repo
		logical, lerr := s.Restic.Stats(ctx, repoEnv, "", "restore-size")
		if lerr != nil {
			rs.Error = lerr.Error()
			out.Partial = true
			out.Repos = append(out.Repos, rs)
			continue
		}
		stored, serr := s.Restic.Stats(ctx, repoEnv, "", "raw-data")
		if serr != nil {
			rs.Error = serr.Error()
			out.Partial = true
			// still keep logical if we have it
		}
		rs.LogicalBytes = jsonInt64(logical, "total_size")
		if serr == nil {
			rs.StoredBytes = jsonInt64(stored, "total_size")
		}
		if rs.LogicalBytes > 0 && rs.StoredBytes > 0 {
			rs.DedupRatio = 1 - float64(rs.StoredBytes)/float64(rs.LogicalBytes)
			if rs.DedupRatio < 0 {
				rs.DedupRatio = 0
			}
		}
		out.LogicalBytes += rs.LogicalBytes
		out.StoredBytes += rs.StoredBytes
		out.SnapshotCount += rs.SnapshotCount
		out.Repos = append(out.Repos, rs)
	}

	out.SavedBytes = out.LogicalBytes - out.StoredBytes
	if out.SavedBytes < 0 {
		out.SavedBytes = 0
	}
	if out.LogicalBytes > 0 && out.StoredBytes > 0 {
		out.DedupRatio = 1 - float64(out.StoredBytes)/float64(out.LogicalBytes)
		if out.DedupRatio < 0 {
			out.DedupRatio = 0
		}
	}
	return out, nil
}

func (s *Server) globalResticEnv(ctx context.Context) ([]string, error) {
	sec, err := s.K8s.GetSecret(ctx, s.Cfg.K8upGlobalSecretNS, s.Cfg.K8upGlobalSecretName)
	if err != nil {
		return nil, fmt.Errorf("k8up global secret: %w", err)
	}
	get := func(k string) string {
		if v, ok := sec.Data[k]; ok {
			return string(v)
		}
		return ""
	}
	env := []string{}
	if pw := get("BACKUP_GLOBALREPOPASSWORD"); pw != "" {
		env = append(env, "RESTIC_PASSWORD="+pw)
	}
	if ak := get("BACKUP_GLOBALACCESSKEYID"); ak != "" {
		env = append(env, "AWS_ACCESS_KEY_ID="+ak)
	}
	if sk := get("BACKUP_GLOBALSECRETACCESSKEY"); sk != "" {
		env = append(env, "AWS_SECRET_ACCESS_KEY="+sk)
	}
	if ep := get("BACKUP_GLOBALS3ENDPOINT"); ep != "" {
		env = append(env, "S3_ENDPOINT="+strings.TrimRight(ep, "/"))
	}
	if !hasEnv(env, "RESTIC_PASSWORD") {
		return nil, fmt.Errorf("missing RESTIC_PASSWORD in global secret")
	}
	return env, nil
}

func (s *Server) repoEnvForNamespace(ctx context.Context, ns, bucket, _endpoint string, baseEnv []string) (resticcmd.RepoEnv, error) {
	// Prefer full repository URI from a snapshot in ns
	list, err := s.K8s.ListResource(ctx, k8s.GVRSnapshot, ns)
	if err == nil {
		for i := range list.Items {
			repo, _, _ := nestedString(list.Items[i].Object, "spec", "repository")
			if repo != "" {
				env := append([]string{}, baseEnv...)
				env = append(env, "RESTIC_REPOSITORY="+repo)
				return resticcmd.RepoEnv{Env: env}, nil
			}
		}
	}
	// Build from global endpoint + bucket
	ep := ""
	for _, e := range baseEnv {
		if strings.HasPrefix(e, "S3_ENDPOINT=") {
			ep = strings.TrimPrefix(e, "S3_ENDPOINT=")
		}
	}
	if ep == "" || bucket == "" {
		return resticcmd.RepoEnv{}, fmt.Errorf("cannot build repository for %s", ns)
	}
	// K8up style: s3:https://host/bucket
	uri := ep
	if !strings.HasPrefix(uri, "http") {
		uri = "https://" + uri
	}
	repo := "s3:" + strings.TrimRight(uri, "/") + "/" + strings.TrimPrefix(bucket, "/")
	env := append([]string{}, baseEnv...)
	env = append(env, "RESTIC_REPOSITORY="+repo)
	_ = ctx
	return resticcmd.RepoEnv{Env: env}, nil
}

func s3BucketFromRepoURI(repo string) string {
	const pfx = "s3:"
	if !strings.HasPrefix(repo, pfx) {
		return ""
	}
	rest := strings.TrimPrefix(repo, pfx)
	for _, sch := range []string{"https://", "http://"} {
		if strings.HasPrefix(rest, sch) {
			rest = strings.TrimPrefix(rest, sch)
			break
		}
	}
	_, path, ok := strings.Cut(rest, "/")
	if !ok || path == "" {
		return ""
	}
	return strings.TrimSuffix(path, "/")
}

func jsonInt64(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}
