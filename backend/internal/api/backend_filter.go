package api

import (
	"context"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Retired restic backends that must not surface in the UI after the B2 cutover.
// Matching is case-insensitive substring on Snapshot.spec.repository.
var retiredRepoMarkers = []string{
	"garage.nakunga.com",
	"naku-k8up/",
	"/naku-k8up",
}

// snapshotRepository returns the restic URI from a Snapshot CR.
func snapshotRepository(obj map[string]any) string {
	if repo, _, _ := nestedString(obj, "spec", "repository"); repo != "" {
		return repo
	}
	if repo, _, _ := nestedString(obj, "status", "repository"); repo != "" {
		return repo
	}
	return ""
}

// isRetiredRepository is true for known pre-cutover Garage URIs.
func isRetiredRepository(repo string) bool {
	r := strings.ToLower(repo)
	for _, m := range retiredRepoMarkers {
		if strings.Contains(r, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// normalizeS3EndpointHost strips scheme/path so we can match against restic URIs.
func normalizeS3EndpointHost(endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return ""
	}
	if !strings.Contains(ep, "://") {
		ep = "https://" + ep
	}
	u, err := url.Parse(ep)
	if err != nil || u.Host == "" {
		// best-effort: strip scheme manually
		ep = strings.TrimPrefix(ep, "https://")
		ep = strings.TrimPrefix(ep, "http://")
		if i := strings.Index(ep, "/"); i >= 0 {
			ep = ep[:i]
		}
		return strings.ToLower(strings.TrimSpace(ep))
	}
	return strings.ToLower(u.Host)
}

// repositoryMatchesEndpoint reports whether repoURI is on the given S3 endpoint host.
func repositoryMatchesEndpoint(repoURI, endpoint string) bool {
	host := normalizeS3EndpointHost(endpoint)
	if host == "" || repoURI == "" {
		return false
	}
	return strings.Contains(strings.ToLower(repoURI), host)
}

// filterSnapshotsCurrentBackend drops retired Garage Snapshot CRs and, when the
// global S3 endpoint is known, any snapshot whose repository URI is not on that
// endpoint. Live credentials come from k8up-global; old-location CRs are not operable.
func (s *Server) filterSnapshotsCurrentBackend(ctx context.Context, items []unstructured.Unstructured) []unstructured.Unstructured {
	endpoint := ""
	if s != nil && s.K8s != nil && s.Cfg.K8upGlobalSecretName != "" {
		if sec, err := s.K8s.GetSecret(ctx, s.Cfg.K8upGlobalSecretNS, s.Cfg.K8upGlobalSecretName); err == nil {
			if v, ok := sec.Data["BACKUP_GLOBALS3ENDPOINT"]; ok {
				endpoint = string(v)
			}
		}
	}

	out := make([]unstructured.Unstructured, 0, len(items))
	for i := range items {
		repo := snapshotRepository(items[i].Object)
		if isRetiredRepository(repo) {
			continue
		}
		if endpoint != "" && repo != "" && !repositoryMatchesEndpoint(repo, endpoint) {
			continue
		}
		out = append(out, items[i])
	}
	return out
}

// snapshotUsableWithGlobalBackend is the browse/restore gate for a single CR.
func (s *Server) snapshotUsableWithGlobalBackend(ctx context.Context, repo string) error {
	if isRetiredRepository(repo) {
		return errRetiredBackend
	}
	if s == nil || s.K8s == nil || s.Cfg.K8upGlobalSecretName == "" {
		return nil
	}
	sec, err := s.K8s.GetSecret(ctx, s.Cfg.K8upGlobalSecretNS, s.Cfg.K8upGlobalSecretName)
	if err != nil {
		return nil // fall through; existing secret errors handled elsewhere
	}
	ep := ""
	if v, ok := sec.Data["BACKUP_GLOBALS3ENDPOINT"]; ok {
		ep = string(v)
	}
	if ep != "" && repo != "" && !repositoryMatchesEndpoint(repo, ep) {
		return errRetiredBackend
	}
	return nil
}

// errRetiredBackend is returned when a Snapshot points at a backend the global
// secret no longer serves (e.g. Garage after B2 cutover).
var errRetiredBackend = &backendMismatchError{msg: "snapshot repository is not on the current K8up S3 backend"}

type backendMismatchError struct{ msg string }

func (e *backendMismatchError) Error() string { return e.msg }
