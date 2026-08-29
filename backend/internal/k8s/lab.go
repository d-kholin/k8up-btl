package k8s

import (
        "context"
        "fmt"
        "strings"
        "time"

        appsv1 "k8s.io/api/apps/v1"
        corev1 "k8s.io/api/core/v1"
        "k8s.io/apimachinery/pkg/api/resource"
        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
        "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Restore Lab helpers (docs/restore-lab.md). Everything here creates
// resources ONLY in the lab namespace or clone Applications in the Argo CD
// namespace — see AGENTS.md rule 9.

const (
        // AnnLabPath names an alternate kustomize path (per-app lab overlay) on
        // the source Application, e.g. "apps/mealie/lab".
        AnnLabPath = "k8up-btl.local/lab-path"
        // LabelLabID marks every resource the lab manager itself creates.
        LabelLabID = "k8up-btl.local/lab-id"
        // LabelManagedBy is the shared managed-by marker.
        LabelManagedBy = "app.kubernetes.io/managed-by"
        // ArgoResourcesFinalizer makes deleting an Application cascade to its
        // managed resources.
        ArgoResourcesFinalizer = "resources-finalizer.argocd.argoproj.io"
)

// FindApplicationForNamespace resolves the Argo Application deploying into ns.
// Clone Applications created by this tool are skipped. Errors when zero or
// more than one Application targets the namespace — the app lab needs an
// unambiguous git source.
func (c *Clients) FindApplicationForNamespace(ctx context.Context, argoNS, ns string) (*unstructured.Unstructured, error) {
        list, err := c.Dynamic.Resource(GVRArgoApp).Namespace(argoNS).List(ctx, metav1.ListOptions{})
        if err != nil {
                return nil, err
        }
        var matches []*unstructured.Unstructured
        for i := range list.Items {
                item := &list.Items[i]
                if item.GetLabels()[LabelManagedBy] == "k8up-btl" {
                        continue
                }
                dest, _, _ := unstructured.NestedString(item.Object, "spec", "destination", "namespace")
                if dest == ns {
                        matches = append(matches, item)
                }
        }
        switch len(matches) {
        case 0:
                return nil, fmt.Errorf("no Argo Application deploys into namespace %q — the app lab needs one to clone", ns)
        case 1:
                return matches[0], nil
        default:
                names := make([]string, 0, len(matches))
                for _, m := range matches {
                        names = append(names, m.GetName())
                }
                return nil, fmt.Errorf("multiple Argo Applications deploy into namespace %q (%s); cannot pick a clone source", ns, strings.Join(names, ", "))
        }
}

// BuildLabApplication assembles the clone Application for an app-lab run:
// same git source (or the AnnLabPath overlay), kustomize-namespace-rewritten
// into the lab, Namespace + Schedule delete-patched out, project-locked, no
// automated sync, resources finalizer, and an initial sync operation.
func BuildLabApplication(sourceApp *unstructured.Unstructured, name, argoNS, project, labNS, labID string) (*unstructured.Unstructured, error) {
        src, found, _ := unstructured.NestedMap(sourceApp.Object, "spec", "source")
        if !found || src == nil {
                return nil, fmt.Errorf("application %s has no spec.source (multi-source apps are not supported by the app lab)", sourceApp.GetName())
        }
        repoURL, _ := src["repoURL"].(string)
        targetRevision, _ := src["targetRevision"].(string)
        path, _ := src["path"].(string)
        if labPath := sourceApp.GetAnnotations()[AnnLabPath]; labPath != "" {
                path = labPath
        }
        if repoURL == "" || path == "" {
                return nil, fmt.Errorf("application %s source is not a git path source (repoURL/path missing)", sourceApp.GetName())
        }
        destServer, _, _ := unstructured.NestedString(sourceApp.Object, "spec", "destination", "server")
        if destServer == "" {
                destServer = "https://kubernetes.default.svc"
        }

        // The apps' manifests hardcode metadata.namespace, so the kustomize
        // namespace transformer (not destination.namespace) does the rewrite.
        // The git-managed Namespace resource keeps its name through that
        // transform and the Schedule must never run in the lab — delete both.
        kustomize := map[string]any{
                "namespace": labNS,
                "patches": []any{
                        map[string]any{
                                "target": map[string]any{"kind": "Namespace"},
                                "patch":  "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: ignored\n$patch: delete\n",
                        },
                        map[string]any{
                                "target": map[string]any{"group": "k8up.io", "version": "v1", "kind": "Schedule"},
                                "patch":  "apiVersion: k8up.io/v1\nkind: Schedule\nmetadata:\n  name: ignored\n$patch: delete\n",
                        },
                },
        }

        obj := &unstructured.Unstructured{Object: map[string]any{
                "apiVersion": "argoproj.io/v1alpha1",
                "kind":       "Application",
                "metadata": map[string]any{
                        "name":      name,
                        "namespace": argoNS,
                        "labels": map[string]any{
                                LabelManagedBy: "k8up-btl",
                                LabelLabID:     labID,
                        },
                        // Cascade: deleting the clone deletes what it deployed.
                        "finalizers": []any{ArgoResourcesFinalizer},
                },
                "spec": map[string]any{
                        "project": project,
                        "source": map[string]any{
                                "repoURL":        repoURL,
                                "targetRevision": targetRevision,
                                "path":           path,
                                "kustomize":      kustomize,
                        },
                        "destination": map[string]any{
                                "server":    destServer,
                                "namespace": labNS,
                        },
                        // No syncPolicy on purpose: sync once (operation below),
                        // never self-heal — the lab must not fight manual pokes.
                },
                // Initial one-shot sync, processed by the application-controller
                // on admission.
                "operation": map[string]any{
                        "initiatedBy": map[string]any{"username": "k8up-btl"},
                        "sync":        map[string]any{"prune": false},
                },
        }}
        return obj, nil
}

func (c *Clients) CreateApplication(ctx context.Context, app *unstructured.Unstructured) (*unstructured.Unstructured, error) {
        return c.Dynamic.Resource(GVRArgoApp).Namespace(app.GetNamespace()).Create(ctx, app, metav1.CreateOptions{})
}

func (c *Clients) DeleteApplication(ctx context.Context, argoNS, name string) error {
        return c.Dynamic.Resource(GVRArgoApp).Namespace(argoNS).Delete(ctx, name, metav1.DeleteOptions{})
}

// WaitApplicationGone blocks until the Application (and, via its resources
// finalizer, everything it deployed) is deleted.
func (c *Clients) WaitApplicationGone(ctx context.Context, argoNS, name string) error {
        for {
                if err := ctx.Err(); err != nil {
                        return err
                }
                _, err := c.Dynamic.Resource(GVRArgoApp).Namespace(argoNS).Get(ctx, name, metav1.GetOptions{})
                if IsNotFound(err) {
                        return nil
                }
                if err != nil {
                        return err
                }
                t := time.NewTimer(3 * time.Second)
                select {
                case <-ctx.Done():
                        t.Stop()
                        return ctx.Err()
                case <-t.C:
                }
        }
}

// AppStatus is a compact view of an Application's rollout state.
type AppStatus struct {
        Health  string
        Sync    string
        Phase   string // operationState.phase
        Message string
}

// GetApplicationStatus reads health/sync/operation state plus any error
// condition (ComparisonError, InvalidSpecError, SyncError).
func (c *Clients) GetApplicationStatus(ctx context.Context, argoNS, name string) (*AppStatus, error) {
        app, err := c.Dynamic.Resource(GVRArgoApp).Namespace(argoNS).Get(ctx, name, metav1.GetOptions{})
        if err != nil {
                return nil, err
        }
        st := &AppStatus{}
        st.Health, _, _ = unstructured.NestedString(app.Object, "status", "health", "status")
        st.Sync, _, _ = unstructured.NestedString(app.Object, "status", "sync", "status")
        st.Phase, _, _ = unstructured.NestedString(app.Object, "status", "operationState", "phase")
        st.Message, _, _ = unstructured.NestedString(app.Object, "status", "operationState", "message")
        if conds, found, _ := unstructured.NestedSlice(app.Object, "status", "conditions"); found {
                for _, cond := range conds {
                        m, ok := cond.(map[string]any)
                        if !ok {
                                continue
                        }
                        t, _ := m["type"].(string)
                        msg, _ := m["message"].(string)
                        if strings.HasSuffix(t, "Error") && msg != "" {
                                st.Message = t + ": " + msg
                                if st.Phase == "" {
                                        st.Phase = "Error"
                                }
                        }
                }
        }
        return st, nil
}

// CreateLabPVCFromSource clones a production PVC's shape (size, access modes,
// storage class) into the lab namespace under the same name, so the cloned
// app's manifests bind to it and a StatefulSet template would adopt it.
func (c *Clients) CreateLabPVCFromSource(ctx context.Context, srcNS, pvcName, labNS, labID string) error {
        src, err := c.Typed.CoreV1().PersistentVolumeClaims(srcNS).Get(ctx, pvcName, metav1.GetOptions{})
        if err != nil {
                return fmt.Errorf("get source PVC %s/%s: %w", srcNS, pvcName, err)
        }
        req := src.Spec.Resources.Requests[corev1.ResourceStorage]
        return c.CreateLabPVC(ctx, labNS, pvcName, labID, src.Spec.AccessModes, src.Spec.StorageClassName, req)
}

// CreateLabPVC creates a lab PVC directly (data-tier scratch volumes).
func (c *Clients) CreateLabPVC(ctx context.Context, labNS, name, labID string, modes []corev1.PersistentVolumeAccessMode, storageClass *string, size resource.Quantity) error {
        if len(modes) == 0 {
                modes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
        }
        pvc := &corev1.PersistentVolumeClaim{
                ObjectMeta: metav1.ObjectMeta{
                        Name:      name,
                        Namespace: labNS,
                        Labels: map[string]string{
                                LabelManagedBy: "k8up-btl",
                                LabelLabID:     labID,
                        },
                },
                Spec: corev1.PersistentVolumeClaimSpec{
                        AccessModes:      modes,
                        StorageClassName: storageClass,
                        Resources: corev1.VolumeResourceRequirements{
                                Requests: corev1.ResourceList{corev1.ResourceStorage: size},
                        },
                },
        }
        _, err := c.Typed.CoreV1().PersistentVolumeClaims(labNS).Create(ctx, pvc, metav1.CreateOptions{})
        return err
}

// CreateLabRestoreCR creates a Restore CR in the lab namespace. Unlike
// CreateRestoreCR it inherits nothing from a Schedule: the lab namespace is
// PSA baseline, K8up's default (root) restore pod preserves file ownership,
// and the backend bucket is passed in explicitly (it points at the SOURCE
// namespace's repository).
func (c *Clients) CreateLabRestoreCR(ctx context.Context, labNS, name, snapshotID, claimName string, backend map[string]any) (*unstructured.Unstructured, error) {
        spec := map[string]any{
                "snapshot": snapshotID,
                "restoreMethod": map[string]any{
                        "folder": map[string]any{
                                "claimName": claimName,
                        },
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
                                "namespace": labNS,
                                "labels": map[string]any{
                                        LabelManagedBy:               "k8up-btl",
                                        "restore-gui.local/one-shot": "true",
                                },
                        },
                        "spec": spec,
                },
        }
        return c.Dynamic.Resource(GVRRestore).Namespace(labNS).Create(ctx, obj, metav1.CreateOptions{})
}

