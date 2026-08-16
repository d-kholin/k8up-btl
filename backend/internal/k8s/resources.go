package k8s

import (
        "context"
        "encoding/json"
        "fmt"
        "strings"

        corev1 "k8s.io/api/core/v1"
        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
        "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
        "k8s.io/apimachinery/pkg/runtime/schema"
        "k8s.io/apimachinery/pkg/types"
)

var (
        GVRSchedule = schema.GroupVersionResource{Group: "k8up.io", Version: "v1", Resource: "schedules"}
        GVRSnapshot = schema.GroupVersionResource{Group: "k8up.io", Version: "v1", Resource: "snapshots"}
        GVRBackup   = schema.GroupVersionResource{Group: "k8up.io", Version: "v1", Resource: "backups"}
        GVRRestore  = schema.GroupVersionResource{Group: "k8up.io", Version: "v1", Resource: "restores"}
        GVRCheck    = schema.GroupVersionResource{Group: "k8up.io", Version: "v1", Resource: "checks"}
        GVRPrune    = schema.GroupVersionResource{Group: "k8up.io", Version: "v1", Resource: "prunes"}
        GVRArchive  = schema.GroupVersionResource{Group: "k8up.io", Version: "v1", Resource: "archives"}
        GVRArgoApp  = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
)

const (
        // PausedStateAnnotation stores durable restore orchestration state on the Argo Application.
        PausedStateAnnotation = "restore-gui.local/paused-state"
        LabelInstance         = "app.kubernetes.io/instance"
        AnnTrackingID         = "argocd.argoproj.io/tracking-id"
)

type WorkloadRef struct {
        Kind      string `json:"kind"` // Deployment | StatefulSet
        Namespace string `json:"namespace"`
        Name      string `json:"name"`
}

type ArgoRef struct {
        Namespace string `json:"namespace"`
        Name      string `json:"name"`
}

// ResolvePVCOwner finds a Deployment or StatefulSet that mounts the PVC.
func (c *Clients) ResolvePVCOwner(ctx context.Context, pvcNS, pvcName string) (*WorkloadRef, error) {
        // Deployments
        deps, err := c.Typed.AppsV1().Deployments(pvcNS).List(ctx, metav1.ListOptions{})
        if err != nil {
                return nil, err
        }
        for i := range deps.Items {
                d := &deps.Items[i]
                if podMountsPVC(&d.Spec.Template.Spec, pvcName) {
                        return &WorkloadRef{Kind: "Deployment", Namespace: d.Namespace, Name: d.Name}, nil
                }
        }
        stss, err := c.Typed.AppsV1().StatefulSets(pvcNS).List(ctx, metav1.ListOptions{})
        if err != nil {
                return nil, err
        }
        for i := range stss.Items {
                s := &stss.Items[i]
                if podMountsPVC(&s.Spec.Template.Spec, pvcName) {
                        return &WorkloadRef{Kind: "StatefulSet", Namespace: s.Namespace, Name: s.Name}, nil
                }
        }
        return nil, fmt.Errorf("no Deployment/StatefulSet in %s mounts PVC %s", pvcNS, pvcName)
}

func podMountsPVC(spec *corev1.PodSpec, pvcName string) bool {
        for _, v := range spec.Volumes {
                if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == pvcName {
                        return true
                }
        }
        return false
}

// ResolveArgoApp inspects workload labels/annotations for Argo ownership.
func (c *Clients) ResolveArgoApp(ctx context.Context, wl *WorkloadRef, argoNS string) (*ArgoRef, map[string]any, error) {
        labels, anns, err := c.workloadMeta(ctx, wl)
        if err != nil {
                return nil, nil, err
        }
        appName := labels[LabelInstance]
        if appName == "" {
                if tid := anns[AnnTrackingID]; tid != "" {
                        // format: <app>:<group>/<kind>:<ns>/<name> or similar
                        appName = strings.SplitN(tid, ":", 2)[0]
                }
        }
        if appName == "" {
                return nil, nil, nil
        }
        obj, err := c.Dynamic.Resource(GVRArgoApp).Namespace(argoNS).Get(ctx, appName, metav1.GetOptions{})
        if err != nil {
                // try cluster-scoped search by name across argoNS only first; if missing, still return ref with nil policy
                return &ArgoRef{Namespace: argoNS, Name: appName}, nil, fmt.Errorf("argo application %s/%s: %w", argoNS, appName, err)
        }
        policy, _, _ := unstructured.NestedMap(obj.Object, "spec", "syncPolicy")
        return &ArgoRef{Namespace: obj.GetNamespace(), Name: obj.GetName()}, policy, nil
}

