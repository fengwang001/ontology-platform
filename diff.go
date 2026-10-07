package ontology

import "sort"

// SnapshotDiff is the exact difference between two snapshots extracted for
// the same requested scope. Every entry is attributable to a concrete
// main-graph change in the revision window (From.Revision, To.Revision].
type SnapshotDiff struct {
	FromRevision int64
	ToRevision   int64

	ObjectsAdded    []Object
	ObjectsRemoved  []Object
	LinksAdded      []Link
	LinksRemoved    []Link
	DanglingAdded   []DanglingLink
	DanglingRemoved []DanglingLink
}

// Empty reports whether the two snapshots are identical in content.
func (d SnapshotDiff) Empty() bool {
	return len(d.ObjectsAdded) == 0 && len(d.ObjectsRemoved) == 0 &&
		len(d.LinksAdded) == 0 && len(d.LinksRemoved) == 0 &&
		len(d.DanglingAdded) == 0 && len(d.DanglingRemoved) == 0
}

// Compare computes the content difference from prev to next. Both
// snapshots must have been extracted for the same requested scope; the
// revision labels are taken verbatim. Slices are returned sorted, so the
// diff itself is deterministic.
func Compare(prev, next *Snapshot) SnapshotDiff {
	d := SnapshotDiff{
		FromRevision:    prev.Revision,
		ToRevision:      next.Revision,
		ObjectsAdded:    diffObjects(prev.Objects, next.Objects),
		ObjectsRemoved:  diffObjects(next.Objects, prev.Objects),
		LinksAdded:      diffLinks(prev.Links, next.Links),
		LinksRemoved:    diffLinks(next.Links, prev.Links),
		DanglingAdded:   diffDangling(prev.Dangling, next.Dangling),
		DanglingRemoved: diffDangling(next.Dangling, prev.Dangling),
	}
	return d
}

// Diff is a convenience wrapper kept for the skeleton API.
func Diff(prev, next *Snapshot) SnapshotDiff { return Compare(prev, next) }

func diffObjects(base, other []Object) []Object {
	have := make(map[ID]Object, len(base))
	for _, o := range base {
		have[o.ID] = o
	}
	out := make([]Object, 0)
	for _, o := range other {
		if _, ok := have[o.ID]; !ok {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func diffLinks(base, other []Link) []Link {
	have := make(map[ID]struct{}, len(base))
	for _, l := range base {
		have[l.ID] = struct{}{}
	}
	out := make([]Link, 0)
	for _, l := range other {
		if _, ok := have[l.ID]; !ok {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type dkey struct {
	linkID                  ID
	included, excluded, src string
}

func danglingKey(r DanglingLink) dkey {
	return dkey{
		linkID:   r.LinkID,
		included: r.Included,
		excluded: r.Excluded,
		src:      r.Source.String(),
	}
}

func diffDangling(base, other []DanglingLink) []DanglingLink {
	have := make(map[interface{}]struct{}, len(base))
	for _, r := range base {
		have[danglingKey(r)] = struct{}{}
	}
	out := make([]DanglingLink, 0)
	for _, r := range other {
		if _, ok := have[danglingKey(r)]; !ok {
			out = append(out, r)
		}
	}
	sortDangling(out)
	return out
}

// Attribution binds a snapshot difference to the journaled changes in the
// window. A non-empty diff over a window with no relevant changes would
// violate the determinism contract; RelevantChanges is the authoritative
// list the diff must be explainable by.
type Attribution struct {
	Window  []int64  // [fromRevision, toRevision]
	Changes []Change // changes strictly inside the window
}

// Attribute returns the journaled changes strictly between the two
// snapshot revisions, i.e. revisions in (From, To]. When the revisions are
// equal the returned change list is empty.
func (g *Graph) Attribute(prev, next *Snapshot) Attribution {
	changes := g.Journal(prev.Revision, next.Revision)
	return Attribution{
		Window:  []int64{prev.Revision, next.Revision},
		Changes: changes,
	}
}
