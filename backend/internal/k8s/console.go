package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// JobConsoleEvent is one message on a live job console stream.
type JobConsoleEvent struct {
	// Type: "log" (pod log line), "note" (console meta/status line) or
	// "done" (terminal — Status carries the outcome).
	Type string `json:"type"`
	Line string `json:"line,omitempty"`
	// Status on "done": "succeeded", "failed" or "gone" (CR was deleted or
	// garbage-collected before a pod ever ran).
	Status string `json:"status,omitempty"`
}

// ConsoleGVRs maps the lowercase K8up job kind to its GroupVersionResource for
// the kinds the live console can attach to.
var ConsoleGVRs = map[string]schema.GroupVersionResource{
	"backup":  GVRBackup,
	"check":   GVRCheck,
	"prune":   GVRPrune,
	"restore": GVRRestore,
	"archive": GVRArchive,
}

const consoleMaxDuration = 30 * time.Minute

// StreamJobConsole follows the pod logs of a K8up job CR's batch Job and emits
// them as console events until the pod reaches a terminal phase, the CR
// disappears, or ctx is cancelled. While no pod exists (pod pending, or — the
// diagnostic case — pod creation rejected by admission) it emits "note" events
// describing the batch Job's state and its warning events instead of going
// silent. Blocks for the duration of the stream; emit is called from a single
// goroutine.
func (c *Clients) StreamJobConsole(ctx context.Context, kind, namespace, crName string, emit func(JobConsoleEvent)) {
	kind = strings.ToLower(kind)
	// K8up names the batch Job <kind>-<crName>; the owner-UID fallback below
	// corrects this if the convention ever changes.
	jobName := kind + "-" + crName
	checkedOwner := false

	var lastNote string
	note := func(line string) {
		if line == "" || line == lastNote {
			return
		}
		lastNote = line
		emit(JobConsoleEvent{Type: "note", Line: "··· " + line})
	}

	deadline := time.Now().Add(consoleMaxDuration)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		podName, ok := c.findJobPod(ctx, namespace, "job-name="+jobName)
		if !ok {
			job, jobErr := c.Typed.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
			if errors.IsNotFound(jobErr) && !checkedOwner {
				// Maybe K8up named the Job differently — find it by CR owner UID.
				checkedOwner = true
				if gvr, known := ConsoleGVRs[kind]; known {
					if cr, err := c.GetResource(ctx, gvr, namespace, crName); err == nil {
						if owned := c.findOwnedJob(ctx, namespace, cr.GetUID()); owned != nil {
							jobName = owned.Name
							continue
						}
					}
				}
			}
			if errors.IsNotFound(jobErr) {
				// No Job at all: is the CR still there?
				if gvr, known := ConsoleGVRs[kind]; known {
					if _, err := c.GetResource(ctx, gvr, namespace, crName); errors.IsNotFound(err) {
						emit(JobConsoleEvent{Type: "note", Line: fmt.Sprintf("··· %s %s/%s no longer exists (deleted or garbage-collected by K8up)", kind, namespace, crName)})
						emit(JobConsoleEvent{Type: "done", Status: "gone"})
						return
					}
				}
				note(fmt.Sprintf("no batch Job for %s %s/%s yet — waiting for K8up to create it…", kind, namespace, crName))
			} else if jobErr == nil {
				note(describeJobWithoutPod(job) + jobEventDetail(ctx, c, namespace, jobName))
			}
			if !sleepCtx(ctx, 2*time.Second) {
				return
			}
			continue
		}

		note(fmt.Sprintf("attached to pod %s/%s", namespace, podName))
		streamErr := c.streamPodLogs(ctx, namespace, podName, func(line string) {
			emit(JobConsoleEvent{Type: "log", Line: line})
		})
		if ctx.Err() != nil {
			return
		}
		pod, err := c.Typed.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err == nil {
			switch pod.Status.Phase {
			case corev1.PodSucceeded:
				emit(JobConsoleEvent{Type: "done", Status: "succeeded"})
				return
			case corev1.PodFailed:
				emit(JobConsoleEvent{Type: "done", Status: "failed"})
				return
			}
		}
		if streamErr != nil {
			note(fmt.Sprintf("log stream ended (%v); waiting for pod…", streamErr))
		} else {
			note("log stream ended; waiting for pod…")
		}
		if !sleepCtx(ctx, 2*time.Second) {
			return
		}
	}
	note(fmt.Sprintf("console detached after %s — reopen to reattach", consoleMaxDuration))
}

// describeJobWithoutPod summarizes a batch Job that has produced no pod — the
// signature of a pod rejected by admission or an unschedulable pod spec.
func describeJobWithoutPod(job *batchv1.Job) string {
	for _, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		if cond.Type == batchv1.JobFailed || cond.Type == batchv1.JobSuspended {
			msg := cond.Reason
			if cond.Message != "" {
				msg += " — " + cond.Message
			}
			return fmt.Sprintf("job %s: %s: %s", job.Name, strings.ToLower(string(cond.Type)), msg)
		}
	}
	return fmt.Sprintf("job %s exists but has no pod yet (active=%d succeeded=%d failed=%d)",
		job.Name, job.Status.Active, job.Status.Succeeded, job.Status.Failed)
}

// jobEventDetail appends the latest warning event on the Job (e.g. FailedCreate
// with the Pod Security admission error). Best-effort: returns "" when events
// cannot be read (older deployments without the events RBAC grant).
func jobEventDetail(ctx context.Context, c *Clients, namespace, jobName string) string {
	list, err := c.Typed.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.kind=Job,involvedObject.name=" + jobName,
	})
	if err != nil || len(list.Items) == 0 {
		return ""
	}
	events := list.Items
	sort.Slice(events, func(i, j int) bool {
		return eventTime(&events[i]).After(eventTime(&events[j]))
	})
	for i := range events {
		if events[i].Type == corev1.EventTypeWarning {
			return fmt.Sprintf(" | %s: %s", events[i].Reason, events[i].Message)
		}
	}
	return ""
}

func eventTime(e *corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	return e.CreationTimestamp.Time
}

// sleepCtx sleeps for d unless ctx ends first; reports whether ctx is still live.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
