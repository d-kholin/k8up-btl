package resticcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// Runner invokes restic read-only against remote repos.
// Credentials are passed via env per call and never written to disk.
type Runner struct {
	Binary string

	// CacheDir, when set, is passed as RESTIC_CACHE_DIR so repo index/metadata
	// persist across invocations — first `ls`/`stats` per repo pays the S3 index
	// fetch, subsequent calls hit local disk.
	CacheDir string

	// ListCacheTTL caches directory listings (snapshots are immutable).
	// Zero uses defaultListCacheTTL. Negative disables the cache.
	ListCacheTTL time.Duration

	listCache    listCache
	diffCache    diffCache
	cacheDirOnce sync.Once
}

const defaultListCacheTTL = 5 * time.Minute

// RepoEnv is the environment for a single restic invocation.
type RepoEnv struct {
	// Env should include RESTIC_REPOSITORY, RESTIC_PASSWORD, and S3/AWS keys as needed.
	Env []string
}

// allowedCommands is the hard allowlist for browse/download/stats.
var allowedCommands = map[string]struct{}{
	"ls":        {},
	"dump":      {},
	"stats":     {},
	"snapshots": {},
	"diff":      {},
}

func (r *Runner) bin() string {
	if r.Binary != "" {
		return r.Binary
	}
	return "restic"
}

func (r *Runner) run(ctx context.Context, repo RepoEnv, args ...string) *exec.Cmd {
	if len(args) == 0 {
		panic("restic: empty args")
	}
	if _, ok := allowedCommands[args[0]]; !ok {
		// Fail closed — never run mutating commands through this runner.
		panic(fmt.Sprintf("restic: command %q not allowed", args[0]))
	}
	cmd := exec.CommandContext(ctx, r.bin(), args...)
	// Clean env + only what we need. Include PATH so restic can find helpers if any.
	// Propagate TLS trust roots so restic can verify S3/Garage HTTPS (e.g. Let's Encrypt).
	base := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"LANG=C.UTF-8",
	}
	for _, k := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE"} {
		if v := os.Getenv(k); v != "" {
			base = append(base, k+"="+v)
		}
	}
	if r.CacheDir != "" {
		r.cacheDirOnce.Do(func() { _ = os.MkdirAll(r.CacheDir, 0o700) })
		base = append(base, "RESTIC_CACHE_DIR="+r.CacheDir)
	}
	cmd.Env = append(base, repo.Env...)
	return cmd
}

// FileNode is one entry from `restic ls --json`.
type FileNode struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // file | dir
	Path  string `json:"path"`
	Size  int64  `json:"size,omitempty"`
	Mtime string `json:"mtime,omitempty"`
}

// NormalizeBrowsePath ensures a clean absolute path for restic ls filters.
func NormalizeBrowsePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	// path.Clean collapses .. and duplicate slashes; keep absolute.
	p = path.Clean(p)
	if p == "." || p == "" {
		return "/"
	}
	return p
}

// List lists immediate children under path inside a snapshot (snapshot id or "latest").
// Never recursive — full-tree ls is too slow against remote S3/Garage repos.
func (r *Runner) List(ctx context.Context, repo RepoEnv, snapshotID, browsePath string) ([]FileNode, error) {
	browsePath = NormalizeBrowsePath(browsePath)
	cacheKey := listCacheKey(repo, snapshotID, browsePath)
	if nodes, ok := r.listCache.get(cacheKey); ok {
		return cloneNodes(nodes), nil
	}

	// Always pass an absolute directory filter and never --recursive.
	// Without a path filter, restic ls dumps the entire snapshot tree (multi-second S3 walk).
	// --no-lock: browse is read-only; skip lock round-trips on shared repos.
	args := []string{"ls", "--json", "--no-lock", snapshotID, browsePath}
	cmd := r.run(ctx, repo, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, wrapExecErr("ls", err, out)
	}
	nodes, err := parseLSJSON(out, browsePath)
	if err != nil {
		return nil, err
	}
	r.listCache.put(cacheKey, nodes, r.listTTL())
	return cloneNodes(nodes), nil
}

func (r *Runner) listTTL() time.Duration {
	if r.ListCacheTTL < 0 {
		return 0
	}
	if r.ListCacheTTL == 0 {
		return defaultListCacheTTL
	}
	return r.ListCacheTTL
}

func listCacheKey(repo RepoEnv, snapshotID, browsePath string) string {
	// Include repository URI so same snap id in different repos cannot collide.
	repoURL := ""
	for _, e := range repo.Env {
		if strings.HasPrefix(e, "RESTIC_REPOSITORY=") {
			repoURL = strings.TrimPrefix(e, "RESTIC_REPOSITORY=")
			break
		}
	}
	return repoURL + "\x00" + snapshotID + "\x00" + browsePath
}

