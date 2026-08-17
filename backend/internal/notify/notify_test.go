package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/restore"
)

func TestNtfySend(t *testing.T) {
	var gotPath, gotTitle, gotPriority, gotTags, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotTags = r.Header.Get("Tags")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}))
	defer srv.Close()

	n := &Ntfy{Server: srv.URL, Topic: "backups", Token: "tk_secret"}
	err := n.Send(context.Background(), Event{
		Title:    "Backup failed: ns/job",
		Body:     "details here",
		Severity: SeverityFailure,
		Tags:     []string{"backup", "myns"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/backups" {
		t.Errorf("path: %q", gotPath)
	}
	if gotTitle != "Backup failed: ns/job" {
		t.Errorf("title: %q", gotTitle)
	}
	if gotPriority != "high" {
		t.Errorf("priority: %q", gotPriority)
	}
	if gotTags != "rotating_light,backup,myns" {
		t.Errorf("tags: %q", gotTags)
	}
	if gotAuth != "Bearer tk_secret" {
		t.Errorf("auth: %q", gotAuth)
	}
	if gotBody != "details here" {
		t.Errorf("body: %q", gotBody)
	}
}

func TestNtfySendErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()
	n := &Ntfy{Server: srv.URL, Topic: "x"}
	if err := n.Send(context.Background(), Event{Title: "t"}); err == nil {
		t.Fatal("expected error on 403")
	}
}

func TestEmailMessage(t *testing.T) {
	m := &Email{From: "btl@example.com", To: []string{"a@example.com", "b@example.com"}, SubjectPrefix: "[k8up btl]"}
	msg := string(m.message(Event{Title: "Backup failed", Body: "line1\nline2"}))
	for _, want := range []string{
		"From: btl@example.com\r\n",
		"To: a@example.com, b@example.com\r\n",
		"Subject: [k8up btl] Backup failed\r\n",
		"line1\r\nline2\r\n",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestJobFailureEvent(t *testing.T) {
	fin := time.Date(2026, 8, 17, 3, 0, 0, 0, time.UTC)
	e := JobFailureEvent(audit.BackupEvent{
		Kind: "Backup", Namespace: "immich", Name: "schedule-backup-abc",
		Schedule: "immich-schedule", Status: "failed", Message: "restic exit 1",
		StartedAt: fin.Add(-5 * time.Minute), FinishedAt: &fin,
	})
	if e.Severity != SeverityFailure {
		t.Errorf("severity: %v", e.Severity)
	}
	if e.Title != "Backup failed: immich/schedule-backup-abc" {
		t.Errorf("title: %q", e.Title)
	}
	for _, want := range []string{"immich-schedule", "restic exit 1"} {
		if !strings.Contains(e.Body, want) {
			t.Errorf("body missing %q:\n%s", want, e.Body)
		}
	}
}

func TestRestoreOutcomeEvent(t *testing.T) {
	if _, ok := RestoreOutcomeEvent(restore.State{Step: restore.StepRestoring}); ok {
		t.Fatal("non-terminal step should not notify")
	}
	e, ok := RestoreOutcomeEvent(restore.State{
		Step: restore.StepDone, PVCNamespace: "immich", PVCName: "library", SnapshotID: "abcdef1234567890",
	})
	if !ok || e.Severity != SeveritySuccess {
		t.Fatalf("done: ok=%v sev=%v", ok, e.Severity)
	}
	if !strings.Contains(e.Body, "abcdef123456") {
		t.Errorf("body missing short snapshot id: %s", e.Body)
	}

	paused, okf := RestoreOutcomeEvent(restore.State{
		Step: restore.StepFailed, PVCNamespace: "immich", PVCName: "library",
		LastError: "boom", ArgoPausedGlobally: true,
	})
	if !okf || paused.Severity != SeverityFailure {
		t.Fatalf("failed: ok=%v sev=%v", okf, paused.Severity)
	}
	if !strings.Contains(paused.Body, "still PAUSED") {
		t.Errorf("failed body should warn about paused Argo:\n%s", paused.Body)
	}
}

func TestManagerFanOutAndNil(t *testing.T) {
	var m *Manager
	if m.Enabled() {
		t.Fatal("nil manager should be disabled")
	}
	m.Go(Event{Title: "x"}) // must not panic

	var got atomic.Int32
	ch := channelFunc(func(context.Context, Event) error { got.Add(1); return nil })
	mgr := NewManager(nil, ch, ch)
	errs := mgr.Send(context.Background(), Event{Title: "t"})
	if got.Load() != 2 || len(errs) != 1 { // same Name() → one map key, both invoked
		t.Fatalf("got=%d errs=%d", got.Load(), len(errs))
	}
}

type channelFunc func(context.Context, Event) error

func (f channelFunc) Name() string                            { return "test" }
func (f channelFunc) Send(ctx context.Context, e Event) error { return f(ctx, e) }
