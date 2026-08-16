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
        StaticDir           string
        ResticBinary        string
        LogLevel            string
        AuditRetention      time.Duration
        ScaleDownTimeout    time.Duration
        RestoreTimeout      time.Duration
}

func Load() Config {
        return Config{
                HTTPAddr:            getenv("HTTP_ADDR", ":8080"),
                Kubeconfig:          os.Getenv("KUBECONFIG"),
                ArgoCDNamespace:     getenv("ARGOCD_NAMESPACE", "argocd"),
                AuditDBPath:         getenv("AUDIT_DB_PATH", "./data/audit.db"),
                PrometheusURL:       os.Getenv("PROMETHEUS_URL"),
                GrafanaDashboardURL: os.Getenv("GRAFANA_DASHBOARD_URL"),
                AuthUserHeader:      getenv("AUTH_USER_HEADER", "X-authentik-username"),
                AuthEmailHeader:     getenv("AUTH_EMAIL_HEADER", "X-authentik-email"),
                DevAuthUser:         os.Getenv("DEV_AUTH_USER"),
                StaticDir:           os.Getenv("STATIC_DIR"),
                ResticBinary:        getenv("RESTIC_BINARY", "restic"),
                LogLevel:            getenv("LOG_LEVEL", "info"),
                AuditRetention:      durationEnv("AUDIT_RETENTION", 90*24*time.Hour),
                ScaleDownTimeout:    durationEnv("SCALE_DOWN_TIMEOUT", 10*time.Minute),
                RestoreTimeout:      durationEnv("RESTORE_TIMEOUT", 2*time.Hour),
        }
}

func getenv(k, def string) string {
        if v := os.Getenv(k); v != "" {
                return v
        }
        return def
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
