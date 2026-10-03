package syncplan

import (
	"fmt"
	"strings"
)

// locKey identifies a slot under a parent directory.
type locKey struct {
	parent int
	name   string
}

// validateSnapshot checks all structural invariants of a snapshot:
// ids >= 1, non-empty names without '/', dir/file hash rules, existing
// directory parents, unique names per parent, and no cycles.
func validateSnapshot(s Snapshot) error {
	seen := make(map[locKey]int, len(s))
	for id, e := range s {
		if id < 1 {
			return fmt.Errorf("entry id %d is < 1", id)
		}
		if e.Name == "" {
			return fmt.Errorf("entry %d: empty name", id)
		}
		if strings.Contains(e.Name, "/") {
			return fmt.Errorf("entry %d: name %q contains '/'", id, e.Name)
		}
		if e.Dir {
			if e.Hash != "" {
				return fmt.Errorf("entry %d: directory with non-empty hash", id)
			}
		} else if e.Hash == "" {
			return fmt.Errorf("entry %d: file with empty hash", id)
		}
		if e.Parent != 0 {
			p, ok := s[e.Parent]
			if !ok {
				return fmt.Errorf("entry %d: dangling parent %d", id, e.Parent)
			}
			if !p.Dir {
				return fmt.Errorf("entry %d: parent %d is not a directory", id, e.Parent)
			}
		}
		k := locKey{e.Parent, e.Name}
		if prev, dup := seen[k]; dup {
			return fmt.Errorf("entries %d and %d share name %q under parent %d", prev, id, e.Name, e.Parent)
		}
		seen[k] = id
	}
	for id := range s {
		if hasCycle(s, id) {
			return fmt.Errorf("entry %d: parent chain contains a cycle", id)
		}
	}
	return nil
}

func hasCycle(s Snapshot, id int) bool {
	seen := map[int]bool{}
	for {
		e, ok := s[id]
		if !ok {
			return false
		}
		if seen[id] {
			return true
		}
		seen[id] = true
		if e.Parent == 0 {
			return false
		}
		id = e.Parent
	}
}

// depthOf returns the depth of id in s: root-level entries have depth 1.
// It returns -1 if the parent chain is broken or cyclic.
func depthOf(s Snapshot, id int) int {
	depth := 0
	seen := map[int]bool{}
	for {
		e, ok := s[id]
		if !ok || seen[id] {
			return -1
		}
		seen[id] = true
		depth++
		if e.Parent == 0 {
			return depth
		}
		id = e.Parent
	}
}
