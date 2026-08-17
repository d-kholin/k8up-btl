package k8s

import (
        "context"
        "encoding/json"
        "fmt"
        "strings"

        appsv1 "k8s.io/api/apps/v1"
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
        // volumeClaimTemplate PVCs (<template>-<sts>-<ordinal>) never appear in the
        // pod template's volumes; match them by naming convention.
        for i := range stss.Items {
                s := &stss.Items[i]
                if stsTemplateOwnsPVC(s, pvcName) {
                        return &WorkloadRef{Kind: "StatefulSet", Namespace: s.Namespace, Name: s.Name}, nil
                }
        }
        // Last resort: a live pod mounting the PVC, resolved through owner references.
        if wl, err := c.resolveOwnerViaPods(ctx, pvcNS, pvcName); err == nil && wl != nil {
                return wl, nil
        }
        return nil, fmt.Errorf("no Deployment/StatefulSet in %s mounts PVC %s", pvcNS, pvcName)
}

func stsTemplateOwnsPVC(s *appsv1.StatefulSet, pvcName string) bool {
        for _, t := range s.Spec.VolumeClaimTemplates {
                prefix := t.Name + "-" + s.Name + "-"
                if strings.HasPrefix(pvcName, prefix) && isOrdinal(strings.TrimPrefix(pvcName, prefix)) {
                        return true
                }
        }
        return false
}

func isOrdinal(s string) bool {
        if s == "" {
                return false
        }
        for _, r := range s {
                if r < '0' || r > '9' {
                        return false
                }
        }
        return true
}

func (c *Clients) resolveOwnerViaPods(ctx context.Context, pvcNS, pvcName string) (*WorkloadRef, error) {
        pods, err := c.Typed.CoreV1().Pods(pvcNS).List(ctx, metav1.ListOptions{})
        if err != nil {
                return nil, err
        }
        for i := range pods.Items {
                p := &pods.Items[i]
                if !podMountsPVC(&p.Spec, pvcName) {
                        continue
                }
                for _, ref := range p.OwnerReferences {
                        switch ref.Kind {
                        case "StatefulSet":
                                return &WorkloadRef{Kind: "StatefulSet", Namespace: pvcNS, Name: ref.Name}, nil
                        case "ReplicaSet":
                                rs, err := c.Typed.AppsV1().ReplicaSets(pvcNS).Get(ctx, ref.Name, metav1.GetOptions{})
                                if err != nil {
                                        continue
                                }
                                for _, rref := range rs.OwnerReferences {
                                        if rref.Kind == "Deployment" {
                                                return &WorkloadRef{Kind: "Deployment", Namespace: pvcNS, Name: rref.Name}, nil
                                        }
                                }
                        }
                }
        }
        return nil, nil
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
// backend should include at least s3.bucket (e.g. naku-k8up/<ns>) when using global S3 endpoint.
func (c *Clients) CreateRestoreCR(ctx context.Context, namespace, name, snapshotID, pvcName string, backend map[string]any) (*unstructured.Unstructured, error) {
        if backend == nil {
                var err error
                backend, err = c.ResolveRestoreBackend(ctx, namespace, snapshotID)
                if err != nil {
                        return nil, err
                }
        }
        spec := map[string]any{
                "snapshot": snapshotID,
                "restoreMethod": map[string]any{
                        "folder": map[string]any{
                                "claimName": pvcName,
                        },
                },
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
                                        "app.kubernetes.io/managed-by": "k8up-btl",
                                        "restore-gui.local/one-shot":   "true",
                                },
                        },
                        "spec": spec,
                },
        }
        return c.Dynamic.Resource(GVRRestore).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
}

// ResolveRestoreBackend builds a K8up backend from the namespace Schedule or Snapshot repository URI.
func (c *Clients) ResolveRestoreBackend(ctx context.Context, namespace, snapshotHint string) (map[string]any, error) {
        // Prefer Schedule.spec.backend in the same namespace.
        list, err := c.Dynamic.Resource(GVRSchedule).Namespace(namespace).List(ctx, metav1.ListOptions{})
        if err == nil {
                for i := range list.Items {
                        if be, found, _ := unstructured.NestedMap(list.Items[i].Object, "spec", "backend"); found && be != nil {
                                return be, nil
                        }
                }
        }
        // Fallback: parse Snapshot.spec.repository s3:https://host/bucket
        snaps, err := c.Dynamic.Resource(GVRSnapshot).Namespace(namespace).List(ctx, metav1.ListOptions{})
        if err != nil {
                return nil, fmt.Errorf("resolve restore backend: no schedule backend and list snapshots: %w", err)
        }
        for i := range snaps.Items {
                repo, _, _ := unstructured.NestedString(snaps.Items[i].Object, "spec", "repository")
                if repo == "" {
                        continue
                }
                if bucket := s3BucketFromRepo(repo); bucket != "" {
                        return map[string]any{
                                "s3": map[string]any{
                                        "bucket": bucket,
                                },
                        }, nil
                }
        }
        return nil, fmt.Errorf("could not resolve s3 bucket for namespace %s (set Schedule.spec.backend or Snapshot.spec.repository)", namespace)
}

// s3BucketFromRepo parses restic S3 URIs like s3:https://garage.example/naku-k8up/ns
func s3BucketFromRepo(repo string) string {
        // s3:https://host/path or s3:http://host/path
        const pfx = "s3:"
        if !strings.HasPrefix(repo, pfx) {
                return ""
        }
        rest := strings.TrimPrefix(repo, pfx)
        // strip scheme
        for _, sch := range []string{"https://", "http://"} {
                if strings.HasPrefix(rest, sch) {
                        rest = strings.TrimPrefix(rest, sch)
                        break
                }
        }
        // host/bucket...
        _, path, ok := strings.Cut(rest, "/")
        if !ok || path == "" {
                return ""
        }
        return strings.TrimSuffix(path, "/")
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
                                        "app.kubernetes.io/managed-by": "k8up-btl",
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

// PVCRef is a light PVC reference for coverage reporting.
type PVCRef struct {
        Namespace string `json:"namespace"`
        Name      string `json:"name"`
}

// ListPVCRefs returns all PVCs cluster-wide (used to spot namespaces with
// volumes but no backup Schedule).
func (c *Clients) ListPVCRefs(ctx context.Context) ([]PVCRef, error) {
        list, err := c.Typed.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
        if err != nil {
                return nil, err
        }
        out := make([]PVCRef, 0, len(list.Items))
        for i := range list.Items {
                out = append(out, PVCRef{Namespace: list.Items[i].Namespace, Name: list.Items[i].Name})
        }
        return out, nil
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

// GetResource fetches any namespaced CR (used to poll K8up job status).
func (c *Clients) GetResource(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
        return c.Dynamic.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
}

// DeleteRestore removes a Restore CR; its Job is cascade-deleted via owner
// references (used when an operator cancels an in-flight restore).
func (c *Clients) DeleteRestore(ctx context.Context, ns, name string) error {
        fg := metav1.DeletePropagationForeground
        return c.Dynamic.Resource(GVRRestore).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &fg})
}