func parseLSJSON(out []byte, browsePath string) ([]FileNode, error) {
	var nodes []FileNode
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	// raise token size for long paths
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 10*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		// restic ls --json emits a snapshot header object then nodes
		if st, ok := raw["struct_type"].(string); ok && st == "snapshot" {
			continue
		}
		n := FileNode{}
		if v, ok := raw["name"].(string); ok {
			n.Name = v
		}
		if v, ok := raw["path"].(string); ok {
			n.Path = v
		}
		if v, ok := raw["type"].(string); ok {
			n.Type = v
		}
		switch sz := raw["size"].(type) {
		case float64:
			n.Size = int64(sz)
		}
		if v, ok := raw["mtime"].(string); ok {
			n.Mtime = v
		}
		if n.Path == "" && n.Name == "" {
			continue
		}
		if n.Path == "" {
			// Construct path from browse + name when restic omits it.
			if browsePath == "/" {
				n.Path = "/" + n.Name
			} else {
				n.Path = browsePath + "/" + n.Name
			}
		}
		if !isDirectChild(browsePath, n.Path) {
			continue
		}
		if n.Name == "" {
			n.Name = path.Base(n.Path)
		}
		nodes = append(nodes, n)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		di, dj := nodes[i].Type == "dir", nodes[j].Type == "dir"
		if di != dj {
			return di // directories first
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	return nodes, nil
}

// isDirectChild reports whether childPath is an immediate child of parentPath.
func isDirectChild(parentPath, childPath string) bool {
	parentPath = NormalizeBrowsePath(parentPath)
	childPath = NormalizeBrowsePath(childPath)
	if childPath == parentPath {
		return false // skip the directory entry itself
	}
	var wantPrefix string
	if parentPath == "/" {
		wantPrefix = "/"
	} else {
		wantPrefix = parentPath + "/"
	}
	if !strings.HasPrefix(childPath, wantPrefix) {
		return false
	}
	rest := strings.TrimPrefix(childPath, wantPrefix)
	if rest == "" || strings.Contains(rest, "/") {
		return false
	}
	return true
}

func cloneNodes(in []FileNode) []FileNode {
	if in == nil {
		return []FileNode{}
	}
	out := make([]FileNode, len(in))
	copy(out, in)
	return out
}

// DumpOptions controls restic dump output.
// Archive is empty for single-file raw dump; "zip" or "tar" for folders / full snapshot.
type DumpOptions struct {
	// Archive: "", "zip", or "tar". Required for directories and for path "/".
	Archive string
}

// Dump streams a file or folder archive from a snapshot to w.
// Pass path "/" with Archive set to dump the whole snapshot.
func (r *Runner) Dump(ctx context.Context, repo RepoEnv, snapshotID, filePath string, w io.Writer, opts DumpOptions) error {
	filePath = NormalizeBrowsePath(filePath)
	archive := strings.ToLower(strings.TrimSpace(opts.Archive))
	switch archive {
	case "", "zip", "tar":
	default:
		return fmt.Errorf("dump archive must be empty, zip, or tar")
	}
	if filePath == "/" && archive == "" {
		// Whole-snapshot dump must be an archive.
		archive = "zip"
	}

	args := []string{"dump", "--no-lock"}
	if archive != "" {
		args = append(args, "--archive", archive)
	}
	args = append(args, snapshotID, filePath)
	cmd := r.run(ctx, repo, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	_, copyErr := io.Copy(w, stdout)
	waitErr := cmd.Wait()
	if copyErr != nil {
		return copyErr
	}
	if waitErr != nil {
		return fmt.Errorf("restic dump: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Stats runs `restic stats` JSON for restore-size or raw-data.
func (r *Runner) Stats(ctx context.Context, repo RepoEnv, snapshotID, mode string) (map[string]any, error) {
	if mode == "" {
		mode = "restore-size"
	}
	args := []string{"stats", "--json", "--no-lock", "--mode", mode}
	if snapshotID != "" {
		args = append(args, snapshotID)
	}
	cmd := r.run(ctx, repo, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, wrapExecErr("stats", err, out)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func wrapExecErr(op string, err error, out []byte) error {
	var stderr string
	if ee, ok := err.(*exec.ExitError); ok {
		stderr = string(ee.Stderr)
	}
	if stderr == "" {
		stderr = string(out)
	}
	return fmt.Errorf("restic %s: %w: %s", op, err, strings.TrimSpace(stderr))
}

// --- short TTL listing cache (snapshots are content-addressed / immutable) ---

type listCacheEntry struct {
	at    time.Time
	nodes []FileNode
}

type listCache struct {
	mu      sync.Mutex
	entries map[string]listCacheEntry
}

func (c *listCache) get(key string) ([]FileNode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		return nil, false
	}
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	// `at` is absolute expiry deadline.
	if time.Now().After(e.at) {
		delete(c.entries, key)
		return nil, false
	}
	return e.nodes, true
}

func (c *listCache) put(key string, nodes []FileNode, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]listCacheEntry)
	}
	// Cap map growth: drop expired, then if still huge clear half by oldest.
	if len(c.entries) > 512 {
		now := time.Now()
		for k, e := range c.entries {
			if now.After(e.at) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) > 512 {
			c.entries = make(map[string]listCacheEntry)
		}
	}
	c.entries[key] = listCacheEntry{
		at:    time.Now().Add(ttl),
		nodes: cloneNodes(nodes),
	}
}