func (c *Clients) workloadMeta(ctx context.Context, wl *WorkloadRef) (map[string]string, map[string]string, error) {
        switch wl.Kind {
        case "Deployment":
                d, err := c.Typed.AppsV1().Deployments(wl.Namespace).Get(ctx, wl.Name, metav1.GetOptions{})
                if err != nil {
                        return nil, nil, err
                }
                return d.Labels, d.Annotations, nil
        case "StatefulSet":
                s, err := c.Typed.AppsV1().StatefulSets(wl.Namespace).Get(ctx, wl.Name, metav1.GetOptions{})
                if err != nil {
                        return nil, nil, err
                }
                return s.Labels, s.Annotations, nil
        default:
                return nil, nil, fmt.Errorf("unsupported kind %s", wl.Kind)
        }
}

func (c *Clients) GetReplicas(ctx context.Context, wl *WorkloadRef) (int32, error) {
        switch wl.Kind {
        case "Deployment":
                d, err := c.Typed.AppsV1().Deployments(wl.Namespace).Get(ctx, wl.Name, metav1.GetOptions{})
                if err != nil {
                        return 0, err
                }
                if d.Spec.Replicas == nil {
                        return 1, nil
                }
                return *d.Spec.Replicas, nil
        case "StatefulSet":
                s, err := c.Typed.AppsV1().StatefulSets(wl.Namespace).Get(ctx, wl.Name, metav1.GetOptions{})
                if err != nil {
                        return 0, err
                }
                if s.Spec.Replicas == nil {
                        return 1, nil
                }
                return *s.Spec.Replicas, nil
        default:
                return 0, fmt.Errorf("unsupported kind %s", wl.Kind)
        }
}

func (c *Clients) Scale(ctx context.Context, wl *WorkloadRef, replicas int32) error {
        patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas))
        switch wl.Kind {
        case "Deployment":
                _, err := c.Typed.AppsV1().Deployments(wl.Namespace).Patch(ctx, wl.Name, types.MergePatchType, patch, metav1.PatchOptions{})
                return err
        case "StatefulSet":
                _, err := c.Typed.AppsV1().StatefulSets(wl.Namespace).Patch(ctx, wl.Name, types.MergePatchType, patch, metav1.PatchOptions{})
                return err
        default:
                return fmt.Errorf("unsupported kind %s", wl.Kind)
        }
}

