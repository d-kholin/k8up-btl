package notify

import "github.com/d-kholin/k8up-gui/internal/config"

// SubjectPrefix tags every email subject sent by this app.
const SubjectPrefix = "[k8up btl]"

// Build constructs the channel set for an effective config (env baseline with
// any UI overrides already applied via cfg.WithNotifyOverrides).
func Build(cfg config.Config) []Channel {
	var channels []Channel
	if cfg.NtfyEnabled() {
		channels = append(channels, &Ntfy{Server: cfg.NtfyServer, Topic: cfg.NtfyTopic, Token: cfg.NtfyToken})
	}
	if cfg.EmailEnabled() {
		channels = append(channels, &Email{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, TLSMode: cfg.SMTPTLS,
			Username: cfg.SMTPUser, Password: cfg.SMTPPass,
			From: cfg.SMTPFrom, To: cfg.SMTPTo,
			SubjectPrefix: SubjectPrefix,
		})
	}
	return channels
}
