package auth

import (
        "context"
        "net/http"
        "strings"

        "github.com/d-kholin/k8up-gui/internal/config"
)

type ctxKey int

const userKey ctxKey = 1

// User is the forward-auth identity (or dev stub).
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

// Middleware trusts reverse-proxy headers. No session cookies.
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
                                        r.Header.Get(cfg.AuthUserHeader),
                                        r.Header.Get("X-Forwarded-User"),
                                        r.Header.Get("X-Remote-User"),
                                ),
                                Email: r.Header.Get(cfg.AuthEmailHeader),
                        }
                        if u.Username == "" && cfg.DevAuthUser != "" {
                                u.Username = cfg.DevAuthUser
                                u.Email = cfg.DevAuthUser + "@localhost"
                        }
                        if u.Username == "" {
                                http.Error(w, "unauthorized: missing forward-auth identity", http.StatusUnauthorized)
                                return
                        }
                        next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
                })
        }
}

func firstNonEmpty(vals ...string) string {
        for _, v := range vals {
                if s := strings.TrimSpace(v); s != "" {
                        return s
                }
        }
        return ""
}
