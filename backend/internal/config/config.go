package config

import (
        "os"
        "strconv"
        "strings"
        "time"
)

type Config struct {
        HTTPAddr            string
        Kubeconfig          string
        ArgoCDNamespace     string
        AuditDBPath         string
        PrometheusURL       string
        GrafanaDashboardURL string
        AuthUserHeader      string
        AuthEmailHeader     string
        DevAuthUser         string
        // TrustEdge: when true, allow requests with no identity headers and
        // attribute them to DefaultUser. Use when Pangolin/Newt enforces SSO
        // at the edge but does not inject identity headers (typical lab setup).
        TrustEdge   bool
        DefaultUser string
        StaticDir   string
        ResticBinary string
        LogLevel     string
        AuditRetention   time.Duration
        ScaleDownTimeout time.Duration
        RestoreTimeout   time.Duration
        // Cluster-wide K8up backend secret (this homelab: k8up/k8up-global).
        K8upGlobalSecretNS   string
        K8upGlobalSecretName string
}

func Load() Config {
        return Config{
                HTTPAddr:             getenv("HTTP_ADDR", ":8080"),
                Kubeconfig:           os.Getenv("KUBECONFIG"),
                ArgoCDNamespace:      getenv("ARGOCD_NAMESPACE", "argocd"),
                AuditDBPath:          getenv("AUDIT_DB_PATH", "./data/audit.db"),
                PrometheusURL:        os.Getenv("PROMETHEUS_URL"),
                GrafanaDashboardURL:  os.Getenv("GRAFANA_DASHBOARD_URL"),
                AuthUserHeader:       getenv("AUTH_USER_HEADER", "X-authentik-username"),
                AuthEmailHeader:      getenv("AUTH_EMAIL_HEADER", "X-authentik-email"),
                DevAuthUser:          os.Getenv("DEV_AUTH_USER"),
                TrustEdge:            boolEnv("AUTH_TRUST_EDGE", false),
                DefaultUser:          getenv("AUTH_DEFAULT_USER", "operator"),
                StaticDir:            os.Getenv("STATIC_DIR"),
                ResticBinary:         getenv("RESTIC_BINARY", "restic"),
                LogLevel:             getenv("LOG_LEVEL", "info"),
                AuditRetention:       durationEnv("AUDIT_RETENTION", 90*24*time.Hour),
                ScaleDownTimeout:     durationEnv("SCALE_DOWN_TIMEOUT", 10*time.Minute),
                RestoreTimeout:       durationEnv("RESTORE_TIMEOUT", 2*time.Hour),
                K8upGlobalSecretNS:   getenv("K8UP_GLOBAL_SECRET_NAMESPACE", "k8up"),
                K8upGlobalSecretName: getenv("K8UP_GLOBAL_SECRET_NAME", "k8up-global"),
        }
}

func getenv(k, def string) string {
        if v := os.Getenv(k); v != "" {
                return v
        }
        return def
}

func boolEnv(k string, def bool) bool {
        v := strings.TrimSpace(strings.ToLower(os.Getenv(k)))
        if v == "" {
                return def
        }
        switch v {
        case "1", "true", "yes", "on":
                return true
        case "0", "false", "no", "off":
                return false
        default:
                return def
        }
}

func durationEnv(k string, def time.Duration) time.Duration {
        v := os.Getenv(k)
        if v == "" {
                return def
        }
        if d, err := time.ParseDuration(v); err == nil {
                return d
        }
        if n, err := strconv.Atoi(v); err == nil {
                return time.Duration(n) * time.Second
        }
        return def
}

func (c Config) DevMode() bool {
        return strings.TrimSpace(c.DevAuthUser) != ""
}
