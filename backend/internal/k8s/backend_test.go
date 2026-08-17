package k8s

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestS3BucketFromRepo(t *testing.T) {
	got := s3BucketFromRepo("s3:https://garage.nakunga.com/naku-k8up/lubelogger")
	if got != "naku-k8up/lubelogger" {
		t.Fatalf("got %q", got)
	}
}

func TestSTSTemplateOwnsPVC(t *testing.T) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres"},
		Spec: appsv1.StatefulSetSpec{
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{ObjectMeta: metav1.ObjectMeta{Name: "data"}},
			},
		},
	}
	cases := []struct {
		pvc  string
		want bool
	}{
		{"data-postgres-0", true},
		{"data-postgres-12", true},
		{"data-postgres-", false},
		{"data-postgres-0a", false},
		{"data-other-0", false},
		{"logs-postgres-0", false},
	}
	for _, c := range cases {
		if got := stsTemplateOwnsPVC(sts, c.pvc); got != c.want {
			t.Errorf("stsTemplateOwnsPVC(%q) = %v, want %v", c.pvc, got, c.want)
		}
	}
}

func TestAppGroupOf(t *testing.T) {
	if got := appGroupOf(map[string]string{"app.kubernetes.io/instance": "mealie"}, nil); got != "mealie" {
		t.Fatalf("label tracking: got %q", got)
	}
	if got := appGroupOf(nil, map[string]string{"argocd.argoproj.io/tracking-id": "mealie:apps/Deployment:mealie/postgres"}); got != "mealie" {
		t.Fatalf("annotation tracking: got %q", got)
	}
	if got := appGroupOf(map[string]string{"app": "postgres"}, map[string]string{}); got != "" {
		t.Fatalf("no tracking: got %q", got)
	}
}
