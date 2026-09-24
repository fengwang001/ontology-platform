// Package attrib attributes hotspots from a tree snapshot.
package attrib

import (
	"sort"

	"ontology/tree"
)

// Entry is a function-level attribution row.
type Entry struct {
	Frame string
	Self  int64
	Total int64
}

// Report is a flattened, reusable view of one snapshot. Queries scan and
// sort but never rebuild the tree (Rebuilds stays 0).
type Report struct {
	nodes    []*tree.SNode // preorder, parents before children
	samples  int64
	rebuilds int64
}

// NewReport flattens the snapshot in one preorder pass.
func NewReport(snap *tree.Snapshot) *Report {
	r := &Report{samples: snap.Samples}
	var walk func(n *tree.SNode)
	walk = func(n *tree.SNode) {
		for _, c := range n.Children {
			r.nodes = append(r.nodes, c)
			walk(c)
		}
	}
	walk(snap.Root)
	return r
}

// Samples returns the total sample count of the snapshot.
func (r *Report) Samples() int64 { return r.samples }

// Rebuilds returns how many times queries rebuilt the tree: always 0.
func (r *Report) Rebuilds() int64 { return r.rebuilds }

// aggregate folds nodes into function-level entries. Self sums over all
// nodes (a partition of samples); Total sums only over outermost nodes
// (no same-frame ancestor) to avoid double counting recursion. Frames
// equal to exclude are spliced out: their self moves to the nearest
// non-excluded ancestor and their subtree totals are unchanged.
func (r *Report) aggregate(exclude string) map[string]*Entry {
	effAnc := make(map[*tree.SNode]*tree.SNode, len(r.nodes))
	entries := make(map[string]*Entry)
	get := func(frame string) *Entry {
		e, ok := entries[frame]
		if !ok {
			e = &Entry{Frame: frame}
			entries[frame] = e
		}
		return e
	}
	for _, n := range r.nodes {
		var anc *tree.SNode
		if p := n.Parent; p != nil && p.Parent != nil { // skip synthetic root
			if p.Frame == exclude {
				anc = effAnc[p]
			} else {
				anc = p
			}
		}
		effAnc[n] = anc
		if n.Frame == exclude {
			if anc != nil {
				get(anc.Frame).Self += n.Self
			}
			continue
		}
		e := get(n.Frame)
		e.Self += n.Self
		outermost := true
		for a := anc; a != nil; a = effAnc[a] {
			if a.Frame == n.Frame {
				outermost = false
				break
			}
		}
		if outermost {
			e.Total += n.Total
		}
	}
	return entries
}

func sorted(entries map[string]*Entry, less func(a, b *Entry) bool) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if less(&out[i], &out[j]) {
			return true
		}
		if less(&out[j], &out[i]) {
			return false
		}
		return out[i].Frame < out[j].Frame
	})
	return out
}

// BySelf returns function entries sorted by self time, descending.
func (r *Report) BySelf() []Entry {
	return sorted(r.aggregate(""), func(a, b *Entry) bool { return a.Self > b.Self })
}

// ByTotal returns function entries sorted by total time, descending.
func (r *Report) ByTotal() []Entry {
	return sorted(r.aggregate(""), func(a, b *Entry) bool { return a.Total > b.Total })
}

// Excluding returns function entries with frame spliced out of the tree,
// sorted by total time, descending.
func (r *Report) Excluding(frame string) []Entry {
	return sorted(r.aggregate(frame), func(a, b *Entry) bool { return a.Total > b.Total })
}

// FunctionTotal returns the function-level total for frame: the sum of
// Total over its outermost nodes only.
func (r *Report) FunctionTotal(frame string) int64 {
	if e, ok := r.aggregate("")[frame]; ok {
		return e.Total
	}
	return 0
}
