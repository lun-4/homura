package git

import (
	"path/filepath"
	"testing"
)

func TestBranchDirName(t *testing.T) {
	cases := []struct {
		branch string
		want   string
	}{
		{"main", "main"},
		{"feat/foo", "feat.foo"},
		{"feat/foo/bar", "feat.foo.bar"},
		{"feat.foo", "feat.foo"},
	}
	for _, c := range cases {
		if got := BranchDirName(c.branch); got != c.want {
			t.Errorf("BranchDirName(%q) = %q, want %q", c.branch, got, c.want)
		}
	}
}

func TestGetCopyPathMapsSlashes(t *testing.T) {
	got := GetCopyPath("/repo", "feat/foo")
	want := filepath.Join("/repo", ".homura", "feat.foo")
	if got != want {
		t.Errorf("GetCopyPath = %q, want %q", got, want)
	}
}
