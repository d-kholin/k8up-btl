package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/lab"
	"github.com/d-kholin/k8up-gui/internal/restore"
)

// JobFailureEvent formats a K8up job CR failure (Backup/Check/Prune/Restore).
func JobFailureEvent(e audit.BackupEvent) Event {
	title := fmt.Sprintf("%s failed: %s/%s", e.Kind, e.Namespace, e.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s in namespace %s failed.\n", e.Kind, e.Name, e.Namespace)
	if e.Schedule != "" {
		fmt.Fprintf(&b, "Schedule: %s\n", e.Schedule)
	}
	fmt.Fprintf(&b, "Started: %s\n", e.StartedAt.Format("2006-01-02 15:04:05 MST"))
	if e.FinishedAt != nil {
		fmt.Fprintf(&b, "Finished: %s\n", e.FinishedAt.Format("2006-01-02 15:04:05 MST"))
	}
	if e.Message != "" {
		fmt.Fprintf(&b, "\n%s\n", e.Message)
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, "\n%s\n", truncateBody(e.Detail, 1500))
	}
	return Event{
		Title:    title,
		Body:     b.String(),
		Severity: SeverityFailure,
		Tags:     []string{strings.ToLower(e.Kind), e.Namespace},
	}
}

func truncateBody(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n… (truncated — full detail in the GUI)"
}

// RestoreOutcomeEvent formats a GUI-orchestrated restore reaching a terminal
// step (done or failed). Returns ok=false for non-terminal states.
func RestoreOutcomeEvent(st restore.State) (Event, bool) {
	target := fmt.Sprintf("%s/%s", st.PVCNamespace, st.PVCName)
	if st.Kind == "recovery" {
		target = fmt.Sprintf("%s (SQL recovery via %s)", st.PVCNamespace, st.DBPod)
	}
	switch st.Step {
	case restore.StepDone:
		body := fmt.Sprintf("Restore of PVC %s completed successfully.\n", target)
		if st.SnapshotID != "" {
			body += fmt.Sprintf("Snapshot: %s\n", shortID(st.SnapshotID))
		}
		return Event{
			Title:    "Restore complete: " + target,
			Body:     body,
			Severity: SeveritySuccess,
			Tags:     []string{"restore", st.PVCNamespace},
		}, true
	case restore.StepFailed:
		if st.Cancelled {
			var b strings.Builder
			fmt.Fprintf(&b, "Restore of PVC %s was cancelled by the operator.\n", target)
			if st.SnapshotID != "" {
				fmt.Fprintf(&b, "Snapshot: %s\n", shortID(st.SnapshotID))
			}
			b.WriteString("The volume may contain a partial restore.\n")
			if st.ArgoPausedGlobally && (st.ArgoSyncResumed == nil || !*st.ArgoSyncResumed) {
				b.WriteString("\nATTENTION: Argo CD sync is still PAUSED and needs manual resume.\n")
			}
			return Event{
				Title:    "Restore cancelled: " + target,
				Body:     b.String(),
				Severity: SeverityInfo,
				Tags:     []string{"restore", st.PVCNamespace},
			}, true
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Restore of PVC %s FAILED.\n", target)
		if st.SnapshotID != "" {
			fmt.Fprintf(&b, "Snapshot: %s\n", shortID(st.SnapshotID))
		}
		if st.LastError != "" {
			fmt.Fprintf(&b, "Error: %s\n", st.LastError)
		}
		if st.ArgoPausedGlobally && (st.ArgoSyncResumed == nil || !*st.ArgoSyncResumed) {
			b.WriteString("\nATTENTION: Argo CD sync is still PAUSED and needs manual resume.\n")
		}
		return Event{
			Title:    "Restore FAILED: " + target,
			Body:     b.String(),
			Severity: SeverityFailure,
			Tags:     []string{"restore", st.PVCNamespace},
		}, true
	default:
		return Event{}, false
	}
}

// InterruptedRestoresEvent reports restores found interrupted at startup with
// Argo sync possibly still paused.
func InterruptedRestoresEvent(states []restore.State) (Event, bool) {
	if len(states) == 0 {
		return Event{}, false
	}
	var b strings.Builder
	b.WriteString("The backend restarted and found restore(s) interrupted mid-flight — Argo CD sync may still be paused:\n\n")
	for _, st := range states {
		fmt.Fprintf(&b, "- %s/%s (restore %s, step %s)\n", st.PVCNamespace, st.PVCName, st.RestoreID, st.Step)
	}
	b.WriteString("\nReview them in the GUI (Restores page) and resume Argo sync if needed.")
	return Event{
		Title:    fmt.Sprintf("%d interrupted restore(s) need attention", len(states)),
		Body:     b.String(),
		Severity: SeverityFailure,
		Tags:     []string{"restore", "interrupted"},
	}, true
}

// LabOutcomeEvent formats a Restore Lab run reaching a state worth alerting
// on: ready (the drill's success evidence), failed provisioning, or a
// teardown that left resources behind. Returns ok=false otherwise.
func LabOutcomeEvent(st lab.State) (Event, bool) {
	target := st.SourceNamespace
	if st.AppName != "" {
		target = st.AppName
	}
	switch st.Step {
	case lab.StepReady:
		var b strings.Builder
		fmt.Fprintf(&b, "Restore lab for %s is ready (%s tier).\n", target, st.Tier)
		if st.HealthyAt != nil {
			fmt.Fprintf(&b, "Git deploy reached Healthy in %s.\n", st.HealthyAt.Sub(st.StartedAt).Round(time.Second))
		}
		if len(st.PVCs) > 0 {
			names := make([]string, 0, len(st.PVCs))
			for _, p := range st.PVCs {
				names = append(names, p.PVCName)
			}
			fmt.Fprintf(&b, "Restored PVCs: %s\n", strings.Join(names, ", "))
		}
		if st.DumpSnapshot != "" {
			b.WriteString("SQL dump replayed into the lab database.\n")
		}
		if st.ExpiresAt != nil {
			fmt.Fprintf(&b, "Auto-teardown at %s.\n", st.ExpiresAt.Format("2006-01-02 15:04 MST"))
		}
		return Event{
			Title:    "Restore lab ready: " + target,
			Body:     b.String(),
			Severity: SeveritySuccess,
			Tags:     []string{"lab", st.SourceNamespace},
		}, true
	case lab.StepFailed:
		title := "Restore lab FAILED: " + target
		if strings.Contains(st.LastError, "teardown") {
			title = "Restore lab teardown incomplete: " + target
		}
		body := fmt.Sprintf("Lab %s (%s tier) for %s failed.\nError: %s\n", st.LabID, st.Tier, st.SourceNamespace, st.LastError)
		return Event{
			Title:    title,
			Body:     body,
			Severity: SeverityFailure,
			Tags:     []string{"lab", st.SourceNamespace},
		}, true
	default:
		return Event{}, false
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
