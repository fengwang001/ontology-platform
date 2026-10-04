package delta

import (
	"sort"

	"ontology/doc"
)

// Entry is one difference item: a desired leaf that reported does not match.
type Entry struct {
	Path string
	Val  *doc.Node
	MV   int64
}

// Change is the difference movement produced by one accepted update.
type Change struct {
	Upsert []Entry
	Remove []string
}

// Delta maintains the desired-vs-reported difference as a path-indexed map.
// Update work is confined to candidate leaf paths of the touched subtrees.
type Delta struct {
	entries map[string]Entry
}

func New() *Delta { return &Delta{entries: map[string]Entry{}} }

// Recompute revisits exactly the candidate paths and returns their movement.
// candidates are absolute leaf paths that may have changed (old desired
// subtree leaves of removed/replaced paths plus new ones); desired, reported
// and mv reflect the post-merge state.
func (d *Delta) Recompute(desired, reported *doc.Node, mv map[string]int64, candidates []string) Change {
	var ch Change
	for _, p := range uniqueSorted(candidates) {
		dn := doc.Lookup(desired, p)
		oldEntry, wasIn := d.entries[p]
		switch {
		case dn == nil || dn.Kind == doc.Object:
			if wasIn {
				delete(d.entries, p)
				ch.Remove = append(ch.Remove, p)
			}
		default:
			rn := doc.Lookup(reported, p)
			if doc.EqualLeaves(dn, rn) {
				if wasIn {
					delete(d.entries, p)
					ch.Remove = append(ch.Remove, p)
				}
			} else {
				e := Entry{Path: p, Val: dn, MV: mv[p]}
				if !wasIn || oldEntry.MV != e.MV || !sameVal(oldEntry.Val, e.Val) {
					d.entries[p] = e
					ch.Upsert = append(ch.Upsert, e)
				}
			}
		}
	}
	sort.Slice(ch.Upsert, func(i, j int) bool { return ch.Upsert[i].Path < ch.Upsert[j].Path })
	sort.Strings(ch.Remove)
	return ch
}

// All returns a byte-sorted copy of the complete difference.
func (d *Delta) All() []Entry {
	out := make([]Entry, 0, len(d.entries))
	for _, e := range d.entries {
		c := e
		c.Val = doc.Clone(e.Val)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	w := 1
	for i := 1; i < len(out); i++ {
		if out[i] != out[i-1] {
			out[w] = out[i]
			w++
		}
	}
	return out[:w]
}

func sameVal(a, b *doc.Node) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case doc.Int:
		return a.I == b.I
	case doc.Str:
		return a.S == b.S
	case doc.Bool:
		return a.B == b.B
	}
	return false
}
