package ontology

import "strconv"

// Constraint is a composite unique constraint over named columns.
type Constraint struct {
	Name string
	Cols []string
	// NullsEqual switches NULL semantics: when false (default, SQL-like)
	// a NULL never conflicts with anything and any row containing a NULL
	// in a constraint column is excluded from conflict detection; when
	// true, two rows that are both NULL in the constraint columns clash.
	NullsEqual bool
}

// key builds the normalized comparison key for props. ok is false when,
// under SQL NULL semantics, a constraint column is NULL and the row must
// not participate in conflict detection for this constraint.
//
// The encoding is unambiguous: each component is tagged "N" (NULL) or
// "V<len>:<bytes>" (value), so NULL, "" and any string stay distinct.
func (c Constraint) key(n Normalize, props map[string]Value) (string, bool) {
	var b []byte
	for _, col := range c.Cols {
		v, present := props[col]
		if !present || v.Null {
			if !c.NullsEqual {
				return "", false
			}
			b = append(b, 'N')
			continue
		}
		s := n.apply(v)
		b = append(b, 'V')
		b = strconv.AppendInt(b, int64(len(s)), 10)
		b = append(b, ':')
		b = append(b, s...)
	}
	return string(b), true
}

// pickCols extracts the constraint columns from a record for reporting.
func pickCols(c Constraint, props map[string]Value) map[string]Value {
	out := make(map[string]Value, len(c.Cols))
	for _, col := range c.Cols {
		v, present := props[col]
		if !present {
			v = Null()
		}
		out[col] = v
	}
	return out
}
