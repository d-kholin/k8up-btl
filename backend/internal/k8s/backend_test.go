package k8s

import "testing"

func TestS3BucketFromRepo(t *testing.T) {
	got := s3BucketFromRepo("s3:https://garage.nakunga.com/naku-k8up/lubelogger")
	if got != "naku-k8up/lubelogger" {
		t.Fatalf("got %q", got)
	}
}
