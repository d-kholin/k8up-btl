package k8s

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	failureLogTailLines = 40
	failureDetailMax    = 4000
)

// CaptureJobFailureDetail collects pod-level context for a failed K8up job CR
// (Backup/Check/Prune/Restore): the batch Job's failure condition, container
// termination reasons/exit codes, and a log tail from the job's pod. K8up's CR
// conditions only carry a generic "job failed" message — the restic error
// itself is in the pod. Best-effort: returns "" when nothing can be gathered
// (e.g. K8up already garbage-collected the Job and its pods).
func (c *Clients) CaptureJobFailureDetail(ctx context.Context, kind, namespace, crName string, crUID types.UID) string {
	var b strings.Builder
	job := c.findOwnedJob(ctx, namespace, crUID)
	// Fall back to K8up's job naming convention (<kind>-<crName>) when the
	// owner lookup finds nothing.
	jobName := strings.ToLower(kind) + "-" + crName
	if job != nil {
		jobName = job.Name
		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				fmt.Fprintf(&b, "job %s: %s", job.Name, cond.Reason)
				if cond.Message != "" {
					fmt.Fprintf(&b, " — %s", cond.Message)
				}
				b.WriteString("\n")
			}
		}
	}

	pods, err := c.Typed.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + jobName})
	if err != nil || len(pods.Items) == 0 {
		return truncateDetail(b.String())
	}
	sort.Slice(pods.Items, func(i, j int) bool {
		return pods.Items[i].CreationTimestamp.After(pods.Items[j].CreationTimestamp.Time)
	})
	pod := &pods.Items[0]

	fmt.Fprintf(&b, "pod %s: %s\n", pod.Name, string(pod.Status.Phase))
	for _, cs := range pod.Status.ContainerStatuses {
		term := cs.State.Terminated
		if term == nil {
			term = cs.LastTerminationState.Terminated
		}
		if term != nil && term.ExitCode != 0 {
			fmt.Fprintf(&b, "container %s: %s (exit %d)", cs.Name, term.Reason, term.ExitCode)
			if msg := strings.TrimSpace(term.Message); msg != "" {
				fmt.Fprintf(&b, " — %s", msg)
			}
			b.WriteString("\n")
		}
	}

	if tail := c.podLogTail(ctx, namespace, pod.Name); tail != "" {
		fmt.Fprintf(&b, "--- last log lines ---\n%s\n", tail)
	}
	return truncateDetail(b.String())
}

func (c *Clients) findOwnedJob(ctx context.Context, namespace string, crUID types.UID) *batchv1.Job {
	if crUID == "" {
		return nil
	}
	list, err := c.Typed.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	for i := range list.Items {
		for _, ref := range list.Items[i].OwnerReferences {
			if ref.UID == crUID {
				return &list.Items[i]
			}
		}
	}
	return nil
}

func (c *Clients) podLogTail(ctx context.Context, namespace, podName string) string {
	tail := int64(failureLogTailLines)
	// The failed container is usually the pod's current one; Previous covers a
	// restarted-then-failed container.
	for _, previous := range []bool{false, true} {
		req := c.Typed.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{TailLines: &tail, Previous: previous})
		stream, err := req.Stream(ctx)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(stream, failureDetailMax))
		_ = stream.Close()
		if err == nil && strings.TrimSpace(string(data)) != "" {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

func truncateDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > failureDetailMax {
		return s[:failureDetailMax] + "\n… (truncated)"
	}
	return s
}
