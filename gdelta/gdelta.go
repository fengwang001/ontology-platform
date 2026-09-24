// Package gdelta computes the group delta of a single operation and
// renders the changelog entries for the affected groups.
package gdelta

// Kind is the operation kind.
type Kind int

const (
	Insert Kind = iota
	Update
	Delete
)

// Op is one upstream row-level change.
type Op struct {
	Kind Kind
	ID   int64
	G    string
	V    int64
}

// Row is a stored row: group key and value.
type Row struct {
	G string
	V int64
}

// Agg is a group's materialized aggregate.
type Agg struct {
	Sum   int64
	Count int64
}

// Change is one changelog entry: retract (-) or add (+) of a group row.
type Change struct {
	Retract bool
	G       string
	Sum     int64
	Count   int64
}

// Entries computes the changelog entries for one operation, given the
// old row (nil if none), the new row (nil if none), and the pre-op
// aggregates of the old and new rows' groups (zero if the group is absent).
// Group-key-changing updates emit the old group's entries first; within a
// group the retract precedes the add; a same-key update is one net change.
func Entries(old, new *Row, ago, agn Agg) []Change {
	var out []Change
	if old != nil && new != nil && old.G == new.G {
		return group(out, old.G, ago, Agg{Sum: ago.Sum - old.V + new.V, Count: ago.Count})
	}
	if old != nil {
		out = group(out, old.G, ago, Agg{Sum: ago.Sum - old.V, Count: ago.Count - 1})
	}
	if new != nil {
		out = group(out, new.G, agn, Agg{Sum: agn.Sum + new.V, Count: agn.Count + 1})
	}
	return out
}

// group appends the entries for one group changing from before to after.
// An unchanged group emits nothing; a group is retracted only if it was in
// the view (count > 0) and added only if it stays in the view.
func group(out []Change, g string, before, after Agg) []Change {
	if before == after {
		return out
	}
	if before.Count > 0 {
		out = append(out, Change{Retract: true, G: g, Sum: before.Sum, Count: before.Count})
	}
	if after.Count > 0 {
		out = append(out, Change{G: g, Sum: after.Sum, Count: after.Count})
	}
	return out
}