// WaitPodsGone waits until no pods selected by the workload still exist (PVC can unmount).
func (c *Clients) WaitPodsGone(ctx context.Context, wl *WorkloadRef) error {
        var selector string
        switch wl.Kind {
        case "Deployment":
                d, err := c.Typed.AppsV1().Deployments(wl.Namespace).Get(ctx, wl.Name, metav1.GetOptions{})
                if err != nil {
                        return err
                }
                selector = metav1.FormatLabelSelector(d.Spec.Selector)
        case "StatefulSet":
                s, err := c.Typed.AppsV1().StatefulSets(wl.Namespace).Get(ctx, wl.Name, metav1.GetOptions{})
                if err != nil {
                        return err
                }
                selector = metav1.FormatLabelSelector(s.Spec.Selector)
        default:
                return fmt.Errorf("unsupported kind %s", wl.Kind)
        }
        for {
                if err := ctx.Err(); err != nil {
                        return err
                }
                pods, err := c.Typed.CoreV1().Pods(wl.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
                if err != nil {
                        return err
                }
                if len(pods.Items) == 0 {
                        return nil
                }
                // simple backoff via context-friendly sleep
                t := sleepCh(ctx, 2e9) // 2s
                <-t
        }
}

func sleepCh(ctx context.Context, ns int64) <-chan struct{} {
        ch := make(chan struct{})
        go func() {
                timer := newTimer(ns)
                defer timer.Stop()
                select {
                case <-ctx.Done():
                case <-timer.C:
                }
                close(ch)
        }()
        return ch
}

// PauseArgoSync removes spec.syncPolicy.automated and writes durable state annotation.
// originalPolicy is a deep copy of the prior full syncPolicy (including automated).
// Caller should verify the pause stuck (root app-of-apps can re-heal automated unless
// ignoreDifferences is configured on root).
func (c *Clients) PauseArgoSync(ctx context.Context, ref *ArgoRef, stateJSON string) (originalPolicy map[string]any, err error) {
        app, err := c.Dynamic.Resource(GVRArgoApp).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
        if err != nil {
                return nil, err
        }
        policy, found, _ := unstructured.NestedMap(app.Object, "spec", "syncPolicy")
        if found && policy != nil {
                // Deep copy BEFORE mutating — NestedMap is already a deep copy; keep for resume.
                originalPolicy = deepCopyMap(policy)
                delete(policy, "automated")
                if err := unstructured.SetNestedMap(app.Object, policy, "spec", "syncPolicy"); err != nil {
                        return nil, err
                }
        }
        anns := app.GetAnnotations()
        if anns == nil {
                anns = map[string]string{}
        }
        anns[PausedStateAnnotation] = stateJSON
        app.SetAnnotations(anns)

        _, err = c.Dynamic.Resource(GVRArgoApp).Namespace(ref.Namespace).Update(ctx, app, metav1.UpdateOptions{})
        return originalPolicy, err
}

// ArgoAutomatedEnabled reports whether Application has syncPolicy.automated set.
func (c *Clients) ArgoAutomatedEnabled(ctx context.Context, ref *ArgoRef) (bool, error) {
        app, err := c.Dynamic.Resource(GVRArgoApp).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
        if err != nil {
                return false, err
        }
        auto, found, _ := unstructured.NestedMap(app.Object, "spec", "syncPolicy", "automated")
        return found && auto != nil, nil
}

// ClearArgoAutomated removes syncPolicy.automated without touching other fields/annotations.
func (c *Clients) ClearArgoAutomated(ctx context.Context, ref *ArgoRef) error {
        app, err := c.Dynamic.Resource(GVRArgoApp).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
        if err != nil {
                return err
        }
        policy, found, _ := unstructured.NestedMap(app.Object, "spec", "syncPolicy")
        if !found || policy == nil {
                return nil
        }
        if _, has := policy["automated"]; !has {
                return nil
        }
        delete(policy, "automated")
        if err := unstructured.SetNestedMap(app.Object, policy, "spec", "syncPolicy"); err != nil {
                return err
        }
        _, err = c.Dynamic.Resource(GVRArgoApp).Namespace(ref.Namespace).Update(ctx, app, metav1.UpdateOptions{})
        return err
}

func deepCopyMap(in map[string]any) map[string]any {
        if in == nil {
                return nil
        }
        b, err := json.Marshal(in)
        if err != nil {
                out := map[string]any{}
                for k, v := range in {
                        out[k] = v
                }
                return out
        }
        var out map[string]any
        if err := json.Unmarshal(b, &out); err != nil {
                return map[string]any{}
        }
        return out
}

// ResumeArgoSync restores automated policy and clears annotation.
func (c *Clients) ResumeArgoSync(ctx context.Context, ref *ArgoRef, originalPolicy map[string]any) error {
        app, err := c.Dynamic.Resource(GVRArgoApp).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
        if err != nil {
                return err
        }
        if originalPolicy == nil {
                // clear entire syncPolicy automated by ensuring map without automated
                cur, found, _ := unstructured.NestedMap(app.Object, "spec", "syncPolicy")
                if found && cur != nil {
                        delete(cur, "automated")
                        _ = unstructured.SetNestedMap(app.Object, cur, "spec", "syncPolicy")
                }
        } else {
                _ = unstructured.SetNestedMap(app.Object, originalPolicy, "spec", "syncPolicy")
        }
        anns := app.GetAnnotations()
        if anns != nil {
                delete(anns, PausedStateAnnotation)
                app.SetAnnotations(anns)
        }
        _, err = c.Dynamic.Resource(GVRArgoApp).Namespace(ref.Namespace).Update(ctx, app, metav1.UpdateOptions{})
        return err
}

// SetPausedStateAnnotation updates only the durable state blob.
func (c *Clients) SetPausedStateAnnotation(ctx context.Context, ref *ArgoRef, stateJSON string) error {
        app, err := c.Dynamic.Resource(GVRArgoApp).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
        if err != nil {
                return err
        }
        anns := app.GetAnnotations()
        if anns == nil {
                anns = map[string]string{}
        }
        if stateJSON == "" {
                delete(anns, PausedStateAnnotation)
        } else {
                anns[PausedStateAnnotation] = stateJSON
        }
        app.SetAnnotations(anns)
        _, err = c.Dynamic.Resource(GVRArgoApp).Namespace(ref.Namespace).Update(ctx, app, metav1.UpdateOptions{})
        return err
}

// ListInterruptedRestores finds Applications with our pause annotation.
func (c *Clients) ListInterruptedRestores(ctx context.Context, argoNS string) ([]unstructured.Unstructured, error) {
        list, err := c.Dynamic.Resource(GVRArgoApp).Namespace(argoNS).List(ctx, metav1.ListOptions{})
        if err != nil {
                return nil, err
        }
        var out []unstructured.Unstructured
        for i := range list.Items {
                item := list.Items[i]
                if _, ok := item.GetAnnotations()[PausedStateAnnotation]; ok {
                        out = append(out, item)
                }
        }
        return out, nil
}

// CreateRestoreCR creates a k8up Restore without Argo tracking labels.
func (c *Clients) CreateRestoreCR(ctx context.Context, namespace, name, snapshotID, pvcName string, backend map[string]any) (*unstructured.Unstructured, error) {
        spec := map[string]any{
                "snapshot": snapshotID,
                "restoreMethod": map[string]any{
                        "folder": map[string]any{
                                "claimName": pvcName,
                        },
                },
                // Homelab Schedules always use PodConfig "backup-pod".
                "podConfigRef": map[string]any{
                        "name": "backup-pod",
                },
        }
        if backend != nil {
                spec["backend"] = backend
        }
        obj := &unstructured.Unstructured{
                Object: map[string]any{
                        "apiVersion": "k8up.io/v1",
                        "kind":       "Restore",
                        "metadata": map[string]any{
                                "name":      name,
                                "namespace": namespace,
                                "labels": map[string]any{
                                        "app.kubernetes.io/managed-by": "k8up-gui",
                                        "restore-gui.local/one-shot":   "true",
                                },
                                // Explicitly no Argo tracking labels.
                        },
                        "spec": spec,
                },
        }
        return c.Dynamic.Resource(GVRRestore).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
}

// CreateSimpleJobCR creates Backup or Check CR (no Argo labels).
func (c *Clients) CreateSimpleJobCR(ctx context.Context, gvr schema.GroupVersionResource, kind, namespace, name string, spec map[string]any) (*unstructured.Unstructured, error) {
        obj := &unstructured.Unstructured{
                Object: map[string]any{
                        "apiVersion": "k8up.io/v1",
                        "kind":       kind,
                        "metadata": map[string]any{
                                "name":      name,
                                "namespace": namespace,
                                "labels": map[string]any{
                                        "app.kubernetes.io/managed-by": "k8up-gui",
                                        "restore-gui.local/one-shot":   "true",
                                },
                        },
                        "spec": spec,
                },
        }
        return c.Dynamic.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
}

func (c *Clients) ListResource(ctx context.Context, gvr schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error) {
        if namespace == "" {
                return c.Dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
        }
        return c.Dynamic.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
}

func (c *Clients) GetSecret(ctx context.Context, ns, name string) (*corev1.Secret, error) {
        return c.Typed.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
}

func (c *Clients) GetSnapshot(ctx context.Context, ns, name string) (*unstructured.Unstructured, error) {
        return c.Dynamic.Resource(GVRSnapshot).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
}

func (c *Clients) GetRestore(ctx context.Context, ns, name string) (*unstructured.Unstructured, error) {
        return c.Dynamic.Resource(GVRRestore).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
}
