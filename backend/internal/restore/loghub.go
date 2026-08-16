package restore

import (
        "sync"
)

const maxLogLines = 2000

// LogHub buffers restore job log lines and fans them out to subscribers.
type LogHub struct {
        mu      sync.RWMutex
        buffers map[string][]string
        subs    map[string]map[chan string]struct{}
}

func NewLogHub() *LogHub {
        return &LogHub{
                buffers: map[string][]string{},
                subs:    map[string]map[chan string]struct{}{},
        }
}

func (h *LogHub) Append(restoreID, line string) {
        h.mu.Lock()
        buf := append(h.buffers[restoreID], line)
        if len(buf) > maxLogLines {
                buf = buf[len(buf)-maxLogLines:]
        }
        h.buffers[restoreID] = buf
        subs := h.subs[restoreID]
        // copy chans under lock
        var chans []chan string
        for ch := range subs {
                chans = append(chans, ch)
        }
        h.mu.Unlock()
        for _, ch := range chans {
                select {
                case ch <- line:
                default:
                        // drop if subscriber is slow
                }
        }
}

func (h *LogHub) Snapshot(restoreID string) []string {
        h.mu.RLock()
        defer h.mu.RUnlock()
        src := h.buffers[restoreID]
        out := make([]string, len(src))
        copy(out, src)
        return out
}

// Subscribe returns a channel of new lines and an unsubscribe func.
// Buffer is not replayed on the channel — call Snapshot first.
func (h *LogHub) Subscribe(restoreID string) (<-chan string, func()) {
        ch := make(chan string, 64)
        h.mu.Lock()
        if h.subs[restoreID] == nil {
                h.subs[restoreID] = map[chan string]struct{}{}
        }
        h.subs[restoreID][ch] = struct{}{}
        h.mu.Unlock()
        unsub := func() {
                h.mu.Lock()
                if m := h.subs[restoreID]; m != nil {
                        delete(m, ch)
                        if len(m) == 0 {
                                delete(h.subs, restoreID)
                        }
                }
                h.mu.Unlock()
                close(ch)
        }
        return ch, unsub
}
