package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func copyTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Copy Test", "GIT_AUTHOR_EMAIL=copy@example.invalid", "GIT_COMMITTER_NAME=Copy Test", "GIT_COMMITTER_EMAIL=copy@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func copyTestWrite(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func copyTestRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(repo, 0755); err != nil {
		t.Fatal(err)
	}
	copyTestGit(t, repo, "init", "-b", "main")
	copyTestWrite(t, repo, "tracked", "original\n")
	copyTestWrite(t, repo, ".gitignore", "ignored\n.homura/\n")
	copyTestGit(t, repo, "add", ".")
	copyTestGit(t, repo, "commit", "-m", "initial")
	return repo
}

func copyTestAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s absent, got %v", path, err)
	}
}

func TestCopyRepositoryPreservesStateAndIndependence(t *testing.T) {
	repo := copyTestRepo(t)
	copyTestGit(t, repo, "worktree", "add", "-b", "registered", filepath.Join(t.TempDir(), "worktree"))
	copyTestWrite(t, repo, "tracked", "staged\n")
	copyTestGit(t, repo, "add", "tracked")
	copyTestWrite(t, repo, "tracked", "unstaged\n")
	copyTestWrite(t, repo, "untracked", "untracked\n")
	copyTestWrite(t, repo, "ignored", "ignored\n")
	copyTestWrite(t, repo, "executable", "executable\n")
	if err := os.Chmod(filepath.Join(repo, "executable"), 0751); err != nil {
		t.Fatal(err)
	}
	copyTestWrite(t, repo, "private/file", "private\n")
	if err := os.Chmod(filepath.Join(repo, "private"), 0710); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tracked", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(repo, "dangling")); err != nil {
		t.Fatal(err)
	}
	copyTestWrite(t, repo, ".homura/old/sentinel", "skip me")
	dest := filepath.Join(repo, ".homura", "copy")
	status := copyTestGit(t, repo, "status", "--porcelain", "--ignored")
	staged := copyTestGit(t, repo, "diff", "--cached")
	unstaged := copyTestGit(t, repo, "diff")
	head := copyTestGit(t, repo, "rev-parse", "HEAD")
	if err := CopyRepository(repo, dest, "feature/copy", ""); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status", "--porcelain", "--ignored"}, {"diff", "--cached"}, {"diff"}} {
		got, want := copyTestGit(t, dest, args...), copyTestGit(t, repo, args...)
		if args[0] == "status" {
			var lines []string
			for _, line := range strings.Split(want, "\n") {
				if line != "!! .homura/" {
					lines = append(lines, line)
				}
			}
			want = strings.Join(lines, "\n")
		}
		if got != want {
			t.Fatalf("%v differs:\n%s\nwant:\n%s", args, got, want)
		}
	}
	for _, name := range []string{"tracked", "untracked", "ignored", "executable", "private", "private/file", ".git/index", ".git/HEAD"} {
		srcInfo, err := os.Stat(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}
		dstInfo, err := os.Stat(filepath.Join(dest, name))
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(srcInfo, dstInfo) || srcInfo.Mode() != dstInfo.Mode() {
			t.Fatalf("%s shares an inode or has different permissions", name)
		}
	}
	if err := filepath.WalkDir(filepath.Join(dest, ".git", "objects"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dest, path)
		if err != nil {
			return err
		}
		src, err := os.Stat(filepath.Join(repo, rel))
		if err != nil {
			return err
		}
		dst, err := entry.Info()
		if err != nil {
			return err
		}
		if os.SameFile(src, dst) {
			t.Errorf("shared object inode: %s", rel)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"link", "dangling"} {
		got, err := os.Readlink(filepath.Join(dest, name))
		want, _ := os.Readlink(filepath.Join(repo, name))
		if err != nil || got != want {
			t.Fatalf("symlink %s: %q, %v", name, got, err)
		}
	}
	copyTestAbsent(t, filepath.Join(dest, ".homura"))
	copyTestAbsent(t, filepath.Join(dest, ".git", "worktrees"))
	if got := copyTestGit(t, dest, "branch", "--show-current"); got != "feature/copy" {
		t.Fatal(got)
	}
	copyTestGit(t, dest, "add", ".")
	copyTestGit(t, dest, "commit", "-m", "independent")
	copyTestWrite(t, dest, "tracked", "destination only\n")
	if copyTestGit(t, repo, "rev-parse", "HEAD") != head || copyTestGit(t, repo, "status", "--porcelain", "--ignored") != status || copyTestGit(t, repo, "diff", "--cached") != staged || copyTestGit(t, repo, "diff") != unstaged {
		t.Fatal("destination operations changed source")
	}
	cmd := exec.Command("git", "show-ref", "--verify", "refs/heads/feature/copy")
	cmd.Dir = repo
	if err := cmd.Run(); err == nil {
		t.Fatal("destination branch leaked into source")
	}
}

