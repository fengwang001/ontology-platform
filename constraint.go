package ontology

import "strings"

// Constraint is a composite unique constraint over a set of
// property columns.
type Constraint struct {
	// Name identifies the constraint in conflict reports.
	Name string
	// Columns are the properties the constraint covers.
	Columns []string
	// NullsEqual selects NULL semantics. False (the default) is
	// SQL semantics: NULL never conflicts with anything, and a
	// composite key containing any NULL column does not
	// participate in conflict detection at all. True treats NULL
	// as a regular value: two NULLs in the same column conflict.
	NullsEqual bool
}

// normValues returns the normalized key values of props for this
// constraint. It reports ok=false when the key does not
// participate in conflict detection (SQL NULL semantics with at
// least one NULL column). A missing property counts as NULL.
func (c Constraint) normValues(props map[string]Value, norm NormOptions) (vals []string, ok bool) {
	vals = make([]string, len(c.Columns))
	for i, col := range c.Columns {
		v, present := props[col]
		if !present || v.IsNull() {
			if !c.NullsEqual {
				return nil, false
			}
			vals[i] = nullKey
			continue
		}
		vals[i] = norm.Normalize(v.String())
	}
	return vals, true
}

// nullKey marks a NULL column inside a normalized key. It cannot
// collide with any real value because real values are prefixed
// with \x01 in the encoded key.
const nullKey = "\x00NULL"

// encodeKey packs normalized column values into one index key.
// Each value is length-agnostic: a \x01 prefix separates columns,
// so NULL (\x00...) and the empty string stay distinct.
func encodeKey(vals []string) string {
	var b strings.Builder
	for _, v := range vals {
		b.WriteByte('\x01')
		b.WriteString(v)
	}
	return b.String()
}
