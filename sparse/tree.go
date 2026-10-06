package sparse

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"strings"
)

// dirNode is one directory in a commit's file tree. The subtree hash lets
// diffs skip unchanged subtrees in O(1).
type dirNode struct {
	dirs  map[string]*dirNode
	files map[string]struct{}
	hash  uint64
}

// Commit is an immutable file tree: leaves are files, inner nodes are dirs.
type Commit struct {
	ID        string
	root      *dirNode
	FileCount int
}

func (n *dirNode) child(name string) *dirNode {
	if n == nil {
		return nil
	}
	return n.dirs[name]
}

func (n *dirNode) hasFile(name string) bool {
	if n == nil {
		return false
	}
	_, ok := n.files[name]
	return ok
}

// NewCommit builds a commit tree from file paths. Paths must be unique,
// non-empty, free of ".." and empty segments, and a path may not be both a
// file and a directory.
func NewCommit(id string, files []string) (*Commit, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty commit id", ErrInvalidParam)
	}
	c := &Commit{ID: id, root: &dirNode{}}
	seen := make(map[string]struct{}, len(files))
	for _, f := range files {
		if !validPathSegments(f) {
			return nil, fmt.Errorf("%w: malformed path %q", ErrInvalidParam, f)
		}
		if _, dup := seen[f]; dup {
			return nil, fmt.Errorf("%w: duplicate path %q", ErrInvalidParam, f)
		}
		seen[f] = struct{}{}
		if err := c.addFile(splitPath(f)); err != nil {
			return nil, err
		}
	}
	c.FileCount = len(files)
	c.root.computeHash()
	return c, nil
}

func (c *Commit) addFile(segs []string) error {
	n := c.root
	for _, d := range segs[:len(segs)-1] {
		if n.hasFile(d) {
			return fmt.Errorf("%w: %q is both file and directory", ErrInvalidParam, d)
		}
		ch := n.dirs[d]
		if ch == nil {
			if n.dirs == nil {
				n.dirs = map[string]*dirNode{}
			}
			ch = &dirNode{}
			n.dirs[d] = ch
		}
		n = ch
	}
	last := segs[len(segs)-1]
	if n.dirs[last] != nil {
		return fmt.Errorf("%w: %q is both file and directory", ErrInvalidParam, last)
	}
	if n.files == nil {
		n.files = map[string]struct{}{}
	}
	n.files[last] = struct{}{}
	return nil
}

func (n *dirNode) computeHash() uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for _, name := range slices.Sorted(maps.Keys(n.dirs)) {
		h.Write([]byte("d"))
		h.Write([]byte(name))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(buf[:], n.dirs[name].computeHash())
		h.Write(buf[:])
	}
	for _, name := range slices.Sorted(maps.Keys(n.files)) {
		h.Write([]byte("f"))
		h.Write([]byte(name))
		h.Write([]byte{0})
	}
	n.hash = h.Sum64()
	return n.hash
}

// containsFile reports whether p is a file in this commit.
func (c *Commit) containsFile(p string) bool {
	segs := splitPath(p)
	n := c.root
	for _, s := range segs[:len(segs)-1] {
		n = n.child(s)
	}
	return n.hasFile(segs[len(segs)-1])
}

// dirNodeAt returns the directory node at p, or nil if absent. p == "" is root.
func (c *Commit) dirNodeAt(p string) *dirNode {
	n := c.root
	if p == "" {
		return n
	}
	for _, s := range splitPath(p) {
		n = n.child(s)
	}
	return n
}

// diffCommits returns files added and removed between two commits. Subtrees
// with equal hashes are skipped, so cost tracks the changed paths only.
func diffCommits(oldC, newC *Commit, st *Stats) (added, removed []string) {
	diffDirNodes(oldC.root, newC.root, "", st, &added, &removed)
	return added, removed
}

func diffDirNodes(a, b *dirNode, prefix string, st *Stats, added, removed *[]string) {
	if a != nil && b != nil && a.hash == b.hash {
		return
	}
	if st != nil {
		st.TreeNodes.Add(1)
	}
	if a != nil {
		for f := range a.files {
			if !b.hasFile(f) {
				*removed = append(*removed, childPath(prefix, f))
			}
		}
	}
	if b != nil {
		for f := range b.files {
			if !a.hasFile(f) {
				*added = append(*added, childPath(prefix, f))
			}
		}
	}
	if a != nil {
		for name, ch := range a.dirs {
			diffDirNodes(ch, b.child(name), childPath(prefix, name), st, added, removed)
		}
	}
	if b != nil {
		for name, ch := range b.dirs {
			if a.child(name) == nil {
				diffDirNodes(nil, ch, childPath(prefix, name), st, added, removed)
			}
		}
	}
}

// collectFiles appends every file under node n (located at prefix) to out.
func collectFiles(n *dirNode, prefix string, st *Stats, out *[]string) {
	if st != nil {
		st.TreeNodes.Add(1)
	}
	for f := range n.files {
		*out = append(*out, childPath(prefix, f))
	}
	for name, ch := range n.dirs {
		collectFiles(ch, childPath(prefix, name), st, out)
	}
}

// --- path helpers ---

func splitPath(p string) []string { return strings.Split(p, "/") }

func joinSegs(segs []string) string { return strings.Join(segs, "/") }

func childPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// ancestors returns every ancestor directory of p, nearest first.
// ancestors("a/b/c") == ["a/b", "a"].
func ancestors(p string) []string {
	var out []string
	for {
		i := strings.LastIndex(p, "/")
		if i < 0 {
			return out
		}
		p = p[:i]
		out = append(out, p)
	}
}

// validPathSegments reports whether p is a well-formed relative path:
// non-empty, no empty segments (no leading/trailing/double separators),
// no ".." components.
func validPathSegments(p string) bool {
	if p == "" {
		return false
	}
	for _, s := range splitPath(p) {
		if s == "" || s == ".." {
			return false
		}
	}
	return true
}
