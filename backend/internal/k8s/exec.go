package k8s

import (
	"bytes"
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecStdin runs a command in a pod container, streaming stdin into it and
// forwarding stdout/stderr line-by-line to onLine. Used by SQL dump recovery
// to pipe `restic dump` output into the database client inside the DB pod.
func (c *Clients) ExecStdin(ctx context.Context, ns, pod, container string, command []string, stdin io.Reader, onLine func(string)) error {
	req := c.Typed.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(ns).
		Name(pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.Config, "POST", req.URL())
	if err != nil {
		return err
	}
	out := &lineWriter{onLine: onLine, prefix: ""}
	errw := &lineWriter{onLine: onLine, prefix: "stderr: "}
	defer out.Flush()
	defer errw.Flush()
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: out,
		Stderr: errw,
	})
}

// lineWriter buffers writes and emits complete lines to onLine.
type lineWriter struct {
	onLine func(string)
	prefix string
	buf    bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// incomplete line — keep in buffer
			w.buf.Reset()
			w.buf.WriteString(line)
			break
		}
		w.emit(line)
	}
	return len(p), nil
}

func (w *lineWriter) Flush() {
	if w.buf.Len() > 0 {
		w.emit(w.buf.String())
		w.buf.Reset()
	}
}

func (w *lineWriter) emit(line string) {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	if line == "" {
		return
	}
	if w.onLine != nil {
		w.onLine(w.prefix + line)
	}
}
