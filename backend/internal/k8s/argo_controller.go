package k8s

import (
        "context"
        "fmt"

        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
        "k8s.io/apimachinery/pkg/types"
)

// Default Argo CD application-controller StatefulSet (reconciliation / self-heal).
const ArgoApplicationControllerSTS = "argocd-application-controller"

// PauseArgoController scales the application-controller to 0 so no app self-heals.
// Durable restore state is stored on the STS annotation.
func (c *Clients) PauseArgoController(ctx context.Context, argoNS, stateJSON string) (originalReplicas int32, err error) {
        sts, err := c.Typed.AppsV1().StatefulSets(argoNS).Get(ctx, ArgoApplicationControllerSTS, metav1.GetOptions{})
        if err != nil {
                return 0, fmt.Errorf("get %s: %w", ArgoApplicationControllerSTS, err)
        }
        if sts.Spec.Replicas == nil {
                originalReplicas = 1
        } else {
                originalReplicas = *sts.Spec.Replicas
        }
        // Already paused (e.g. prior crashed restore) — keep recorded original if annotated.
        if originalReplicas == 0 {
                if prev := sts.Annotations[PausedStateAnnotation]; prev != "" && stateJSON != "" {
                        // leave replicas at 0; refresh annotation
                }
        }
        anns := sts.Annotations
        if anns == nil {
                anns = map[string]string{}
        }
        anns[PausedStateAnnotation] = stateJSON
        sts.Annotations = anns
        zero := int32(0)
        sts.Spec.Replicas = &zero
        _, err = c.Typed.AppsV1().StatefulSets(argoNS).Update(ctx, sts, metav1.UpdateOptions{})
        if err != nil {
                return originalReplicas, err
        }
        // Wait controller pods gone so no in-flight reconcile races scale-down.
        wl := &WorkloadRef{Kind: "StatefulSet", Namespace: argoNS, Name: ArgoApplicationControllerSTS}
        if err := c.WaitPodsGone(ctx, wl); err != nil {
                return originalReplicas, fmt.Errorf("wait argo controller stopped: %w", err)
        }
        return originalReplicas, nil
}

// ResumeArgoController scales the application-controller back and clears the annotation.
func (c *Clients) ResumeArgoController(ctx context.Context, argoNS string, originalReplicas int32) error {
        if originalReplicas <= 0 {
                originalReplicas = 1
        }
        sts, err := c.Typed.AppsV1().StatefulSets(argoNS).Get(ctx, ArgoApplicationControllerSTS, metav1.GetOptions{})
        if err != nil {
                return err
        }
        anns := sts.Annotations
        if anns != nil {
                delete(anns, PausedStateAnnotation)
                sts.Annotations = anns
        }
        sts.Spec.Replicas = &originalReplicas
        _, err = c.Typed.AppsV1().StatefulSets(argoNS).Update(ctx, sts, metav1.UpdateOptions{})
        return err
}

// SetArgoControllerStateAnnotation updates durable restore JSON on the controller STS.
func (c *Clients) SetArgoControllerStateAnnotation(ctx context.Context, argoNS, stateJSON string) error {
        sts, err := c.Typed.AppsV1().StatefulSets(argoNS).Get(ctx, ArgoApplicationControllerSTS, metav1.GetOptions{})
        if err != nil {
                return err
        }
        anns := sts.Annotations
        if anns == nil {
                anns = map[string]string{}
        }
        if stateJSON == "" {
                delete(anns, PausedStateAnnotation)
        } else {
                anns[PausedStateAnnotation] = stateJSON
        }
        sts.Annotations = anns
        _, err = c.Typed.AppsV1().StatefulSets(argoNS).Update(ctx, sts, metav1.UpdateOptions{})
        return err
}

// GetArgoControllerPausedState returns annotation JSON if present.
func (c *Clients) GetArgoControllerPausedState(ctx context.Context, argoNS string) (stateJSON string, controllerReplicas int32, err error) {
        sts, err := c.Typed.AppsV1().StatefulSets(argoNS).Get(ctx, ArgoApplicationControllerSTS, metav1.GetOptions{})
        if err != nil {
                return "", 0, err
        }
        if sts.Spec.Replicas != nil {
                controllerReplicas = *sts.Spec.Replicas
        } else {
                controllerReplicas = 1
        }
        if sts.Annotations != nil {
                stateJSON = sts.Annotations[PausedStateAnnotation]
        }
        return stateJSON, controllerReplicas, nil
}

// ScaleSTS is a typed helper (unused externally but handy).
func (c *Clients) ScaleSTS(ctx context.Context, ns, name string, replicas int32) error {
        patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas))
        _, err := c.Typed.AppsV1().StatefulSets(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
        return err
}
