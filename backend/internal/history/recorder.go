package history

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/d-kholin/k8up-gui/internal/audit"
	"github.com/d-kholin/k8up-gui/internal/k8s"
)

// Recorder durably records K8up job CR outcomes into SQLite. K8up
// garbage-collects finished CRs per keepJobs, so the cluster alone cannot
// answer "what ran last month and did it succeed" — this poller can.
type Recorder struct {
	Clients  *k8s.Clients
	Store    *audit.Store
	Log      *slog.Logger
	Interval time.Duration
	// OnEvent fires when a job's recorded status changes (including its first
	// observation after startup). Used to push live updates over SSE.
	OnEvent func(audit.BackupEvent)
	// OnTransition is like OnEvent but suppressed during the first sweep, so a
	// backend restart never re-fires for jobs that finished long ago. Used for
	// notifications.
	OnTransition func(audit.BackupEvent)

	seen map[string]string // "<kind>/<uid>" → last observed status
	warm bool              // first sweep completed
}

var watchedKinds = []struct {
	kind string
	gvr  schema.GroupVersionResource
}{
	{"Backup", k8s.GVRBackup},
	{"Check", k8s.GVRCheck},
	{"Prune", k8s.GVRPrune},
	{"Restore", k8s.GVRRestore},
}

func (r *Recorder) Run(ctx context.Context) {
	if r.Interval <= 0 {
		r.Interval = time.Minute
	}
	r.sweep(ctx)
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.sweep(ctx)
		}
	}
}

func (r *Recorder) sweep(ctx context.Context) {
	if r.seen == nil {
		r.seen = map[string]string{}
	}
	for _, k := range watchedKinds {
		list, err := r.Clients.ListResource(ctx, k.gvr, "")
		if err != nil {
			r.Log.Warn("history sweep: list failed", "kind", k.kind, "err", err)
			continue
		}
		present := make([]string, 0, len(list.Items))
		presentKeys := make(map[string]struct{}, len(list.Items))
		for i := range list.Items {
			e := eventFromObject(k.kind, &list.Items[i])
			if e.UID == "" {
				continue
			}
			present = append(present, e.UID)
			seenKey := k.kind + "/" + e.UID
			presentKeys[seenKey] = struct{}{}
			if err := r.Store.UpsertBackupEvent(ctx, e); err != nil {
				r.Log.Warn("history sweep: upsert failed", "kind", k.kind, "name", e.Namespace+"/"+e.Name, "err", err)
				continue
			}
			if r.seen[seenKey] != e.Status {
				if r.OnEvent != nil {
					r.OnEvent(e)
				}
				if r.OnTransition != nil && r.warm {
					r.OnTransition(e)
				}
			}
			r.seen[seenKey] = e.Status
		}
		if n, err := r.Store.MarkVanishedRunning(ctx, k.kind, present); err != nil {
			r.Log.Warn("history sweep: mark vanished failed", "kind", k.kind, "err", err)
		} else if n > 0 {
			r.Log.Info("history sweep: running jobs vanished before completion", "kind", k.kind, "count", n)
		}
		// Drop cache entries for CRs K8up has garbage-collected.
		for key := range r.seen {
			if _, ok := presentKeys[key]; !ok && strings.HasPrefix(key, k.kind+"/") {
				delete(r.seen, key)
			}
		}
	}
	r.warm = true
}

func eventFromObject(kind string, obj *unstructured.Unstructured) audit.BackupEvent {
	e := audit.BackupEvent{
		UID:       string(obj.GetUID()),
		Kind:      kind,
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
		StartedAt: obj.GetCreationTimestamp().Time.UTC(),
	}
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind == "Schedule" {
			e.Schedule = ref.Name
			break
		}
	}
	e.Status, e.Message, e.FinishedAt = jobOutcome(obj.Object)
	return e
}

type condition struct {
	typ, status, reason, message, transition string
}

// jobOutcome mirrors restore.waitRestoreComplete's reading of K8up's shared
// status shape (status.finished + Completed/Failed/Progressing conditions).
func jobOutcome(obj map[string]any) (status, message string, finishedAt *time.Time) {
	raw, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	var completed, failed, progressing *condition
	for _, c := range raw {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		cc := condition{}
		cc.typ, _ = m["type"].(string)
		cc.status, _ = m["status"].(string)
		cc.reason, _ = m["reason"].(string)
		cc.message, _ = m["message"].(string)
		cc.transition, _ = m["lastTransitionTime"].(string)
		switch {
		case cc.typ == "Completed" && cc.status == "True":
			completed = &cc
		case cc.typ == "Failed" && cc.status == "True":
			failed = &cc
		case cc.typ == "Progressing":
			progressing = &cc
		}
	}

	if failed != nil {
		return "failed", failed.message, conditionTime(failed)
	}
	if completed != nil {
		if completed.reason == "Failed" || completed.reason == "Error" {
			return "failed", completed.message, conditionTime(completed)
		}
		return "succeeded", "", conditionTime(completed)
	}
	if progressing != nil && progressing.status == "False" && progressing.reason == "Finished" &&
		strings.Contains(strings.ToLower(progressing.message), "failed") {
		return "failed", progressing.message, conditionTime(progressing)
	}
	if finished, _, _ := unstructured.NestedBool(obj, "status", "finished"); finished {
		now := time.Now().UTC()
		return "succeeded", "", &now
	}
	return "running", "", nil
}

func conditionTime(c *condition) *time.Time {
	if c == nil || c.transition == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, c.transition); err == nil {
		u := t.UTC()
		return &u
	}
	return nil
}
