package main

import (
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
)

// PathVisibility represents the visibility status of a path
type PathVisibility int

const (
	// NotVisible means the path is completely hidden
	NotVisible PathVisibility = iota
	// VirtualAncestor means the path is a virtual directory (ancestor of exposed paths)
	VirtualAncestor
	// Exposed means the path is directly exposed and maps to the real filesystem
	Exposed
)

// PathNode represents a node in the path trie
type PathNode struct {
	children              map[string]*PathNode
	isExposed             bool // This exact path is exposed
	hasExposedDescendants bool // Any descendant is exposed
}

// PathRegistry maintains a thread-safe registry of exposed paths
type PathRegistry struct {
	mu      sync.RWMutex
	root    *PathNode
	exposed map[string]bool // Quick existence check
}

// NewPathRegistry creates a new PathRegistry
func NewPathRegistry() *PathRegistry {
	return &PathRegistry{
		root: &PathNode{
			children: make(map[string]*PathNode),
		},
		exposed: make(map[string]bool),
	}
}

// AddPath adds a path to the registry after validating it exists
func (pr *PathRegistry) AddPath(p string) error {
	// Clean and validate path
	p = path.Clean(p)
	if !path.IsAbs(p) {
		return fmt.Errorf("path must be absolute: %s", p)
	}

	// Verify path exists on host filesystem
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("path does not exist: %s: %w", p, err)
	}

	pr.mu.Lock()
	defer pr.mu.Unlock()

	// Already exposed
	if pr.exposed[p] {
		return nil
	}

	// Add to quick lookup map
	pr.exposed[p] = true

	// Split path into components
	components := splitPath(p)

	// Traverse/create trie nodes and mark ancestors
	current := pr.root
	for i, component := range components {
		if current.children == nil {
			current.children = make(map[string]*PathNode)
		}

		if _, exists := current.children[component]; !exists {
			current.children[component] = &PathNode{
				children: make(map[string]*PathNode),
			}
		}

		current = current.children[component]

		// Last component is the exposed path itself
		if i == len(components)-1 {
			current.isExposed = true
		} else {
			// Intermediate components are ancestors
			current.hasExposedDescendants = true
		}
	}

	return nil
}

// RemovePath removes a path from the registry
func (pr *PathRegistry) RemovePath(p string) {
	p = path.Clean(p)

	pr.mu.Lock()
	defer pr.mu.Unlock()

	// Not exposed
	if !pr.exposed[p] {
		return
	}

	// Remove from quick lookup map
	delete(pr.exposed, p)

	// Split path into components
	components := splitPath(p)

	// Navigate to the node and mark as not exposed
	current := pr.root
	ancestors := make([]*PathNode, 0, len(components))

	for _, component := range components {
		if current.children == nil {
			return
		}

		if next, exists := current.children[component]; exists {
			ancestors = append(ancestors, current)
			current = next
		} else {
			return
		}
	}

	// Mark the path as not exposed
	current.isExposed = false

	// Walk back up the tree and update hasExposedDescendants flags
	for i := len(components) - 1; i >= 0; i-- {
		component := components[i]
		parent := pr.root
		if i > 0 {
			for _, c := range components[:i] {
				parent = parent.children[c]
			}
		}

		node := parent.children[component]

		// Check if this node has any exposed descendants
		hasDescendants := node.isExposed || pr.nodeHasExposedDescendants(node)

		if !hasDescendants {
			// Remove this node if it has no exposed descendants
			delete(parent.children, component)
		} else {
			// Update the flag and stop propagating up
			node.hasExposedDescendants = pr.nodeHasExposedDescendants(node)
			break
		}
	}
}

// nodeHasExposedDescendants recursively checks if a node has any exposed descendants
func (pr *PathRegistry) nodeHasExposedDescendants(node *PathNode) bool {
	if node.isExposed {
		return true
	}

	for _, child := range node.children {
		if child.isExposed || pr.nodeHasExposedDescendants(child) {
			return true
		}
	}

	return false
}

// CheckPath returns the visibility status of a path
func (pr *PathRegistry) CheckPath(p string) PathVisibility {
	p = path.Clean(p)

	pr.mu.RLock()
	defer pr.mu.RUnlock()

	// Quick check for exact match
	if pr.exposed[p] {
		return Exposed
	}

	// Split path and traverse trie
	components := splitPath(p)
	current := pr.root

	// Special case: root path "/"
	if len(components) == 0 {
		// Root is exposed if explicitly added
		if current.isExposed {
			return Exposed
		}
		// Root is a virtual ancestor if any paths are exposed
		if current.hasExposedDescendants || len(current.children) > 0 {
			return VirtualAncestor
		}
		// Even with no exposed paths, root should be accessible (empty directory)
		return VirtualAncestor
	}

	parentExposed := false
	for i, component := range components {
		if current.children == nil {
			// If parent is exposed, child might exist on filesystem even if not in trie
			if parentExposed {
				return Exposed
			}
			return NotVisible
		}

		next, exists := current.children[component]
		if !exists {
			// If parent is exposed, child might exist on filesystem even if not in trie
			if parentExposed {
				return Exposed
			}
			return NotVisible
		}

		current = next

		// Track if we're inside an exposed directory
		if current.isExposed {
			parentExposed = true
		}

		// Last component - check if exposed
		if i == len(components)-1 {
			if current.isExposed {
				return Exposed
			}
			// If parent was exposed, this child is also exposed
			if parentExposed {
				return Exposed
			}
			if current.hasExposedDescendants {
				return VirtualAncestor
			}
			return NotVisible
		}
	}

	// Shouldn't reach here, but return NotVisible as safe default
	return NotVisible
}

// ListPaths returns all exposed paths
func (pr *PathRegistry) ListPaths() []string {
	pr.mu.RLock()
	defer pr.mu.RUnlock()

	paths := make([]string, 0, len(pr.exposed))
	for p := range pr.exposed {
		paths = append(paths, p)
	}

	return paths
}

// GetExposedChildren returns the names of exposed child paths for a given parent path
// This is used by VirtualFS to implement readdir for virtual ancestor directories
func (pr *PathRegistry) GetExposedChildren(parentPath string) []string {
	parentPath = path.Clean(parentPath)

	pr.mu.RLock()
	defer pr.mu.RUnlock()

	// Navigate to the parent node
	components := splitPath(parentPath)
	current := pr.root

	for _, component := range components {
		if current.children == nil {
			return nil
		}

		next, exists := current.children[component]
		if !exists {
			return nil
		}

		current = next
	}

	// Collect visible children
	children := make([]string, 0, len(current.children))
	for name := range current.children {
		children = append(children, name)
	}

	return children
}

// splitPath splits a path into components, handling root correctly
func splitPath(p string) []string {
	if p == "/" {
		return []string{}
	}

	// Remove leading slash and split
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return []string{}
	}

	return strings.Split(p, "/")
}
