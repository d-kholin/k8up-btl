package k8s

import "time"

// newTimer isolates time.NewTimer for WaitPodsGone helper.
func newTimer(ns int64) *time.Timer {
        return time.NewTimer(time.Duration(ns))
}
