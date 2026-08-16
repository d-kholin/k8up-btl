package resticcmd

import (
	"testing"
)

func TestNormalizeBrowsePath(t *testing.T) {
	cases := map[string]string{
		"":           "/",
		"/":          "/",
		"//":         "/",
		"/data":      "/data",
		"/data/":     "/data",
		"/data//x":   "/data/x",
		"data":       "/data",
		"/data/../y": "/y",
	}
	for in, want := range cases {
		if got := NormalizeBrowsePath(in); got != want {
			t.Errorf("NormalizeBrowsePath(%q)=%q want %q", in, got, want)
		}
	}
}

func TestIsDirectChild(t *testing.T) {
	cases := []struct {
		parent, child string
		want          bool
	}{
		{"/", "/data", true},
		{"/", "/data/nested", false},
		{"/", "/", false},
		{"/data", "/data/file.txt", true},
		{"/data", "/data/nested/x", false},
		{"/data", "/data", false},
		{"/data", "/other", false},
	}
	for _, c := range cases {
		if got := isDirectChild(c.parent, c.child); got != c.want {
			t.Errorf("isDirectChild(%q,%q)=%v want %v", c.parent, c.child, got, c.want)
		}
	}
}

func TestParseLsJSONDirectChildren(t *testing.T) {
	raw := []byte(`{"struct_type":"snapshot","id":"abc"}
{"name":"data","type":"dir","path":"/data"}
{"name":"readme","type":"file","path":"/readme","size":3}
{"name":"nested","type":"dir","path":"/data/nested"}
`)
	nodes, err := parseLSJSON(raw, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 top-level nodes, got %d %#v", len(nodes), nodes)
	}
	// dirs first
	if nodes[0].Name != "data" || nodes[0].Type != "dir" {
		t.Fatalf("first want dir data, got %+v", nodes[0])
	}
	if nodes[1].Name != "readme" || nodes[1].Type != "file" {
		t.Fatalf("second want file readme, got %+v", nodes[1])
	}

	raw2 := []byte(`{"name":"data","type":"dir","path":"/data"}
{"name":"file.txt","type":"file","path":"/data/file.txt","size":12}
{"name":"nested","type":"dir","path":"/data/nested"}
{"name":"deep","type":"file","path":"/data/nested/deep","size":1}
`)
	nodes2, err := parseLSJSON(raw2, "/data")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes2) != 2 {
		t.Fatalf("want 2 children under /data, got %d %#v", len(nodes2), nodes2)
	}
}

func TestRunnerBinaryDefault(t *testing.T) {
	r := &Runner{}
	if r.Binary != "" {
		t.Fatalf("expected empty binary default, got %q", r.Binary)
	}
}
