// Package gdelta computes the per-operation group increments and renders
// changelog entries. It depends on no other package.
package gdelta

// Row is one stored row: group key G and value V.
type Row struct {
	G string
	V int64
}

// Agg is one group's materialized (SUM(v), COUNT(*)).
type Agg struct {
	Sum   int64
	Count int64
}

// OpKind selects the row-level operation.
type OpKind int

const (
	InsertOp OpKind = iota + 1
	UpdateOp
	DeleteOp
)

// Op is one row-level change. For Delete, G/V are unused.
type Op struct {
	Kind OpKind
	ID   int64
	G    string
	V    int64
}

// Change is one changelog entry. Add=false retracts the group's current row;
// Add=true writes the group's new row.
type Change struct {
	G     string
	Sum   int64
	Count int64
	Add   bool
}

// GroupDelta is one affected group's net (sum,count) increment. Groups are
// returned in changelog output order (old group before new group).
type GroupDelta struct {
	G            string
	DSum, DCount int64
}

// Insert builds an Insert op.
func Insert(id int64, g string, v int64) Op { return Op{Kind: InsertOp, ID: id, G: g, V: v} }

// Update builds an Update op replacing id's whole row with (g, v).
func Update(id int64, g string, v int64) Op { return Op{Kind: UpdateOp, ID: id, G: g, V: v} }

// Delete builds a Delete op.
func Delete(id int64) Op { return Op{Kind: DeleteOp, ID: id} }

// Deltas turns one operation (described by its old row, if any, and new row,
// if any) into per-group net increments, already ordered for output.
// A same-key Update yields ONE net delta, never a withdraw/re-add pair.
func Deltas(old, nw *Row) []GroupDelta {
	switch {
	case old == nil && nw != nil: // Insert
		return []GroupDelta{{G: nw.G, DSum: nw.V, DCount: 1}}
	case old != nil && nw == nil: // Delete
		return []GroupDelta{{G: old.G, DSum: -old.V, DCount: -1}}
	case old != nil && nw != nil: // Update
		if old.G == nw.G {
			return []GroupDelta{{G: old.G, DSum: nw.V - old.V, DCount: 0}}
		}
		return []GroupDelta{
			{G: old.G, DSum: -old.V, DCount: -1},
			{G: nw.G, DSum: nw.V, DCount: 1},
		}
	}
	return nil
}

// Apply returns the aggregate after applying d.
func Apply(a Agg, d GroupDelta) Agg {
	return Agg{Sum: a.Sum + d.DSum, Count: a.Count + d.DCount}
}

// Entries renders at most one '-' then one '+' for one group. A group whose
// (sum,count) did not change emits nothing; a group whose count drops to 0
// emits only '-'; a newly populated group emits only '+'.
func Entries(g string, before, after Agg) []Change {
	if before == after {
		return nil
	}
	var out []Change
	if before.Count > 0 {
		out = append(out, Change{G: g, Sum: before.Sum, Count: before.Count})
	}
	if after.Count > 0 {
		out = append(out, Change{G: g, Sum: after.Sum, Count: after.Count, Add: true})
	}
	return out
}
