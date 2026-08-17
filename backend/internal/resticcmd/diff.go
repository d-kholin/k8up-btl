package resticcmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// DiffChange is one changed path between two snapshots.
type DiffChange struct {
	Path string `json:"path"`
	// Modifier is restic's raw marker: "+" added, "-" removed, "M" content
	// modified, "T" type changed, "U" metadata-only. May combine (e.g. "MU").
	Modifier string `json:"modifier"`
	// Kind buckets the modifier for the UI: added|removed|modified|metadata.
	Kind string `json:"kind"`
}

// DiffCounts mirrors restic's added/removed statistics blocks.
type DiffCounts struct {
	Files int64 `json:"files"`
	Dirs  int64 `json:"dirs"`
	Bytes int64 `json:"bytes"`
}

// DiffResult is a parsed `restic diff --json base target`.
type DiffResult struct {
	SourceSnapshot string       `json:"sourceSnapshot"`
	TargetSnapshot string       `json:"targetSnapshot"`
	ChangedFiles   int64        `json:"changedFiles"`
	Added          DiffCounts   `json:"added"`
	Removed        DiffCounts   `json:"removed"`
	Changes        []DiffChange `json:"changes"`
}

// Diff compares two snapshots in the same repository (base = older, target =
// newer). Results are cached: snapshots are immutable, so a (repo, base,
// target) diff never changes.
func (r *Runner) Diff(ctx context.Context, repo RepoEnv, baseID, targetID string) (*DiffResult, error) {
	cacheKey := listCacheKey(repo, baseID, "\x01diff\x01"+targetID)
	if res, ok := r.diffCache.get(cacheKey); ok {
		return res, nil
	}
	// --no-lock: read-only; don't contend with scheduled Backup/Prune locks.
	cmd := r.run(ctx, repo, "diff", "--json", "--no-lock", baseID, targetID)
	out, err := cmd.Output()
	if err != nil {
		return nil, wrapExecErr("diff", err, out)
	}
	res, err := parseDiffJSON(out)
	if err != nil {
		return nil, err
	}
	r.diffCache.put(cacheKey, res)
	return res, nil
}

func parseDiffJSON(out []byte) (*DiffResult, error) {
	res := &DiffResult{Changes: []DiffChange{}}
	sc := bufio.NewScanner(bytes.NewReader(out))
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 10*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			MessageType string `json:"message_type"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		switch probe.MessageType {
		case "change":
			var c struct {
				Path     string `json:"path"`
				Modifier string `json:"modifier"`
			}
			if err := json.Unmarshal(line, &c); err != nil || c.Path == "" {
				continue
			}
			res.Changes = append(res.Changes, DiffChange{
				Path:     c.Path,
				Modifier: c.Modifier,
				Kind:     diffKind(c.Modifier),
			})
		case "statistics":
			var s struct {
				SourceSnapshot string `json:"source_snapshot"`
				TargetSnapshot string `json:"target_snapshot"`
				ChangedFiles   int64  `json:"changed_files"`
				Added          struct {
					Files int64 `json:"files"`
					Dirs  int64 `json:"dirs"`
					Bytes int64 `json:"bytes"`
				} `json:"added"`
				Removed struct {
					Files int64 `json:"files"`
					Dirs  int64 `json:"dirs"`
					Bytes int64 `json:"bytes"`
				} `json:"removed"`
			}
			if err := json.Unmarshal(line, &s); err != nil {
				continue
			}
			res.SourceSnapshot = s.SourceSnapshot
			res.TargetSnapshot = s.TargetSnapshot
			res.ChangedFiles = s.ChangedFiles
			res.Added = DiffCounts{Files: s.Added.Files, Dirs: s.Added.Dirs, Bytes: s.Added.Bytes}
			res.Removed = DiffCounts{Files: s.Removed.Files, Dirs: s.Removed.Dirs, Bytes: s.Removed.Bytes}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(res.Changes, func(i, j int) bool {
		return res.Changes[i].Path < res.Changes[j].Path
	})
	return res, nil
}

func diffKind(mod string) string {
	switch {
	case strings.Contains(mod, "+"):
		return "added"
	case strings.Contains(mod, "-"):
		return "removed"
	case strings.Contains(mod, "M"), strings.Contains(mod, "T"):
		return "modified"
	case strings.Contains(mod, "U"):
		return "metadata"
	default:
		return "modified"
	}
}

// --- diff cache: immutable inputs, so TTL only bounds memory, not staleness ---

const (
	diffCacheTTL = 30 * time.Minute
	diffCacheMax = 64
)

type diffCacheEntry struct {
	at  time.Time
	res *DiffResult
}

type diffCache struct {
	mu      sync.Mutex
	entries map[string]diffCacheEntry
}

func (c *diffCache) get(key string) (*DiffResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.at) {
		delete(c.entries, key)
		return nil, false
	}
	return e.res, true
}

func (c *diffCache) put(key string, res *DiffResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]diffCacheEntry)
	}
	if len(c.entries) >= diffCacheMax {
		now := time.Now()
		for k, e := range c.entries {
			if now.After(e.at) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= diffCacheMax {
			c.entries = make(map[string]diffCacheEntry)
		}
	}
	c.entries[key] = diffCacheEntry{at: time.Now().Add(diffCacheTTL), res: res}
}
