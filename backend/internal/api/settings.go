package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/auth"
	"github.com/d-kholin/k8up-gui/internal/config"
	"github.com/d-kholin/k8up-gui/internal/notify"
)

// Notification config model: env vars (git-managed manifests) are the
// baseline; UI overrides stored in SQLite win field-by-field. Empty override
// = use env. Secrets (ntfy token, SMTP password) are write-only through the
// API — the UI only ever learns whether one is set.

const notifyOverridesKey = "notify_overrides"

// LoadNotifyOverrides reads persisted overrides ("" row → zero value).
func LoadNotifyOverrides(ctx context.Context, store *audit.Store) (config.NotifyOverrides, error) {
	var ov config.NotifyOverrides
	if store == nil {
		return ov, nil
	}
	raw, err := store.GetSetting(ctx, notifyOverridesKey)
	if err != nil || raw == "" {
		return ov, err
	}
	if err := json.Unmarshal([]byte(raw), &ov); err != nil {
		return config.NotifyOverrides{}, fmt.Errorf("corrupt notify overrides: %w", err)
	}
	return ov, nil
}

type notifyFieldView struct {
	NtfyServer   string `json:"ntfyServer"`
	NtfyTopic    string `json:"ntfyTopic"`
	NtfyTokenSet bool   `json:"ntfyTokenSet"`
	SMTPHost     string `json:"smtpHost"`
	SMTPPort     int    `json:"smtpPort"`
	SMTPTLS      string `json:"smtpTls"`
	SMTPUser     string `json:"smtpUser"`
	SMTPPassSet  bool   `json:"smtpPassSet"`
	SMTPFrom     string `json:"smtpFrom"`
	SMTPTo       string `json:"smtpTo"`
}

type notifySettingsView struct {
	Env       notifyFieldView `json:"env"`
	Overrides struct {
		notifyFieldView
		NtfyDisabled  bool `json:"ntfyDisabled"`
		EmailDisabled bool `json:"emailDisabled"`
	} `json:"overrides"`
	Channels []string `json:"channels"`
}

func (s *Server) notifySettingsView(ov config.NotifyOverrides) notifySettingsView {
	var v notifySettingsView
	v.Env = notifyFieldView{
		NtfyServer:   s.Cfg.NtfyServer,
		NtfyTopic:    s.Cfg.NtfyTopic,
		NtfyTokenSet: s.Cfg.NtfyToken != "",
		SMTPHost:     s.Cfg.SMTPHost,
		SMTPPort:     s.Cfg.SMTPPort,
		SMTPTLS:      s.Cfg.SMTPTLS,
		SMTPUser:     s.Cfg.SMTPUser,
		SMTPPassSet:  s.Cfg.SMTPPass != "",
		SMTPFrom:     s.Cfg.SMTPFrom,
		SMTPTo:       joinCSV(s.Cfg.SMTPTo),
	}
	v.Overrides.notifyFieldView = notifyFieldView{
		NtfyServer:   ov.NtfyServer,
		NtfyTopic:    ov.NtfyTopic,
		NtfyTokenSet: ov.NtfyToken != "",
		SMTPHost:     ov.SMTPHost,
		SMTPPort:     ov.SMTPPort,
		SMTPTLS:      ov.SMTPTLS,
		SMTPUser:     ov.SMTPUser,
		SMTPPassSet:  ov.SMTPPass != "",
		SMTPFrom:     ov.SMTPFrom,
		SMTPTo:       ov.SMTPTo,
	}
	v.Overrides.NtfyDisabled = ov.NtfyDisabled
	v.Overrides.EmailDisabled = ov.EmailDisabled
	v.Channels = s.Notify.ChannelNames()
	if v.Channels == nil {
		v.Channels = []string{}
	}
	return v
}

