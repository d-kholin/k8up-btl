package k8s

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Annotations driving SQL dump recovery. Backup side is K8up's; restore side
// is ours and must live in git (workload pod template) — the GUI never accepts
// a free-text command.
const (
	AnnBackupCommand   = "k8up.io/backupcommand"
	AnnBackupContainer = "k8up.io/backupcontainer"
	// AnnRestoreCommand is the inverse of k8up.io/backupcommand: run in the
	// same container via `sh -c`, receiving the dump on stdin
	// (e.g. `psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"`).
	AnnRestoreCommand = "k8up-btl.local/restore-command"
	// AnnQuiesceWorkloads overrides the app-instance-label grouping used to
	// decide which workloads to stop during recovery. Comma-separated
	// `kind/name` entries, e.g. `deployment/mealie,statefulset/mealie-worker`.
	AnnQuiesceWorkloads = "k8up-btl.local/quiesce-workloads"
)

// DBPod is a pod that K8up takes application-level (stdin) dumps from.
type DBPod struct {
	Namespace      string       `json:"namespace"`
	PodName        string       `json:"podName"`
	Container      string       `json:"container"`
	BackupCommand  string       `json:"backupCommand"`
	RestoreCommand string       `json:"restoreCommand,omitempty"`
	QuiesceRaw     string       `json:"-"`
	Instance       string       `json:"instance,omitempty"`
	Workload       *WorkloadRef `json:"workload,omitempty"`
}

// ListBackupCommandPods returns running pods in the namespace annotated with a
// K8up backup command — the candidates a dump snapshot could have come from.
func (c *Clients) ListBackupCommandPods(ctx context.Context, ns string) ([]DBPod, error) {
	pods, err := c.Typed.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []DBPod
	for i := range pods.Items {
		p := &pods.Items[i]
		cmd := p.Annotations[AnnBackupCommand]
		if cmd == "" || p.Status.Phase != corev1.PodRunning {
			continue
		}
		container := p.Annotations[AnnBackupContainer]
		if container == "" && len(p.Spec.Containers) > 0 {
			container = p.Spec.Containers[0].Name
		}
		dp := DBPod{
			Namespace:      ns,
			PodName:        p.Name,
			Container:      container,
			BackupCommand:  cmd,
			RestoreCommand: p.Annotations[AnnRestoreCommand],
			QuiesceRaw:     p.Annotations[AnnQuiesceWorkloads],
			Instance:       p.Labels["app.kubernetes.io/instance"],
		}
		if wl, err := c.workloadFromPod(ctx, p); err == nil && wl != nil {
			dp.Workload = wl
		}
		out = append(out, dp)
	}
	return out, nil
}

// GetBackupCommandPod fetches one annotated pod by name (re-validation at
// recovery start — the command is always re-read from the live pod).
func (c *Clients) GetBackupCommandPod(ctx context.Context, ns, podName string) (*DBPod, error) {
	pods, err := c.ListBackupCommandPods(ctx, ns)
	if err != nil {
		return nil, err
	}
	for i := range pods {
		if pods[i].PodName == podName {
			return &pods[i], nil
		}
	}
	return nil, fmt.Errorf("pod %s/%s is not running or has no %s annotation", ns, podName, AnnBackupCommand)
}

func (c *Clients) workloadFromPod(ctx context.Context, p *corev1.Pod) (*WorkloadRef, error) {
	for _, ref := range p.OwnerReferences {
		switch ref.Kind {
		case "StatefulSet":
			return &WorkloadRef{Kind: "StatefulSet", Namespace: p.Namespace, Name: ref.Name}, nil
		case "ReplicaSet":
			rs, err := c.Typed.AppsV1().ReplicaSets(p.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			for _, rref := range rs.OwnerReferences {
				if rref.Kind == "Deployment" {
					return &WorkloadRef{Kind: "Deployment", Namespace: p.Namespace, Name: rref.Name}, nil
				}
			}
		}
	}
	return nil, nil
}

// ScalableWorkload is a workload plus its current desired replica count.
type ScalableWorkload struct {
	WorkloadRef
	Replicas int32 `json:"replicas"`
}

// QuiesceSet computes the workloads to stop while recovering db's application:
// only that app — never the whole namespace (shared namespaces host unrelated
// apps). Grouping is the pod's app.kubernetes.io/instance label (the Argo app
// grouping), overridable via the k8up-btl.local/quiesce-workloads annotation.
// The DB's own workload is always excluded — it must stay up to receive the
// dump on stdin.
func (c *Clients) QuiesceSet(ctx context.Context, db *DBPod) ([]ScalableWorkload, error) {
	var out []ScalableWorkload
	seen := map[string]bool{}
	exclude := ""
	if db.Workload != nil {
		exclude = db.Workload.Kind + "/" + db.Workload.Name
	}
	add := func(w ScalableWorkload) {
		key := w.Kind + "/" + w.Name
		if key == exclude || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, w)
	}

	if raw := strings.TrimSpace(db.QuiesceRaw); raw != "" {
		// Explicit git-defined list.
		for _, entry := range strings.Split(raw, ",") {
			kind, name, ok := strings.Cut(strings.TrimSpace(entry), "/")
			if !ok || name == "" {
				return nil, fmt.Errorf("invalid %s entry %q (want kind/name)", AnnQuiesceWorkloads, entry)
			}
			w, err := c.scalableWorkload(ctx, db.Namespace, kind, name)
			if err != nil {
				return nil, err
			}
			add(*w)
		}
		return out, nil
	}

	if db.Instance == "" {
		// No grouping available; callers surface this so the operator can add
		// the annotation. PVC-restore owners still get stopped independently.
		return nil, nil
	}

	deps, err := c.Typed.AppsV1().Deployments(db.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range deps.Items {
		d := &deps.Items[i]
		if d.Labels["app.kubernetes.io/instance"] != db.Instance {
			continue
		}
		reps := int32(1)
		if d.Spec.Replicas != nil {
			reps = *d.Spec.Replicas
		}
		add(ScalableWorkload{WorkloadRef{Kind: "Deployment", Namespace: d.Namespace, Name: d.Name}, reps})
	}
	stss, err := c.Typed.AppsV1().StatefulSets(db.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range stss.Items {
		s := &stss.Items[i]
		if s.Labels["app.kubernetes.io/instance"] != db.Instance {
			continue
		}
		reps := int32(1)
		if s.Spec.Replicas != nil {
			reps = *s.Spec.Replicas
		}
		add(ScalableWorkload{WorkloadRef{Kind: "StatefulSet", Namespace: s.Namespace, Name: s.Name}, reps})
	}
	return out, nil
}

func (c *Clients) scalableWorkload(ctx context.Context, ns, kind, name string) (*ScalableWorkload, error) {
	switch strings.ToLower(kind) {
	case "deployment", "deploy":
		d, err := c.Typed.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		reps := int32(1)
		if d.Spec.Replicas != nil {
			reps = *d.Spec.Replicas
		}
		return &ScalableWorkload{WorkloadRef{Kind: "Deployment", Namespace: ns, Name: name}, reps}, nil
	case "statefulset", "sts":
		s, err := c.Typed.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		reps := int32(1)
		if s.Spec.Replicas != nil {
			reps = *s.Spec.Replicas
		}
		return &ScalableWorkload{WorkloadRef{Kind: "StatefulSet", Namespace: ns, Name: name}, reps}, nil
	default:
		return nil, fmt.Errorf("unsupported workload kind %q (want deployment or statefulset)", kind)
	}
}
