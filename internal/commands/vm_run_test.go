package commands

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/lun-4/homura/internal/vm"
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

func TestResolveVMWorkDirHere(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	here := true
	VMHere = &here
	defer func() { VMHere = nil }()

	// --here with no branch returns the cwd and no branch, no git needed.
	workDir, branch, err := resolveVMWorkDir("", true)
	if err != nil {
		t.Fatalf("resolveVMWorkDir(--here) error: %v", err)
	}
	if workDir != cwd {
		t.Errorf("workDir = %q, want %q", workDir, cwd)
	}
	if branch != "" {
		t.Errorf("branch = %q, want empty", branch)
	}

	// --here with a branch arg must error.
	if _, _, err := resolveVMWorkDir("mybranch", true); err == nil {
		t.Error("resolveVMWorkDir(--here + branch) = nil, want error")
	}

	// resolveVMRunSlot must also reject a numeric-slot arg under --here.
	if _, err := resolveVMRunSlot("3", vm.ShareModeVirtioFS); err == nil {
		t.Error("resolveVMRunSlot(--here + slot) = nil, want error")
	}
}