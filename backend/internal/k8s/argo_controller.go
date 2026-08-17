package k8s

import (
        "context"
        "errors"
        "fmt"

        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
        "k8s.io/apimachinery/pkg/types"
        "k8s.io/client-go/util/retry"
)

// Default Argo CD application-controller StatefulSet (reconciliation / self-heal).
const ArgoApplicationControllerSTS = "argocd-application-controller"

// ErrArgoAlreadyPaused means another restore (possibly from another replica or
// an interrupted run) already holds the controller pause annotation.
var ErrArgoAlreadyPaused = errors.New("argo application-controller already paused by another restore; resume or clear its restore state first")

// PauseArgoController scales the application-controller to 0 so no app self-heals.
// Durable restore state is stored on the STS annotation. The annotation acts as a
// lock: pausing fails if any restore state is already present.
func (c *Clients) PauseArgoController(ctx context.Context, argoNS, stateJSON string) (originalReplicas int32, err error) {
        err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
                sts, gerr := c.Typed.AppsV1().StatefulSets(argoNS).Get(ctx, ArgoApplicationControllerSTS, metav1.GetOptions{})
                if gerr != nil {
                        return fmt.Errorf("get %s: %w", ArgoApplicationControllerSTS, gerr)
                }
                if prev := sts.Annotations[PausedStateAnnotation]; prev != "" {
                        return ErrArgoAlreadyPaused
                }
                if sts.Spec.Replicas == nil {
                        originalReplicas = 1
                } else {
                        originalReplicas = *sts.Spec.Replicas
                }
                anns := sts.Annotations
                if anns == nil {
                        anns = map[string]string{}
                }
                anns[PausedStateAnnotation] = stateJSON
                sts.Annotations = anns
                zero := int32(0)
                sts.Spec.Replicas = &zero
                _, uerr := c.Typed.AppsV1().StatefulSets(argoNS).Update(ctx, sts, metav1.UpdateOptions{})
                return uerr
        })
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
        return retry.RetryOnConflict(retry.DefaultRetry, func() error {
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
        })
}

// SetArgoControllerStateAnnotation updates durable restore JSON on the controller STS.
func (c *Clients) SetArgoControllerStateAnnotation(ctx context.Context, argoNS, stateJSON string) error {
        return retry.RetryOnConflict(retry.DefaultRetry, func() error {
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
        })
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
