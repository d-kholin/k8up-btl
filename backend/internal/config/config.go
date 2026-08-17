package config

import (
        "os"
        "path/filepath"
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
        // DefaultUser is the audit identity when no proxy header is present.
        DefaultUser string
        StaticDir   string
        ResticBinary string
        // ResticCacheDir persists restic's repo index/metadata cache across
        // invocations and pod restarts (defaults to a dir next to the audit DB).
        ResticCacheDir string
        LogLevel       string
        AuditRetention   time.Duration
        ScaleDownTimeout time.Duration
        RestoreTimeout   time.Duration
        // HistoryPollInterval is how often K8up job CRs are swept into the
        // durable backup_events history; HistoryRetention bounds that table.
        HistoryPollInterval time.Duration
        HistoryRetention    time.Duration
        // Cluster-wide K8up backend secret (this homelab: k8up/k8up-global).
        K8upGlobalSecretNS   string
        K8upGlobalSecretName string

        // Notifications. A channel is enabled when its required vars are set:
        // ntfy needs NTFY_TOPIC; email needs SMTP_HOST + SMTP_FROM + SMTP_TO.
        NtfyServer string
        NtfyTopic  string
        NtfyToken  string
        SMTPHost   string
        SMTPPort   int
        SMTPTLS    string // starttls (default) | tls | none
        SMTPUser   string
        SMTPPass   string
        SMTPFrom   string
        SMTPTo     []string
        // NotifyRestoreSuccess also alerts on successful GUI restores (failures
        // and interrupted restores always alert when a channel is configured).
        NotifyRestoreSuccess bool
}

func Load() Config {
        cfg := Config{
                HTTPAddr:             getenv("HTTP_ADDR", ":8080"),
                Kubeconfig:           os.Getenv("KUBECONFIG"),
                ArgoCDNamespace:      getenv("ARGOCD_NAMESPACE", "argocd"),
                AuditDBPath:          getenv("AUDIT_DB_PATH", "./data/audit.db"),
                PrometheusURL:        os.Getenv("PROMETHEUS_URL"),
                GrafanaDashboardURL:  os.Getenv("GRAFANA_DASHBOARD_URL"),
                AuthUserHeader:       getenv("AUTH_USER_HEADER", "X-authentik-username"),
                AuthEmailHeader:      getenv("AUTH_EMAIL_HEADER", "X-authentik-email"),
                DevAuthUser:          os.Getenv("DEV_AUTH_USER"),
                DefaultUser:          getenv("AUTH_DEFAULT_USER", "operator"),
                StaticDir:            os.Getenv("STATIC_DIR"),
                ResticBinary:         getenv("RESTIC_BINARY", "restic"),
                LogLevel:             getenv("LOG_LEVEL", "info"),
                AuditRetention:       durationEnv("AUDIT_RETENTION", 90*24*time.Hour),
                HistoryPollInterval:  durationEnv("HISTORY_POLL_INTERVAL", time.Minute),
                HistoryRetention:     durationEnv("HISTORY_RETENTION", 400*24*time.Hour),
                ScaleDownTimeout:     durationEnv("SCALE_DOWN_TIMEOUT", 10*time.Minute),
                RestoreTimeout:       durationEnv("RESTORE_TIMEOUT", 2*time.Hour),
                K8upGlobalSecretNS:   getenv("K8UP_GLOBAL_SECRET_NAMESPACE", "k8up"),
                K8upGlobalSecretName: getenv("K8UP_GLOBAL_SECRET_NAME", "k8up-global"),
                ResticCacheDir:       os.Getenv("RESTIC_CACHE_DIR"),
                NtfyServer:           getenv("NTFY_URL", "https://ntfy.sh"),
                NtfyTopic:            os.Getenv("NTFY_TOPIC"),
                NtfyToken:            os.Getenv("NTFY_TOKEN"),
                SMTPHost:             os.Getenv("SMTP_HOST"),
                SMTPPort:             intEnv("SMTP_PORT", 587),
                SMTPTLS:              getenv("SMTP_TLS", "starttls"),
                SMTPUser:             os.Getenv("SMTP_USERNAME"),
                SMTPPass:             os.Getenv("SMTP_PASSWORD"),
                SMTPFrom:             os.Getenv("SMTP_FROM"),
                SMTPTo:               splitCSV(os.Getenv("SMTP_TO")),
                NotifyRestoreSuccess: boolEnv("NOTIFY_RESTORE_SUCCESS", true),
        }
        if cfg.ResticCacheDir == "" {
                cfg.ResticCacheDir = filepath.Join(filepath.Dir(cfg.AuditDBPath), "restic-cache")
        }
        return cfg
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

func intEnv(k string, def int) int {
        v := os.Getenv(k)
        if v == "" {
                return def
        }
        if n, err := strconv.Atoi(v); err == nil {
                return n
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
        }
        return def
}

func splitCSV(s string) []string {
        var out []string
        for _, p := range strings.Split(s, ",") {
                if p = strings.TrimSpace(p); p != "" {
                        out = append(out, p)
                }
        }
        return out
}

func (c Config) DevMode() bool {
        return strings.TrimSpace(c.DevAuthUser) != ""
}

// NtfyEnabled / EmailEnabled report whether each notification channel has the
// minimum config to be constructed.
func (c Config) NtfyEnabled() bool { return c.NtfyTopic != "" }

func (c Config) EmailEnabled() bool {
        return c.SMTPHost != "" && c.SMTPFrom != "" && len(c.SMTPTo) > 0
}
