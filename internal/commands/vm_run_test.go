package commands

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// withArgs temporarily replaces os.Args for the duration of f.
func withArgs(t *testing.T, args []string, f func()) {
	t.Helper()
	orig := os.Args
	os.Args = args
	defer func() { os.Args = orig }()
	f()
}

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"claude", "'claude'"},
		{"simple path", "'simple path'"},
		{"it's", `'it'\''s'`},
		{"/mnt/host/home/x", "'/mnt/host/home/x'"},
		{"", "''"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitRunArgs(t *testing.T) {
	cases := []struct {
		name          string
		fullArgs      []string // os.Args, program name included
		args          []string // cobra positional args
		wantBranch    string
		wantCommand   []string
	}{
		{
			name:        "no dashdash is all command",
			fullArgs:    []string{"homura", "vm", "run", "claude"},
			args:        []string{"claude"},
			wantBranch:  "",
			wantCommand: []string{"claude"},
		},
		{
			name:        "dashdash before command splits branch",
			fullArgs:    []string{"homura", "vm", "run", "mybranch", "--", "ls", "-la"},
			args:        []string{"mybranch", "ls", "-la"},
			wantBranch:  "mybranch",
			wantCommand: []string{"ls", "-la"},
		},
		{
			name:        "command containing dashdash uses first",
			fullArgs:    []string{"homura", "vm", "run", "mybranch", "--", "claude", "--", "x"},
			args:        []string{"mybranch", "claude", "--", "x"},
			wantBranch:  "mybranch",
			wantCommand: []string{"claude", "--", "x"},
		},
		{
			name:        "branch omitted",
			fullArgs:    []string{"homura", "vm", "run", "--", "ls"},
			args:        []string{"ls"},
			wantBranch:  "",
			wantCommand: []string{"ls"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withArgs(t, c.fullArgs, func() {
				branch, command := splitRunArgs(c.args)
				if branch != c.wantBranch {
					t.Errorf("branch = %q, want %q", branch, c.wantBranch)
				}
				if !reflect.DeepEqual(command, c.wantCommand) {
					t.Errorf("command = %v, want %v", command, c.wantCommand)
				}
			})
		})
	}
}

func TestBuildRemoteCmd(t *testing.T) {
	got := buildRemoteCmd("/home/user/repo", []string{"claude"})
	want := "cd '/mnt/host/home/user/repo' && 'claude'"
	if got != want {
		t.Errorf("buildRemoteCmd = %q, want %q", got, want)
	}

	got = buildRemoteCmd("/home/x", []string{"ls", "-la"})
	want = "cd '/mnt/host/home/x' && 'ls' && '-la'"
	if got != want {
		t.Errorf("buildRemoteCmd = %q, want %q", got, want)
	}

	// Single-quote escaping for paths containing apostrophes.
	got = buildRemoteCmd("/home/luna/it's", []string{"pwd"})
	if !strings.Contains(got, `/mnt/host/home/luna/it'\''s`) {
		t.Errorf("buildRemoteCmd escaping = %q", got)
	}
}