package commands

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lun-4/homura/internal/config"
	"github.com/lun-4/homura/internal/git"
)

func cloneTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

func cloneTestWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func cloneTestRepo(t *testing.T) string {
	t.Helper()
	// Resolve symlinks: on macOS the temp dir lives under /var -> /private/var,
	// and git reports the resolved path.
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cloneTestGit(t, repo, "init", "--initial-branch=main")
	cloneTestGit(t, repo, "config", "user.name", "Homura Test")
	cloneTestGit(t, repo, "config", "user.email", "homura@example.invalid")
	cloneTestWrite(t, filepath.Join(repo, ".gitignore"), ".homura/\n")
	for _, name := range []string{"tracked", "staged", "deleted"} {
		cloneTestWrite(t, filepath.Join(repo, name), "initial\n")
	}
	cloneTestGit(t, repo, "add", ".")
	cloneTestGit(t, repo, "commit", "-m", "initial")
	t.Chdir(repo)
	return repo
}

func cloneTestDefault(t *testing.T, repo, want string) {
	t.Helper()
	state, err := config.LoadState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.DefaultBranch != want {
		t.Fatalf("default branch = %q, want %q", state.DefaultBranch, want)
	}
}

func cloneTestLs(t *testing.T) string {
	t.Helper()
	output, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	original := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = original }()
	if err := Ls(); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(output)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCloneCopyLifecycle(t *testing.T) {
	for _, base := range []string{"", "HEAD"} {
		t.Run("base="+base, func(t *testing.T) {
			repo := cloneTestRepo(t)
			if err := Clone("feature/worktree", ""); err != nil {
				t.Fatal(err)
			}
			cloneTestDefault(t, repo, "feature/worktree")
			worktree := filepath.Join(repo, ".homura", "feature.worktree")
			info, err := os.Stat(filepath.Join(worktree, ".git"))
			if err != nil || info.IsDir() {
				t.Fatalf("worktree .git must be a file: %v", err)
			}
			worktreeStatus := cloneTestGit(t, worktree, "status", "--porcelain")
			cloneTestWrite(t, filepath.Join(repo, "tracked"), "unstaged edit\n")
			cloneTestWrite(t, filepath.Join(repo, "staged"), "staged edit\n")
			cloneTestGit(t, repo, "add", "staged")
			cloneTestWrite(t, filepath.Join(repo, "untracked"), "untracked content\n")
			if err := os.Remove(filepath.Join(repo, "deleted")); err != nil {
				t.Fatal(err)
			}
			parentStatus := cloneTestGit(t, repo, "status", "--porcelain")
			parentHead := cloneTestGit(t, repo, "rev-parse", "HEAD")
			if err := CloneCopy("feature/copy", base); err != nil {
				t.Fatal(err)
			}
			cloneTestDefault(t, repo, "feature/copy")
			copyPath := filepath.Join(repo, ".homura", "feature.copy")
			info, err = os.Stat(filepath.Join(copyPath, ".git"))
			if err != nil || !info.IsDir() {
				t.Fatalf("copy .git must be a directory: %v", err)
			}
			if got := cloneTestGit(t, copyPath, "rev-parse", "--git-common-dir"); got != ".git" {
				t.Fatalf("copy shares git directory: %q", got)
			}
			if got := cloneTestGit(t, copyPath, "branch", "--show-current"); got != "feature/copy" {
				t.Fatalf("copy branch = %q", got)
			}
			if got := cloneTestGit(t, copyPath, "rev-parse", "HEAD"); got != parentHead {
				t.Fatalf("copy HEAD = %q, want %q", got, parentHead)
			}
			for name, want := range map[string]string{"tracked": "unstaged edit\n", "staged": "staged edit\n", "untracked": "untracked content\n"} {
				data, err := os.ReadFile(filepath.Join(copyPath, name))
				if err != nil || string(data) != want {
					t.Fatalf("copy %s = %q, %v; want %q", name, data, err, want)
				}
			}
			if _, err := os.Stat(filepath.Join(copyPath, "deleted")); !os.IsNotExist(err) {
				t.Fatalf("deleted file was restored in copy: %v", err)
			}
			t.Chdir(copyPath)
			if got, err := git.GetRepoRoot(copyPath); err != nil || got != repo {
				t.Fatalf("GetRepoRoot(copy) = %q, %v; want %q", got, err, repo)
			}
			listing := cloneTestLs(t)
			for _, want := range []string{"feature/worktree", "feature/copy (default)"} {
				if strings.Count(listing, want) != 1 {
					t.Errorf("Ls must list %q exactly once:\n%s", want, listing)
				}
			}
			t.Chdir(repo)
			if err := Rm("", false); err == nil || !strings.Contains(err.Error(), "use -f to remove") {
				t.Fatalf("Rm dirty default copy = %v; want force-required error", err)
			}
			if _, err := os.Stat(filepath.Join(copyPath, ".git")); err != nil {
				t.Fatalf("refused removal deleted copy: %v", err)
			}
			if err := Rm("", true); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
				t.Fatalf("forced removal left copy: %v", err)
			}
			if got := cloneTestGit(t, repo, "status", "--porcelain"); got != parentStatus {
				t.Fatalf("parent changes altered: got %q, want %q", got, parentStatus)
			}
			if got := cloneTestGit(t, repo, "rev-parse", "HEAD"); got != parentHead {
				t.Fatalf("parent HEAD altered: %q", got)
			}
			if got := cloneTestGit(t, worktree, "status", "--porcelain"); got != worktreeStatus {
				t.Fatalf("worktree changed: got %q, want %q", got, worktreeStatus)
			}
			if got := cloneTestGit(t, repo, "worktree", "list", "--porcelain"); !strings.Contains(got, "worktree "+worktree+"\n") {
				t.Fatalf("original worktree no longer registered:\n%s", got)
			}
		})
	}
}

