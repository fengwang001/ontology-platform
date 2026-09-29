package sandbox

import (
	"strings"
	"sync"
)

// NodeKind classifies a tree node.
type NodeKind int

const (
	KindDirectory NodeKind = iota
	KindFile
	KindSymlink
)

// Node is a single entry in the in-memory file tree.
type Node struct {
	kind     NodeKind
	name     string
	parent   *Node
	children map[string]*Node
	target   string
}

// Resolution is the outcome of a physical path resolution.
type Resolution struct {
	Node *Node
	Path string
}

// Tree is a sandboxed in-memory file tree with symbolic links.
type Tree struct {
	mu   sync.RWMutex
	root *Node
}

// New creates an empty tree rooted at the sandbox root.
func New() *Tree {
	root := &Node{
		kind:     KindDirectory,
		name:     "",
		children: map[string]*Node{},
	}
	return &Tree{root: root}
}

// IsDir reports whether the node is a directory.
func (n *Node) IsDir() bool { return n != nil && n.kind == KindDirectory }

// Kind returns the node kind.
func (n *Node) Kind() NodeKind { return n.kind }

// Name returns the base name.
func (n *Node) Name() string { return n.name }

// Target returns the raw symlink target text.
func (n *Node) Target() string { return n.target }

func newDirectory(name string) *Node {
	return &Node{kind: KindDirectory, name: name, children: map[string]*Node{}}
}

func newFile(name string) *Node {
	return &Node{kind: KindFile, name: name}
}

func newSymlink(name, target string) *Node {
	return &Node{kind: KindSymlink, name: name, target: target}
}

// physicalPath returns the canonical location of the node: the chain of
// parent names walked from the sandbox root. Directories reached through
// symbolic links report where they physically live, which is exactly what a
// subsequent ".." must undo.
func (t *Tree) physicalPath(n *Node) string {
	if n == nil || n == t.root {
		return "/"
	}
	var parts []string
	for cur := n; cur != nil && cur != t.root; cur = cur.parent {
		parts = append(parts, cur.name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/")
}

// splitSegments drops empty segments (collapsed slashes are ignored) and
// "." segments; ".." segments are kept so the resolver can apply them
// against the real physical parent.
func splitSegments(path string) []string {
	raw := strings.Split(path, "/")
	segments := make([]string, 0, len(raw))
	for _, seg := range raw {
		if seg == "" || seg == "." {
			continue
		}
		segments = append(segments, seg)
	}
	return segments
}

func validName(name string) bool {
	return name != "" && !strings.Contains(name, "/")
}
