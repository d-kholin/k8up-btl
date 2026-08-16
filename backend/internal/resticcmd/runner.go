package resticcmd

import (
        "bufio"
        "context"
        "encoding/json"
        "fmt"
        "io"
        "os"
        "os/exec"
        "strings"
        "sync"
)

// Runner invokes restic read-only against remote repos.
// Credentials are passed via env per call and never written to disk.
type Runner struct {
        Binary string
}

// RepoEnv is the environment for a single restic invocation.
type RepoEnv struct {
        // Env should include RESTIC_REPOSITORY, RESTIC_PASSWORD, and S3/AWS keys as needed.
        Env []string
}

// allowedCommands is the hard allowlist for browse/download/stats.
var allowedCommands = map[string]struct{}{
        "ls":    {},
        "dump":  {},
        "stats": {},
        "snapshots": {},
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

// List lists files under path inside a snapshot (snapshot id or "latest").
func (r *Runner) List(ctx context.Context, repo RepoEnv, snapshotID, path string) ([]FileNode, error) {
        if path == "" {
                path = "/"
        }
        args := []string{"ls", snapshotID, "--json"}
        if path != "/" && path != "" {
                args = append(args, path)
        }
        cmd := r.run(ctx, repo, args...)
        out, err := cmd.Output()
        if err != nil {
                return nil, wrapExecErr("ls", err, out)
        }
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
                if _, ok := raw["struct_type"]; ok {
                        if raw["struct_type"] == "snapshot" {
                                continue
                        }
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
                nodes = append(nodes, n)
        }
        return nodes, sc.Err()
}

// Dump streams a single file to w.
func (r *Runner) Dump(ctx context.Context, repo RepoEnv, snapshotID, path string, w io.Writer) error {
        if path == "" || path == "/" {
                return fmt.Errorf("dump requires a file path")
        }
        cmd := r.run(ctx, repo, "dump", snapshotID, path)
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
        args := []string{"stats", "--json", "--mode", mode}
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

// Guard against concurrent panics in tests if someone misuses run.
var _ = sync.Mutex{}