func TestCloneCopyExplicitBase(t *testing.T) {
	repo := cloneTestRepo(t)
	base := cloneTestGit(t, repo, "rev-parse", "HEAD")
	cloneTestWrite(t, filepath.Join(repo, "tracked"), "later commit\n")
	cloneTestGit(t, repo, "add", "tracked")
	cloneTestGit(t, repo, "commit", "-m", "later")
	parentHead := cloneTestGit(t, repo, "rev-parse", "HEAD")
	if err := CloneCopy("from/base", base); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(repo, ".homura", "from.base")
	if got := cloneTestGit(t, copyPath, "rev-parse", "HEAD"); got != base {
		t.Fatalf("copy HEAD = %q, want explicit base %q", got, base)
	}
	if got := cloneTestGit(t, copyPath, "show", "HEAD:tracked"); got != "initial" {
		t.Fatalf("base content = %q", got)
	}
	if got := cloneTestGit(t, repo, "rev-parse", "HEAD"); got != parentHead {
		t.Fatalf("parent HEAD changed: %q", got)
	}
	cloneTestDefault(t, repo, "from/base")
	cloneTestGit(t, copyPath, "reset", "--hard", "HEAD")
	cloneTestGit(t, copyPath, "config", "user.name", "Homura Test")
	cloneTestGit(t, copyPath, "config", "user.email", "homura@example.invalid")
	cloneTestWrite(t, filepath.Join(copyPath, "unique"), "copy-only history\n")
	cloneTestGit(t, copyPath, "add", "unique")
	cloneTestGit(t, copyPath, "commit", "-m", "copy-only commit")
	uniqueHead := cloneTestGit(t, copyPath, "rev-parse", "HEAD")
	cloneTestWrite(t, filepath.Join(copyPath, ".git", "info", "exclude"), "CLAUDE.local.md\nignored\n")
	cloneTestWrite(t, filepath.Join(copyPath, "ignored"), "keep ignored data\n")
	if got := cloneTestGit(t, copyPath, "status", "--porcelain"); got != "" {
		t.Fatalf("copy must be clean: %q", got)
	}
	if err := Rm("from/base", false); err == nil || !strings.Contains(err.Error(), "use -f to remove") {
		t.Fatalf("Rm clean independent copy = %v; want force-required error", err)
	}
	if got := cloneTestGit(t, copyPath, "rev-parse", "HEAD"); got != uniqueHead {
		t.Fatalf("refused removal changed unique history: %q", got)
	}
	if data, err := os.ReadFile(filepath.Join(copyPath, "ignored")); err != nil || string(data) != "keep ignored data\n" {
		t.Fatalf("refused removal changed ignored file: %q, %v", data, err)
	}
}

func TestRmRejectsUnsafeCopyPaths(t *testing.T) {
	repo := cloneTestRepo(t)
	if err := CloneCopy("safe", ""); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(repo, ".homura", "safe")
	link := filepath.Join(repo, ".homura", "linked")
	if err := os.Symlink(copyPath, link); err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"..", "linked"} {
		if err := Rm(branch, true); err == nil {
			t.Errorf("Rm(%q, true) accepted unsafe path", branch)
		}
	}
	for _, path := range []string{repo, copyPath} {
		if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
			t.Errorf("unsafe removal damaged %s: %v", path, err)
		}
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("unsafe removal changed symlink: %v", err)
	}
}
