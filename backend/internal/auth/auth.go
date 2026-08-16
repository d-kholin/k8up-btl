package auth

import (
        "context"
        "net/http"
        "strings"

        "github.com/d-kholin/k8up-gui/internal/config"
)

type ctxKey int

const userKey ctxKey = 1

// User is the reverse-proxy identity (or edge-trusted default).
type User struct {
        Username string `json:"username"`
        Email    string `json:"email,omitempty"`
}

func FromContext(ctx context.Context) (User, bool) {
        u, ok := ctx.Value(userKey).(User)
        return u, ok
}

func WithUser(ctx context.Context, u User) context.Context {
        return context.WithValue(ctx, userKey, u)
}

// Middleware trusts reverse-proxy identity headers. No session cookies.
//
// Header order (first non-empty wins for username):
//  1. configured AUTH_USER_HEADER (default X-authentik-username)
//  2. Pangolin: Remote-User
//  3. common: X-Forwarded-User, X-Remote-User, Remote-Email (as last resort for id)
//
// If no identity header is present:
//   - DEV_AUTH_USER (local only)
//   - else if AUTH_TRUST_EDGE=true → AUTH_DEFAULT_USER (Pangolin SSO at edge,
//     no identity headers injected — same model as lubelogger/vaultwarden)
//   - else 401
func Middleware(cfg config.Config) func(http.Handler) http.Handler {
        return func(next http.Handler) http.Handler {
                return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                        // Health stays open for probes.
                        if r.URL.Path == "/api/health" || r.URL.Path == "/healthz" {
                                next.ServeHTTP(w, r)
                                return
                        }
                        u := User{
                                Username: firstNonEmpty(
                                        headerCI(r, cfg.AuthUserHeader),
                                        headerCI(r, "Remote-User"),
                                        headerCI(r, "X-Forwarded-User"),
                                        headerCI(r, "X-Remote-User"),
                                        headerCI(r, "X-authentik-username"),
                                ),
                                Email: firstNonEmpty(
                                        headerCI(r, cfg.AuthEmailHeader),
                                        headerCI(r, "Remote-Email"),
                                        headerCI(r, "X-authentik-email"),
                                        headerCI(r, "X-Forwarded-Email"),
                                ),
                        }
                        // Prefer display name only for audit label when username empty.
                        if u.Username == "" {
                                if name := headerCI(r, "Remote-Name"); name != "" {
                                        u.Username = name
                                }
                        }
                        if u.Username == "" && cfg.DevAuthUser != "" {
                                u.Username = cfg.DevAuthUser
                                if u.Email == "" {
                                        u.Email = cfg.DevAuthUser + "@localhost"
                                }
                        }
                        if u.Username == "" && cfg.TrustEdge {
                                u.Username = cfg.DefaultUser
                                if u.Username == "" {
                                        u.Username = "operator"
                                }
                        }
                        if u.Username == "" {
                                http.Error(w, "unauthorized: missing forward-auth identity", http.StatusUnauthorized)
                                return
                        }
                        next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
                })
        }
}

// headerCI returns the first header value matching name case-insensitively.
func headerCI(r *http.Request, name string) string {
        if name == "" {
                return ""
        }
        if v := strings.TrimSpace(r.Header.Get(name)); v != "" {
                return v
        }
        // Canonical MIME header lookup is already case-insensitive via Get,
        // but some proxies send unusual casing — scan as fallback.
        want := strings.ToLower(name)
        for k, vals := range r.Header {
                if strings.ToLower(k) == want && len(vals) > 0 {
                        if s := strings.TrimSpace(vals[0]); s != "" {
                                return s
                        }
                }
        }
        return ""
}

func firstNonEmpty(vals ...string) string {
        for _, v := range vals {
                if s := strings.TrimSpace(v); s != "" {
                        return s
                }
        }
        return ""
}
