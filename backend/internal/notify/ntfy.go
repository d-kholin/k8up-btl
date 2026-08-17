package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Ntfy publishes to an ntfy topic (https://docs.ntfy.sh/publish/).
type Ntfy struct {
	// Server is the ntfy base URL, e.g. https://ntfy.sh or a self-hosted instance.
	Server string
	Topic  string
	// Token is an optional access token sent as a Bearer header.
	Token string

	Client *http.Client
}

func (n *Ntfy) Name() string { return "ntfy" }

func (n *Ntfy) Send(ctx context.Context, e Event) error {
	url := strings.TrimRight(n.Server, "/") + "/" + n.Topic
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(e.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", e.Title)
	req.Header.Set("Priority", ntfyPriority(e.Severity))
	if tags := ntfyTags(e); tags != "" {
		req.Header.Set("Tags", tags)
	}
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}

	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ntfy: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func ntfyPriority(s Severity) string {
	switch s {
	case SeverityFailure:
		return "high"
	default:
		return "default"
	}
}

func ntfyTags(e Event) string {
	tags := make([]string, 0, len(e.Tags)+1)
	switch e.Severity {
	case SeverityFailure:
		tags = append(tags, "rotating_light")
	case SeveritySuccess:
		tags = append(tags, "white_check_mark")
	}
	for _, t := range e.Tags {
		t = strings.TrimSpace(strings.ToLower(t))
		if t != "" {
			tags = append(tags, t)
		}
	}
	return strings.Join(tags, ",")
}
