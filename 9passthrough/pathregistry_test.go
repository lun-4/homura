package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewPathRegistry(t *testing.T) {
	registry := NewPathRegistry()
	if registry == nil {
		t.Fatal("NewPathRegistry returned nil")
	}

	if registry.root == nil {
		t.Fatal("registry.root is nil")
	}

	if registry.exposed == nil {
		t.Fatal("registry.exposed is nil")
	}
}

func TestAddPath(t *testing.T) {
	registry := NewPathRegistry()

	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "pathregistry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Test adding a valid path
	if err := registry.AddPath(tmpDir); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	// Verify path is exposed
	if visibility := registry.CheckPath(tmpDir); visibility != Exposed {
		t.Errorf("Expected path to be Exposed, got %v", visibility)
	}

	// Test adding the same path again (should succeed)
	if err := registry.AddPath(tmpDir); err != nil {
		t.Errorf("AddPath failed on duplicate: %v", err)
	}

	// Test adding a non-existent path
	nonExistent := filepath.Join(tmpDir, "does-not-exist")
	if err := registry.AddPath(nonExistent); err == nil {
		t.Error("AddPath should fail for non-existent path")
	}

	// Test adding a relative path
	if err := registry.AddPath("relative/path"); err == nil {
		t.Error("AddPath should fail for relative path")
	}
}

func TestRemovePath(t *testing.T) {
	registry := NewPathRegistry()

	// Create temporary directories
	tmpDir, err := os.MkdirTemp("", "pathregistry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Add and then remove a path
	if err := registry.AddPath(tmpDir); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	registry.RemovePath(tmpDir)

	// Verify path is not visible
	if visibility := registry.CheckPath(tmpDir); visibility != NotVisible {
		t.Errorf("Expected path to be NotVisible after removal, got %v", visibility)
	}

	// Test removing a path that was never added (should not panic)
	registry.RemovePath("/some/random/path")
}

func TestCheckPath(t *testing.T) {
	registry := NewPathRegistry()

	// Create a nested directory structure
	tmpDir, err := os.MkdirTemp("", "pathregistry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	nestedDir := filepath.Join(tmpDir, "subdir", "nested")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("Failed to create nested dir: %v", err)
	}

	// Add the nested path
	if err := registry.AddPath(nestedDir); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	// Check that the nested path is exposed
	if visibility := registry.CheckPath(nestedDir); visibility != Exposed {
		t.Errorf("Expected nested path to be Exposed, got %v", visibility)
	}

	// Check that parent directories are virtual ancestors
	subdir := filepath.Join(tmpDir, "subdir")
	if visibility := registry.CheckPath(subdir); visibility != VirtualAncestor {
		t.Errorf("Expected parent to be VirtualAncestor, got %v", visibility)
	}

	if visibility := registry.CheckPath(tmpDir); visibility != VirtualAncestor {
		t.Errorf("Expected root parent to be VirtualAncestor, got %v", visibility)
	}

	// Check that a sibling path is not visible
	sibling := filepath.Join(tmpDir, "other")
	if err := os.Mkdir(sibling, 0755); err != nil {
		t.Fatalf("Failed to create sibling dir: %v", err)
	}
	if visibility := registry.CheckPath(sibling); visibility != NotVisible {
		t.Errorf("Expected sibling to be NotVisible, got %v", visibility)
	}
}

func TestListPaths(t *testing.T) {
	registry := NewPathRegistry()

	// Initially empty
	paths := registry.ListPaths()
	if len(paths) != 0 {
		t.Errorf("Expected empty list, got %d paths", len(paths))
	}

	// Create temporary directories
	tmpDir1, err := os.MkdirTemp("", "pathregistry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir1)

	tmpDir2, err := os.MkdirTemp("", "pathregistry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir2)

	// Add paths
	if err := registry.AddPath(tmpDir1); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}
	if err := registry.AddPath(tmpDir2); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	// Check list
	paths = registry.ListPaths()
	if len(paths) != 2 {
		t.Errorf("Expected 2 paths, got %d", len(paths))
	}

	// Verify both paths are in the list
	foundDir1 := false
	foundDir2 := false
	for _, p := range paths {
		if p == tmpDir1 {
			foundDir1 = true
		}
		if p == tmpDir2 {
			foundDir2 = true
		}
	}

	if !foundDir1 {
		t.Error("tmpDir1 not found in list")
	}
	if !foundDir2 {
		t.Error("tmpDir2 not found in list")
	}
}

func TestGetExposedChildren(t *testing.T) {
	registry := NewPathRegistry()

	// Create a nested directory structure
	tmpDir, err := os.MkdirTemp("", "pathregistry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dir1 := filepath.Join(tmpDir, "dir1")
	dir2 := filepath.Join(tmpDir, "dir2")
	dir3 := filepath.Join(tmpDir, "dir3")

	for _, dir := range []string{dir1, dir2, dir3} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}
	}

	// Expose only dir1 and dir2
	if err := registry.AddPath(dir1); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}
	if err := registry.AddPath(dir2); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	// Get exposed children of tmpDir
	children := registry.GetExposedChildren(tmpDir)

	// Should return "dir1" and "dir2" but not "dir3"
	if len(children) != 2 {
		t.Errorf("Expected 2 children, got %d", len(children))
	}

	foundDir1 := false
	foundDir2 := false
	foundDir3 := false

	for _, child := range children {
		if child == "dir1" {
			foundDir1 = true
		}
		if child == "dir2" {
			foundDir2 = true
		}
		if child == "dir3" {
			foundDir3 = true
		}
	}

	if !foundDir1 {
		t.Error("dir1 not in exposed children")
	}
	if !foundDir2 {
		t.Error("dir2 not in exposed children")
	}
	if foundDir3 {
		t.Error("dir3 should not be in exposed children")
	}
}

func TestOverlappingPaths(t *testing.T) {
	registry := NewPathRegistry()

	// Create a nested directory structure
	tmpDir, err := os.MkdirTemp("", "pathregistry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	nestedDir := filepath.Join(tmpDir, "nested")
	if err := os.Mkdir(nestedDir, 0755); err != nil {
		t.Fatalf("Failed to create nested dir: %v", err)
	}

	// Add both parent and child
	if err := registry.AddPath(tmpDir); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}
	if err := registry.AddPath(nestedDir); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	// Both should be exposed
	if visibility := registry.CheckPath(tmpDir); visibility != Exposed {
		t.Errorf("Expected tmpDir to be Exposed, got %v", visibility)
	}
	if visibility := registry.CheckPath(nestedDir); visibility != Exposed {
		t.Errorf("Expected nestedDir to be Exposed, got %v", visibility)
	}

	// Remove parent
	registry.RemovePath(tmpDir)

	// Parent should now be a virtual ancestor
	if visibility := registry.CheckPath(tmpDir); visibility != VirtualAncestor {
		t.Errorf("Expected tmpDir to be VirtualAncestor after removal, got %v", visibility)
	}

	// Child should still be exposed
	if visibility := registry.CheckPath(nestedDir); visibility != Exposed {
		t.Errorf("Expected nestedDir to still be Exposed, got %v", visibility)
	}
}
