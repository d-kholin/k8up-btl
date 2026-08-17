// Package notify pushes operator alerts (job failures, restore outcomes) to
// external channels. Channels are configured via env and fan out best-effort:
// a broken channel logs and never blocks or fails the triggering operation.
package notify

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeveritySuccess Severity = "success"
	SeverityFailure Severity = "failure"
)

// Event is one notification, channel-agnostic.
type Event struct {
	Title    string
	Body     string
	Severity Severity
	// Tags are short keywords (kind, namespace) channels may surface, e.g.
	// ntfy emoji tags or an email subject suffix.
	Tags []string
}

// Channel delivers a single event to one destination.
type Channel interface {
	Name() string
	Send(ctx context.Context, e Event) error
}

const sendTimeout = 15 * time.Second

// Manager fans events out to all configured channels. The channel set can be
// swapped at runtime (UI settings overrides) via SetChannels.
type Manager struct {
	Log *slog.Logger

	chMu     sync.RWMutex
	channels []Channel
}

func NewManager(log *slog.Logger, channels ...Channel) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{Log: log, channels: channels}
}

// SetChannels atomically replaces the channel set.
func (m *Manager) SetChannels(channels ...Channel) {
	if m == nil {
		return
	}
	m.chMu.Lock()
	m.channels = channels
	m.chMu.Unlock()
}

func (m *Manager) snapshot() []Channel {
	if m == nil {
		return nil
	}
	m.chMu.RLock()
	defer m.chMu.RUnlock()
	out := make([]Channel, len(m.channels))
	copy(out, m.channels)
	return out
}

// Enabled reports whether any channel is configured.
func (m *Manager) Enabled() bool { return len(m.snapshot()) > 0 }

// ChannelNames lists configured channels (for /meta and the test endpoint).
func (m *Manager) ChannelNames() []string {
	chs := m.snapshot()
	if chs == nil {
		return nil
	}
	names := make([]string, 0, len(chs))
	for _, c := range chs {
		names = append(names, c.Name())
	}
	return names
}

// Send delivers e to every channel concurrently and returns per-channel errors.
func (m *Manager) Send(ctx context.Context, e Event) map[string]error {
	chs := m.snapshot()
	if len(chs) == 0 {
		return nil
	}
	errs := make(map[string]error, len(chs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, c := range chs {
		wg.Add(1)
		go func(c Channel) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, sendTimeout)
			defer cancel()
			err := c.Send(cctx, e)
			if err != nil {
				m.Log.Warn("notification failed", "channel", c.Name(), "title", e.Title, "err", err)
			}
			mu.Lock()
			errs[c.Name()] = err
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	return errs
}

// Go delivers asynchronously — callers on hot paths (orchestrator steps,
// history sweep) must never wait on SMTP round-trips.
func (m *Manager) Go(e Event) {
	if !m.Enabled() {
		return
	}
	go m.Send(context.Background(), e)
}