// ListLabPVCs returns the names of every PVC in the lab namespace — teardown
// deletes them all (Argo-created ones carry Delete=false sync-options and
// survive the Application cascade, so ownership filtering would leak them).
func (c *Clients) ListLabPVCs(ctx context.Context, labNS string) ([]string, error) {
        list, err := c.Typed.CoreV1().PersistentVolumeClaims(labNS).List(ctx, metav1.ListOptions{})
        if err != nil {
                return nil, err
        }
        out := make([]string, 0, len(list.Items))
        for i := range list.Items {
                out = append(out, list.Items[i].Name)
        }
        return out, nil
}

// DeleteLabPVCAndPV removes one lab PVC and, because the storage class
// retains released volumes, its bound PV afterwards. Returns the PV name it
// deleted ("" when the claim was unbound).
func (c *Clients) DeleteLabPVCAndPV(ctx context.Context, labNS, name string) (string, error) {
        pvc, err := c.Typed.CoreV1().PersistentVolumeClaims(labNS).Get(ctx, name, metav1.GetOptions{})
        if IsNotFound(err) {
                return "", nil
        }
        if err != nil {
                return "", err
        }
        pvName := pvc.Spec.VolumeName
        if err := c.Typed.CoreV1().PersistentVolumeClaims(labNS).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !IsNotFound(err) {
                return "", err
        }
        if pvName == "" {
                return "", nil
        }
        // Wait for the claim to release before deleting the retained PV.
        for i := 0; i < 60; i++ {
                pv, err := c.Typed.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
                if IsNotFound(err) {
                        return pvName, nil
                }
                if err != nil {
                        return pvName, err
                }
                if pv.Status.Phase == corev1.VolumeReleased || pv.Status.Phase == corev1.VolumeFailed {
                        break
                }
                t := time.NewTimer(2 * time.Second)
                select {
                case <-ctx.Done():
                        t.Stop()
                        return pvName, ctx.Err()
                case <-t.C:
                }
        }
        if err := c.Typed.CoreV1().PersistentVolumes().Delete(ctx, pvName, metav1.DeleteOptions{}); err != nil && !IsNotFound(err) {
                return pvName, err
        }
        return pvName, nil
}

