package k8s

import (
        "bufio"
        "context"
        "fmt"
        "io"
        "strings"
        "time"

        corev1 "k8s.io/api/core/v1"
        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
        "k8s.io/apimachinery/pkg/watch"
)

// FollowRestoreJobLogs waits for the K8up restore Job pod and streams its logs.
// restoreCRName is the Restore CR name (job is typically restore-<crName>).
func (c *Clients) FollowRestoreJobLogs(ctx context.Context, namespace, restoreCRName string, onLine func(line string)) error {
        jobName := "restore-" + restoreCRName
        // Wait for a pod for this job.
        podName, err := c.waitForJobPod(ctx, namespace, jobName)
        if err != nil {
                return err
        }
        onLine(fmt.Sprintf("··· attached to pod %s/%s", namespace, podName))

        // Follow current stream; if pod restarts, loop a few times.
        for attempt := 0; attempt < 8; attempt++ {
                if err := ctx.Err(); err != nil {
                        return err
                }
                err := c.streamPodLogs(ctx, namespace, podName, onLine)
                if err == nil || ctx.Err() != nil {
                        return err
                }
                // Pod may have restarted — find latest pod for job again.
                onLine(fmt.Sprintf("··· log stream ended (%v); looking for pod again…", err))
                time.Sleep(2 * time.Second)
                next, werr := c.waitForJobPod(ctx, namespace, jobName)
                if werr != nil {
                        return fmt.Errorf("log follow: %w (after stream: %v)", werr, err)
                }
                podName = next
                onLine(fmt.Sprintf("··· reattached to pod %s/%s", namespace, podName))
        }
        return fmt.Errorf("log follow: too many pod restarts")
}

func (c *Clients) waitForJobPod(ctx context.Context, namespace, jobName string) (string, error) {
        selector := "job-name=" + jobName
        // Try list first
        if name, ok := c.findJobPod(ctx, namespace, selector); ok {
                return name, nil
        }
        w, err := c.Typed.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
                LabelSelector: selector,
        })
        if err != nil {
                // Fallback: poll without watch
                deadline := time.Now().Add(5 * time.Minute)
                for time.Now().Before(deadline) {
                        if err := ctx.Err(); err != nil {
                                return "", err
                        }
                        if name, ok := c.findJobPod(ctx, namespace, selector); ok {
                                return name, nil
                        }
                        time.Sleep(2 * time.Second)
                }
                return "", fmt.Errorf("timeout waiting for job pod %s", jobName)
        }
        defer w.Stop()
        timer := time.NewTimer(5 * time.Minute)
        defer timer.Stop()
        for {
                select {
                case <-ctx.Done():
                        return "", ctx.Err()
                case <-timer.C:
                        return "", fmt.Errorf("timeout waiting for job pod %s", jobName)
                case ev, ok := <-w.ResultChan():
                        if !ok {
                                return "", fmt.Errorf("pod watch closed for job %s", jobName)
                        }
                        if ev.Type == watch.Error {
                                continue
                        }
                        pod, ok := ev.Object.(*corev1.Pod)
                        if !ok || pod == nil {
                                continue
                        }
                        if pod.DeletionTimestamp != nil {
                                continue
                        }
                        // Prefer Running; accept Pending with name so we can try logs soon.
                        if pod.Name != "" {
                                // Wait until container exists
                                if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
                                        return pod.Name, nil
                                }
                                if len(pod.Status.ContainerStatuses) > 0 || pod.Status.Phase == corev1.PodPending {
                                        // brief wait then return name — streamPodLogs will retry
                                        return pod.Name, nil
                                }
                        }
                }
        }
}

func (c *Clients) findJobPod(ctx context.Context, namespace, selector string) (string, bool) {
        list, err := c.Typed.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
        if err != nil || len(list.Items) == 0 {
                return "", false
        }
        // Prefer newest Running
        var best *corev1.Pod
        for i := range list.Items {
                p := &list.Items[i]
                if p.DeletionTimestamp != nil {
                        continue
                }
                if best == nil || p.CreationTimestamp.After(best.CreationTimestamp.Time) {
                        best = p
                }
        }
        if best == nil {
                return "", false
        }
        return best.Name, true
}

func (c *Clients) streamPodLogs(ctx context.Context, namespace, podName string, onLine func(string)) error {
        // Wait until pod is running or terminal (up to 2m)
        deadline := time.Now().Add(2 * time.Minute)
        for time.Now().Before(deadline) {
                if err := ctx.Err(); err != nil {
                        return err
                }
                pod, err := c.Typed.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
                if err != nil {
                        return err
                }
                switch pod.Status.Phase {
                case corev1.PodRunning, corev1.PodSucceeded, corev1.PodFailed:
                        goto stream
                }
                time.Sleep(time.Second)
        }
stream:
        opts := &corev1.PodLogOptions{
                Follow:     true,
                Timestamps: false,
                // Tail a little history when attaching mid-run
                TailLines: int64Ptr(50),
        }
        req := c.Typed.CoreV1().Pods(namespace).GetLogs(podName, opts)
        stream, err := req.Stream(ctx)
        if err != nil {
                // Try without follow once (completed pod)
                opts.Follow = false
                req = c.Typed.CoreV1().Pods(namespace).GetLogs(podName, opts)
                stream, err = req.Stream(ctx)
                if err != nil {
                        return err
                }
        }
        defer stream.Close()

        sc := bufio.NewScanner(stream)
        buf := make([]byte, 0, 64*1024)
        sc.Buffer(buf, 1024*1024)
        for sc.Scan() {
                line := sc.Text()
                // strip very noisy empty
                if strings.TrimSpace(line) == "" {
                        continue
                }
                onLine(line)
                if err := ctx.Err(); err != nil {
                        return err
                }
        }
        if err := sc.Err(); err != nil && err != io.EOF {
                return err
        }
        return nil
}

func int64Ptr(n int64) *int64 { return &n }
