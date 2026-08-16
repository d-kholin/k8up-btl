package auth

import (
        "context"
        "net/http"
        "strings"

        "github.com/d-kholin/k8up-gui/internal/config"
)

type ctxKey int

const userKey ctxKey = 1

// User is who we attribute audit actions to. There is no login in the app;
// access control is entirely at the reverse proxy (Pangolin/Newt).
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

// Middleware never rejects requests. Identity for audit logs is either a
// best-effort header (if the proxy happens to send one) or AUTH_DEFAULT_USER.
func Middleware(cfg config.Config) func(http.Handler) http.Handler {
        return func(next http.Handler) http.Handler {
                return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                        name := firstNonEmpty(
                                headerCI(r, cfg.AuthUserHeader),
                                headerCI(r, "Remote-User"),
                                headerCI(r, "X-Forwarded-User"),
                                headerCI(r, "X-Remote-User"),
                                headerCI(r, "X-authentik-username"),
                                cfg.DevAuthUser,
                                cfg.DefaultUser,
                                "operator",
                        )
                        email := firstNonEmpty(
                                headerCI(r, cfg.AuthEmailHeader),
                                headerCI(r, "Remote-Email"),
                                headerCI(r, "X-authentik-email"),
                                headerCI(r, "X-Forwarded-Email"),
                        )
                        u := User{Username: name, Email: email}
                        next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
                })
        }
}

func headerCI(r *http.Request, name string) string {
        if name == "" {
                return ""
        }
        if v := strings.TrimSpace(r.Header.Get(name)); v != "" {
                return v
        }
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