const labInspectName = "lab-inspect"

// CreateInspectWorkload deploys the data-tier file browser: a single-replica
// Deployment mounting the restored PVC read-only plus a ClusterIP Service.
// Reachable through the tunnel (allow-newt-ingress) or port-forward.
func (c *Clients) CreateInspectWorkload(ctx context.Context, labNS, pvcName, image, labID string) error {
        labels := map[string]string{
                "app":          labInspectName,
                LabelManagedBy: "k8up-btl",
                LabelLabID:     labID,
        }
        one := int32(1)
        readOnly := true
        dep := &appsv1.Deployment{
                ObjectMeta: metav1.ObjectMeta{Name: labInspectName, Namespace: labNS, Labels: labels},
                Spec: appsv1.DeploymentSpec{
                        Replicas: &one,
                        Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
                        Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": labInspectName}},
                        Template: corev1.PodTemplateSpec{
                                ObjectMeta: metav1.ObjectMeta{Labels: labels},
                                Spec: corev1.PodSpec{
                                        Containers: []corev1.Container{{
                                                Name:  "filebrowser",
                                                Image: image,
                                                Args: []string{
                                                        "--noauth",
                                                        "--address", "0.0.0.0",
                                                        "--port", "8080",
                                                        "--root", "/data",
                                                        "--database", "/tmp/filebrowser.db",
                                                },
                                                Ports: []corev1.ContainerPort{{ContainerPort: 8080, Name: "http"}},
                                                VolumeMounts: []corev1.VolumeMount{
                                                        {Name: "data", MountPath: "/data", ReadOnly: true},
                                                        {Name: "tmp", MountPath: "/tmp"},
                                                },
                                        }},
                                        Volumes: []corev1.Volume{
                                                {Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName, ReadOnly: readOnly}}},
                                                {Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
                                        },
                                },
                        },
                },
        }
        if _, err := c.Typed.AppsV1().Deployments(labNS).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
                return err
        }
        svc := &corev1.Service{
                ObjectMeta: metav1.ObjectMeta{Name: labInspectName, Namespace: labNS, Labels: labels},
                Spec: corev1.ServiceSpec{
                        Selector: map[string]string{"app": labInspectName},
                        Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
                },
        }
        _, err := c.Typed.CoreV1().Services(labNS).Create(ctx, svc, metav1.CreateOptions{})
        return err
}

// DeleteInspectWorkload removes the data-tier browser (idempotent).
func (c *Clients) DeleteInspectWorkload(ctx context.Context, labNS string) error {
        if err := c.Typed.AppsV1().Deployments(labNS).Delete(ctx, labInspectName, metav1.DeleteOptions{}); err != nil && !IsNotFound(err) {
                return err
        }
        if err := c.Typed.CoreV1().Services(labNS).Delete(ctx, labInspectName, metav1.DeleteOptions{}); err != nil && !IsNotFound(err) {
                return err
        }
        return nil
}

// NamespaceExists verifies the lab namespace is deployed before a run starts.
func (c *Clients) NamespaceExists(ctx context.Context, name string) (bool, error) {
        _, err := c.Typed.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
        if IsNotFound(err) {
                return false, nil
        }
        if err != nil {
                return false, err
        }
        return true, nil
}
