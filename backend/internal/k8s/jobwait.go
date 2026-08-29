package k8s

import (
        "context"
        "fmt"
        "strings"
        "time"

        "k8s.io/apimachinery/pkg/runtime/schema"
)

// WaitJobDone polls any K8up job CR (Restore, Backup, Check, …) until it
// reports a terminal state — they share the status.finished +
// Completed/Failed condition schema. Used by the restore orchestrator and the
// Restore Lab manager.
func (c *Clients) WaitJobDone(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) error {
        kind := strings.TrimSuffix(gvr.Resource, "s")
        for {
                if err := ctx.Err(); err != nil {
                        return err
                }
                obj, err := c.GetResource(ctx, gvr, ns, name)
                if err != nil {
                        return err
                }

                // Prefer status.finished — K8up sets this when the job ends (success or fail).
                finished, finFound, _ := jwNestedBool(obj.Object, "status", "finished")
                if finFound && finished {
                        // Completed condition: reason Failed vs Succeeded
                        if conds, found, _ := jwNestedSlice(obj.Object, "status", "conditions"); found {
                                for _, cond := range conds {
                                        m, ok := cond.(map[string]any)
                                        if !ok {
                                                continue
                                        }
                                        t, _ := m["type"].(string)
                                        st, _ := m["status"].(string)
                                        reason, _ := m["reason"].(string)
                                        msg, _ := m["message"].(string)
                                        if t == "Completed" && st == "True" {
                                                if reason == "Failed" || reason == "Error" {
                                                        return fmt.Errorf("%s failed: %s", kind, msg)
                                                }
                                                // Succeeded or Finished without failure
                                                return nil
                                        }
                                        if t == "Failed" && st == "True" {
                                                return fmt.Errorf("%s failed: %s", kind, msg)
                                        }
                                }
                        }
                        // finished but no clear condition — treat as success only if no Failed job message
                        return nil
                }

                // In-progress signals: Ready=True / Progressing=True are NOT success.
                if conds, found, _ := jwNestedSlice(obj.Object, "status", "conditions"); found {
                        for _, cond := range conds {
                                m, ok := cond.(map[string]any)
                                if !ok {
                                        continue
                                }
                                t, _ := m["type"].(string)
                                st, _ := m["status"].(string)
                                reason, _ := m["reason"].(string)
                                msg, _ := m["message"].(string)
                                if t == "Completed" && st == "True" && (reason == "Failed" || reason == "Error") {
                                        return fmt.Errorf("%s failed: %s", kind, msg)
                                }
                                if t == "Failed" && st == "True" {
                                        return fmt.Errorf("%s failed: %s", kind, msg)
                                }
                                // Progressing False + Finished without finished flag yet
                                if t == "Progressing" && st == "False" && reason == "Finished" {
                                        if strings.Contains(strings.ToLower(msg), "failed") {
                                                return fmt.Errorf("%s failed: %s", kind, msg)
                                        }
                                }
                        }
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

func jwNestedBool(obj map[string]any, fields ...string) (bool, bool, error) {
        var cur any = obj
        for _, f := range fields {
                m, ok := cur.(map[string]any)
                if !ok {
                        return false, false, nil
                }
                cur, ok = m[f]
                if !ok {
                        return false, false, nil
                }
        }
        b, ok := cur.(bool)
        return b, ok, nil
}

func jwNestedSlice(obj map[string]any, fields ...string) ([]any, bool, error) {
        var cur any = obj
        for _, f := range fields {
                m, ok := cur.(map[string]any)
                if !ok {
                        return nil, false, nil
                }
                cur, ok = m[f]
                if !ok {
                        return nil, false, nil
                }
        }
        s, ok := cur.([]any)
        return s, ok, nil
}