func (s *Server) handleGetNotifySettings(w http.ResponseWriter, r *http.Request) {
	ov, err := LoadNotifyOverrides(r.Context(), s.Audit)
	if err != nil {
		s.writeErr(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s.notifySettingsView(ov))
}

// notifySettingsUpdate uses pointers so PUT is merge-style: absent field =
// unchanged, "" (or 0/false) = clear the override back to the env value.
type notifySettingsUpdate struct {
	NtfyServer   *string `json:"ntfyServer"`
	NtfyTopic    *string `json:"ntfyTopic"`
	NtfyToken    *string `json:"ntfyToken"`
	NtfyDisabled *bool   `json:"ntfyDisabled"`

	SMTPHost      *string `json:"smtpHost"`
	SMTPPort      *int    `json:"smtpPort"`
	SMTPTLS       *string `json:"smtpTls"`
	SMTPUser      *string `json:"smtpUser"`
	SMTPPass      *string `json:"smtpPass"`
	SMTPFrom      *string `json:"smtpFrom"`
	SMTPTo        *string `json:"smtpTo"`
	EmailDisabled *bool   `json:"emailDisabled"`
}

func (s *Server) handlePutNotifySettings(w http.ResponseWriter, r *http.Request) {
	if s.Audit == nil {
		http.Error(w, "settings store unavailable", http.StatusServiceUnavailable)
		return
	}
	var upd notifySettingsUpdate
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	ov, err := LoadNotifyOverrides(r.Context(), s.Audit)
	if err != nil {
		s.writeErr(w, err, http.StatusInternalServerError)
		return
	}
	if upd.NtfyServer != nil {
		ov.NtfyServer = *upd.NtfyServer
	}
	if upd.NtfyTopic != nil {
		ov.NtfyTopic = *upd.NtfyTopic
	}
	if upd.NtfyToken != nil {
		ov.NtfyToken = *upd.NtfyToken
	}
	if upd.NtfyDisabled != nil {
		ov.NtfyDisabled = *upd.NtfyDisabled
	}
	if upd.SMTPHost != nil {
		ov.SMTPHost = *upd.SMTPHost
	}
	if upd.SMTPPort != nil {
		if *upd.SMTPPort < 0 || *upd.SMTPPort > 65535 {
			http.Error(w, "smtpPort must be 0-65535", http.StatusBadRequest)
			return
		}
		ov.SMTPPort = *upd.SMTPPort
	}
	if upd.SMTPTLS != nil {
		switch *upd.SMTPTLS {
		case "", "starttls", "tls", "none":
			ov.SMTPTLS = *upd.SMTPTLS
		default:
			http.Error(w, "smtpTls must be starttls, tls, or none", http.StatusBadRequest)
			return
		}
	}
	if upd.SMTPUser != nil {
		ov.SMTPUser = *upd.SMTPUser
	}
	if upd.SMTPPass != nil {
		ov.SMTPPass = *upd.SMTPPass
	}
	if upd.SMTPFrom != nil {
		ov.SMTPFrom = *upd.SMTPFrom
	}
	if upd.SMTPTo != nil {
		ov.SMTPTo = *upd.SMTPTo
	}
	if upd.EmailDisabled != nil {
		ov.EmailDisabled = *upd.EmailDisabled
	}

	raw, err := json.Marshal(ov)
	if err != nil {
		s.writeErr(w, err, http.StatusInternalServerError)
		return
	}
	if err := s.Audit.SetSetting(r.Context(), notifyOverridesKey, string(raw)); err != nil {
		s.writeErr(w, err, http.StatusInternalServerError)
		return
	}

	// Apply immediately: rebuild channels on the live manager.
	s.Notify.SetChannels(notify.Build(s.Cfg.WithNotifyOverrides(ov))...)
	s.Log.Info("notification settings updated", "channels", s.Notify.ChannelNames())

	u, _ := auth.FromContext(r.Context())
	_, _ = s.Audit.Insert(r.Context(), audit.Entry{
		Kind:   "system",
		Actor:  u.Username,
		Status: "notify_settings_updated",
		Detail: fmt.Sprintf("channels: %v", s.Notify.ChannelNames()),
	})

	writeJSON(w, http.StatusOK, s.notifySettingsView(ov))
}

func joinCSV(list []string) string {
	out := ""
	for i, v := range list {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
