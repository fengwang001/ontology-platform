package indexorder

func validDirection(d Direction) bool {
	return d == ASC || d == DESC
}

func validNulls(n NullsOrder) bool {
	return n == NullsFirst || n == NullsLast
}

func validateColumns(cols []ColumnItem) error {
	for _, c := range cols {
		if c.Column == "" {
			return ErrEmptyColumnName
		}
	}
	for _, c := range cols {
		if !validDirection(c.Dir) {
			return ErrInvalidDirection
		}
	}
	for _, c := range cols {
		if !validNulls(c.Nulls) {
			return ErrInvalidNullsOrder
		}
	}
	seen := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		if _, ok := seen[c.Column]; ok {
			return ErrDuplicateColumn
		}
		seen[c.Column] = struct{}{}
	}
	return nil
}

// normalizeOrder drops ORDER BY items whose column is equality-constrained,
// then keeps only the first occurrence of each remaining column.
func normalizeOrder(eq map[string]struct{}, order []ColumnItem) []ColumnItem {
	need := make([]ColumnItem, 0, len(order))
	seen := make(map[string]struct{}, len(order))
	for _, item := range order {
		if _, isEq := eq[item.Column]; isEq {
			continue
		}
		if _, dup := seen[item.Column]; dup {
			continue
		}
		seen[item.Column] = struct{}{}
		need = append(need, item)
	}
	return need
}

func invertDirection(d Direction) Direction {
	if d == ASC {
		return DESC
	}
	return ASC
}

func invertNulls(n NullsOrder) NullsOrder {
	if n == NullsFirst {
		return NullsLast
	}
	return NullsFirst
}

// satisfies walks the index columns in order. Equality-constrained columns
// are skipped; every other index column must match the next needed item
// exactly (forward) or with both direction and nulls inverted (backward).
func satisfies(idx Index, eq map[string]struct{}, need []ColumnItem, scan ScanDirection) bool {
	matched := 0
	for _, c := range idx.Column {
		if matched == len(need) {
			return true
		}
		if _, isEq := eq[c.Column]; isEq {
			continue
		}
		n := need[matched]
		if c.Column != n.Column {
			return false
		}
		switch scan {
		case ScanForward:
			if c.Dir != n.Dir || c.Nulls != n.Nulls {
				return false
			}
		case ScanBackward:
			if c.Dir != invertDirection(n.Dir) || c.Nulls != invertNulls(n.Nulls) {
				return false
			}
		}
		matched++
	}
	return matched == len(need)
}
