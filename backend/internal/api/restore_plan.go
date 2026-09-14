package api

import (
	"net/http"
	"slices"

	"github.com/d-kholin/k8up-gui/internal/restore"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Read-only preview. Start still validates the source and resolves live ownership.
func (s *Server) handleRestorePlan(w http.ResponseWriter, r *http.Request) {
	if s.K8s == nil {
		http.Error(w, "kubernetes client not configured", http.StatusServiceUnavailable)
		return
	}
	ns, name, pvc := r.PathValue("namespace"), r.PathValue("name"), r.URL.Query().Get("pvc")
	snap, err := s.K8s.GetSnapshot(r.Context(), ns, name)
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	sources := restore.SourcePVCCandidates(snapshotPathsOf(snap))
	if pvc == "" || !slices.Contains(sources, pvc) {
		http.Error(w, "target must be a source PVC of this snapshot", http.StatusBadRequest)
		return
	}
	if _, err := s.K8s.Typed.CoreV1().PersistentVolumeClaims(ns).Get(r.Context(), pvc, metav1.GetOptions{}); err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	workload, err := s.K8s.ResolvePVCOwner(r.Context(), ns, pvc)
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	replicas, err := s.K8s.GetReplicas(r.Context(), workload)
	if err != nil {
		s.writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"namespace": ns, "pvcName": pvc, "snapshotName": name,
		"date": snapshotDateOf(snap), "workload": workload, "originalReplicas": replicas,
		"argoNamespace": s.Cfg.ArgoCDNamespace,
	})
}
