package api

import "testing"

func TestIsRetiredRepository(t *testing.T) {
	cases := []struct {
		repo string
		want bool
	}{
		{"s3:https://garage.nakunga.com/naku-k8up/lubelogger", true},
		{"s3:https://s3.us-east-005.backblazeb2.com/k8up-k3s/lubelogger", false},
		{"s3:https://minio.example/naku-k8up/foo", true},
		{"", false},
	}
	for _, c := range cases {
		if got := isRetiredRepository(c.repo); got != c.want {
			t.Errorf("isRetiredRepository(%q)=%v want %v", c.repo, got, c.want)
		}
	}
}

func TestRepositoryMatchesEndpoint(t *testing.T) {
	ep := "https://s3.us-east-005.backblazeb2.com"
	if !repositoryMatchesEndpoint("s3:https://s3.us-east-005.backblazeb2.com/k8up-k3s/ns", ep) {
		t.Fatal("expected match")
	}
	if repositoryMatchesEndpoint("s3:https://garage.nakunga.com/naku-k8up/ns", ep) {
		t.Fatal("expected no match")
	}
	if normalizeS3EndpointHost("s3.us-east-005.backblazeb2.com") != "s3.us-east-005.backblazeb2.com" {
		t.Fatal("host normalize")
	}
}
