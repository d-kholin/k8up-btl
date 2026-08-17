package k8s

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
)

func fakeDynamic(objs ...runtime.Object) *Clients {
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			GVRSchedule: "ScheduleList",
			GVRSnapshot: "SnapshotList",
		}, objs...)
	return &Clients{Dynamic: dyn}
}

func schedule(ns string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "k8up.io/v1",
		"kind":       "Schedule",
		"metadata":   map[string]any{"name": "backup", "namespace": ns},
		"spec":       spec,
	}}
}

func TestResolveBackupSpecInheritsFromSchedule(t *testing.T) {
	c := fakeDynamic(schedule("linkwarden", map[string]any{
		"backend":      map[string]any{"s3": map[string]any{"bucket": "naku-k8up/linkwarden"}},
		"podConfigRef": map[string]any{"name": "backup-pod"},
		"backup":       map[string]any{"schedule": "@daily-random"},
	}))
	spec := c.ResolveBackupSpec(context.Background(), "linkwarden")

	be, _ := spec["backend"].(map[string]any)
	s3, _ := be["s3"].(map[string]any)
	if s3["bucket"] != "naku-k8up/linkwarden" {
		t.Fatalf("backend not inherited: %v", spec["backend"])
	}
	ref, _ := spec["podConfigRef"].(map[string]any)
	if ref["name"] != "backup-pod" {
		t.Fatalf("podConfigRef not inherited: %v", spec["podConfigRef"])
	}
	if _, ok := spec["podSecurityContext"]; ok {
		t.Fatalf("podSecurityContext invented from nothing: %v", spec["podSecurityContext"])
	}
}

func TestResolveBackupSpecBackupSectionWins(t *testing.T) {
	c := fakeDynamic(schedule("ns", map[string]any{
		"podConfigRef": map[string]any{"name": "schedule-wide"},
		"backup": map[string]any{
			"podConfigRef":       map[string]any{"name": "backup-specific"},
			"podSecurityContext": map[string]any{"runAsNonRoot": true},
		},
	}))
	spec := c.ResolveBackupSpec(context.Background(), "ns")

	ref, _ := spec["podConfigRef"].(map[string]any)
	if ref["name"] != "backup-specific" {
		t.Fatalf("spec.backup.podConfigRef should win: %v", spec["podConfigRef"])
	}
	psc, _ := spec["podSecurityContext"].(map[string]any)
	if psc["runAsNonRoot"] != true {
		t.Fatalf("podSecurityContext not inherited: %v", spec["podSecurityContext"])
	}
}

func TestResolveBackupSpecNoSchedule(t *testing.T) {
	c := fakeDynamic()
	spec := c.ResolveBackupSpec(context.Background(), "empty")
	if len(spec) != 0 {
		t.Fatalf("expected empty spec without a Schedule, got %v", spec)
	}
}
