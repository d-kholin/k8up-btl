package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/d-kholin/k8up-gui/internal/config"
	"github.com/d-kholin/k8up-gui/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRestorePlanSourceLockAndReadOnly(t *testing.T) {
	replicas := int32(2)
	typed := fake.NewSimpleClientset(
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "app", Name: "data"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "app", Name: "web"}, Spec: appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}}}}}},
	)
	dynamic := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "k8up.io/v1", "kind": "Snapshot", "metadata": map[string]any{"namespace": "app", "name": "snap"},
		"spec": map[string]any{"paths": []any{"/data/data"}, "date": "2026-09-14T12:00:00Z"},
	}})
	s := &Server{K8s: &k8s.Clients{Typed: typed, Dynamic: dynamic}, Cfg: config.Config{ArgoCDNamespace: "argocd"}}
	for _, tc := range []struct {
		pvc    string
		status int
	}{{"data", http.StatusOK}, {"another-volume", http.StatusBadRequest}, {"", http.StatusBadRequest}} {
		req := httptest.NewRequest(http.MethodGet, "/plan?pvc="+tc.pvc, nil)
		req.SetPathValue("namespace", "app")
		req.SetPathValue("name", "snap")
		response := httptest.NewRecorder()
		s.handleRestorePlan(response, req)
		if response.Code != tc.status {
			t.Fatalf("pvc=%q status=%d body=%s", tc.pvc, response.Code, response.Body.String())
		}
		if response.Code == http.StatusOK {
			var plan struct {
				Workload         k8s.WorkloadRef `json:"workload"`
				OriginalReplicas int32           `json:"originalReplicas"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if plan.Workload.Name != "web" || plan.Workload.Namespace != "app" || plan.OriginalReplicas != 2 {
				t.Fatalf("wrong impact preview: %+v", plan)
			}
		}
	}
	for _, action := range typed.Actions() {
		if action.GetVerb() != "get" && action.GetVerb() != "list" {
			t.Fatalf("preview mutated cluster: %v", action)
		}
	}
	for _, action := range dynamic.Actions() {
		if action.GetVerb() != "get" {
			t.Fatalf("preview mutated snapshots: %v", action)
		}
	}
}
