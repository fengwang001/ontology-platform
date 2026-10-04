// Package delta maintains the incremental difference between the desired and
// reported documents of a device shadow.
//
// A desired leaf path p is in the delta iff reported has no leaf at p with
// the same value and dynamic type. Only desired leaves may be in the delta.
package delta

import (
	"sort"

	"ontology/doc"
)

// Entry is one desired leaf that is not matched in reported (value and type).
type Entry[M any] struct {
	Path  string
	Value doc.Value
	Mv    M
}

// Change is one update's delta difference, path-sorted.
type Change[M any] struct {
	Upsert []Entry[M]
	Remove []string
}

// Set is the full delta of one device keyed by desired leaf path.
type Set[M comparable] struct {
	entries map[string]Entry[M]
}

// NewSet returns an empty delta set.
func NewSet[M comparable]() *Set[M] { return &Set[M]{entries: map[string]Entry[M]{}} }

// All returns the full delta sorted by path bytes.
func (s *Set[M]) All() []Entry[M] {
	if len(s.entries) == 0 {
		return nil
	}
	out := make([]Entry[M], 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Reconcile recomputes delta membership only around affected paths and returns
// the change against the previous set. affected is the set of leaf paths that
// the merge added, removed or changed on either side. For every affected path
// p the path itself and desired leaves under p are re-examined, so object/leaf
// interchanges are covered without scanning the whole document.
func Reconcile[M comparable](
	s *Set[M],
	desired, reported *doc.Node[M],
	affected []string,
) Change[M] {
	var ch Change[M]
	seen := map[string]struct{}{}
	for _, p := range affected {
		cands := []string{p}
		cands = append(cands, gather(desired, p, seen)...)
		for _, cand := range cands {
			if _, ok := seen[cand]; ok {
				continue
			}
			seen[cand] = struct{}{}
			segs := splitPath(cand)
			dn := doc.Find(desired, segs)
			if dn == nil || dn.Leaf == nil {
				if old, ok := s.entries[cand]; ok {
					delete(s.entries, cand)
					ch.Remove = append(ch.Remove, old.Path)
				}
				continue
			}
			ne := Entry[M]{Path: cand, Value: dn.Leaf.Value, Mv: dn.Leaf.Meta}
			rn := doc.Find(reported, segs)
			inDelta := rn == nil || rn.Leaf == nil ||
				!doc.EqualLeaf(rn.Leaf.Value, dn.Leaf.Value)
			if !inDelta {
				if _, ok := s.entries[cand]; ok {
					delete(s.entries, cand)
					ch.Remove = append(ch.Remove, cand)
				}
				continue
			}
			if old, ok := s.entries[cand]; !ok || old != ne {
				s.entries[cand] = ne
				ch.Upsert = append(ch.Upsert, ne)
			}
		}
	}
	sort.Strings(ch.Remove)
	sort.Slice(ch.Upsert, func(i, j int) bool { return ch.Upsert[i].Path < ch.Upsert[j].Path })
	return ch
}

// gather returns p itself when desired holds a leaf there, and every desired
// leaf under p when desired holds an object there. Only the subtree at p is
// walked; unrelated document regions are never visited.
func gather[M comparable](desired *doc.Node[M], p string, seen map[string]struct{}) []string {
	segs := splitPath(p)
	n := doc.Find(desired, segs)
	if n == nil {
		return nil
	}
	var out []string
	if n.Leaf != nil {
		return append(out, p)
	}
	var walk func(*doc.Node[M], string)
	walk = func(cn *doc.Node[M], cp string) {
		if cn.Leaf != nil {
			out = append(out, cp)
			return
		}
		keys := make([]string, 0, len(cn.Map))
		for k := range cn.Map {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			np := cp
			if np == "" {
				np = k
			} else {
				np = cp + "." + k
			}
			walk(cn.Map[k], np)
		}
	}
	walk(n, p)
	return out
}

func splitPath(p string) []string {
	var segs []string
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '.' {
			segs = append(segs, p[start:i])
			start = i + 1
		}
	}
	return append(segs, p[start:])
}
