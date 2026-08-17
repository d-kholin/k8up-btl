package resticcmd

import "testing"

func TestParseDiffJSON(t *testing.T) {
	out := []byte(`{"message_type":"change","path":"/data/pvc/new.txt","modifier":"+"}
{"message_type":"change","path":"/data/pvc/gone.txt","modifier":"-"}
{"message_type":"change","path":"/data/pvc/edited.txt","modifier":"M"}
{"message_type":"change","path":"/data/pvc/touched.txt","modifier":"U"}
{"message_type":"change","path":"/data/pvc/retyped","modifier":"T"}
{"message_type":"statistics","source_snapshot":"abc123","target_snapshot":"def456","changed_files":3,"added":{"files":2,"dirs":1,"others":0,"data_blobs":5,"tree_blobs":2,"bytes":2048},"removed":{"files":1,"dirs":0,"others":0,"data_blobs":1,"tree_blobs":1,"bytes":512}}
`)
	res, err := parseDiffJSON(out)
	if err != nil {
		t.Fatal(err)
	}
	if res.SourceSnapshot != "abc123" || res.TargetSnapshot != "def456" {
		t.Fatalf("snapshot ids: %q %q", res.SourceSnapshot, res.TargetSnapshot)
	}
	if res.ChangedFiles != 3 {
		t.Fatalf("changed_files: %d", res.ChangedFiles)
	}
	if res.Added.Bytes != 2048 || res.Added.Files != 2 || res.Removed.Bytes != 512 {
		t.Fatalf("stats: %+v %+v", res.Added, res.Removed)
	}
	if len(res.Changes) != 5 {
		t.Fatalf("changes: %d", len(res.Changes))
	}
	kinds := map[string]string{}
	for _, c := range res.Changes {
		kinds[c.Path] = c.Kind
	}
	want := map[string]string{
		"/data/pvc/new.txt":     "added",
		"/data/pvc/gone.txt":    "removed",
		"/data/pvc/edited.txt":  "modified",
		"/data/pvc/touched.txt": "metadata",
		"/data/pvc/retyped":     "modified",
	}
	for p, k := range want {
		if kinds[p] != k {
			t.Errorf("%s: kind=%q want %q", p, kinds[p], k)
		}
	}
	// Sorted by path.
	for i := 1; i < len(res.Changes); i++ {
		if res.Changes[i-1].Path > res.Changes[i].Path {
			t.Fatalf("not sorted: %q before %q", res.Changes[i-1].Path, res.Changes[i].Path)
		}
	}
}

func TestParseDiffJSONEmpty(t *testing.T) {
	res, err := parseDiffJSON([]byte(`{"message_type":"statistics","source_snapshot":"a","target_snapshot":"b","changed_files":0,"added":{"files":0,"dirs":0,"bytes":0},"removed":{"files":0,"dirs":0,"bytes":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 0 {
		t.Fatalf("expected no changes, got %d", len(res.Changes))
	}
}