func TestCopyRepositoryBranchAndBase(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new with base", true: "existing preserves edits"}[existing], func(t *testing.T) {
			repo := copyTestRepo(t)
			base := copyTestGit(t, repo, "rev-parse", "HEAD")
			copyTestGit(t, repo, "branch", "existing")
			if !existing {
				copyTestWrite(t, repo, "later", "later\n")
				copyTestGit(t, repo, "add", ".")
				copyTestGit(t, repo, "commit", "-m", "later")
			} else {
				copyTestWrite(t, repo, "tracked", "local edits\n")
			}
			branch := "new"
			if existing {
				branch, base = "existing", ""
			}
			dest := filepath.Join(t.TempDir(), "copy")
			if err := CopyRepository(repo, dest, branch, base); err != nil {
				t.Fatal(err)
			}
			if copyTestGit(t, dest, "branch", "--show-current") != branch {
				t.Fatal("wrong branch")
			}
			if existing {
				if copyTestGit(t, dest, "diff") != copyTestGit(t, repo, "diff") {
					t.Fatal("lost edits")
				}
			} else if copyTestGit(t, dest, "rev-parse", "HEAD") != base {
				t.Fatal("wrong base")
			}
		})
	}
}

func TestCopyRepositoryFailures(t *testing.T) {
	for _, scenario := range []string{"invalid branch", "existing with base", "bad base", "checkout conflict", "base conflict", "ignored conflict", "preexisting", "git file", "git symlink", "alternates", "fifo", "metadata symlink", "objects symlink", "refs symlink", "worktrees symlink", "core worktree", "empty core worktree", "bare config", "bare repository", "relative submodule gitfile", "absolute submodule gitfile", "recursive destination"} {
		t.Run(scenario, func(t *testing.T) {
			repo := copyTestRepo(t)
			dest := filepath.Join(t.TempDir(), "copy")
			branch, base := "copy", ""
			switch scenario {
			case "invalid branch":
				branch = "bad..branch"
			case "existing with base":
				branch, base = "main", "HEAD"
			case "bad base":
				base = "missing-base"
			case "checkout conflict", "base conflict", "ignored conflict":
				copyTestGit(t, repo, "checkout", "-b", "other")
				if scenario == "ignored conflict" {
					copyTestWrite(t, repo, "ignored", "branch data\n")
					copyTestGit(t, repo, "add", "-f", "ignored")
				} else {
					copyTestWrite(t, repo, "tracked", "other\n")
				}
				copyTestGit(t, repo, "commit", "-am", "other")
				copyTestGit(t, repo, "checkout", "main")
				if scenario == "ignored conflict" {
					copyTestWrite(t, repo, "ignored", "local ignored data\n")
				} else {
					copyTestWrite(t, repo, "tracked", "local\n")
				}
				branch = "other"
				if scenario == "base conflict" {
					branch, base = "new", "other"
				}
			case "preexisting":
				copyTestWrite(t, dest, "sentinel", "keep\n")
			case "git file", "git symlink":
				metadata := filepath.Join(filepath.Dir(repo), "metadata")
				if err := os.Rename(filepath.Join(repo, ".git"), metadata); err != nil {
					t.Fatal(err)
				}
				if scenario == "git file" {
					copyTestWrite(t, repo, ".git", "gitdir: "+metadata+"\n")
				} else if err := os.Symlink(metadata, filepath.Join(repo, ".git")); err != nil {
					t.Fatal(err)
				}
			case "alternates":
				copyTestWrite(t, repo, ".git/objects/info/alternates", "")
			case "fifo":
				if err := syscall.Mkfifo(filepath.Join(repo, "pipe"), 0600); err != nil {
					t.Fatal(err)
				}
			case "bare config":
				copyTestGit(t, repo, "config", "core.bare", "true")
			case "bare repository":
				bare := filepath.Join(t.TempDir(), "bare.git")
				copyTestGit(t, repo, "clone", "--bare", repo, bare)
				repo = bare
			case "relative submodule gitfile", "absolute submodule gitfile":
				module := copyTestRepo(t)
				copyTestGit(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", module, "sub")
				copyTestGit(t, repo, "commit", "-am", "submodule")
				if scenario == "absolute submodule gitfile" {
					copyTestWrite(t, repo, "sub/.git", "gitdir: "+filepath.Join(repo, ".git", "modules", "sub")+"\n")
				}
			case "core worktree", "empty core worktree":
				value := repo
				if scenario == "empty core worktree" {
					value = ""
				}
				copyTestGit(t, repo, "config", "core.worktree", value)
			case "objects symlink", "refs symlink", "worktrees symlink":
				name := strings.TrimSuffix(scenario, " symlink")
				path := filepath.Join(repo, ".git", name)
				target := filepath.Join(t.TempDir(), name)
				if name == "worktrees" {
					if err := os.Mkdir(target, 0755); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "metadata symlink":
				if err := os.Symlink("config", filepath.Join(repo, ".git", "linked")); err != nil {
					t.Fatal(err)
				}
			case "recursive destination":
				dest = filepath.Join(repo, "copy")
			}
			if err := CopyRepository(repo, dest, branch, base); err == nil {
				t.Fatal("expected failure")
			}
			if scenario == "preexisting" {
				data, err := os.ReadFile(filepath.Join(dest, "sentinel"))
				if err != nil || string(data) != "keep\n" {
					t.Fatalf("preexisting destination changed: %q, %v", data, err)
				}
			} else {
				copyTestAbsent(t, dest)
			}
		})
	}
}
