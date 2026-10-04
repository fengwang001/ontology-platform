// Package access evaluates subject reads: row filtering on plaintext, column
// masking on post-mask values, and the ordered rejection rules.
package access

import (
	"fmt"

	"ontology/mask"
	"ontology/policy"
)

// Reader combines the policy store with read-time evaluation.
type Reader struct{ store *policy.Store }

// ResultRow is one returned row: 1-based row number and masked cells in the
// requested select column order.
type ResultRow struct {
	RowNo int
	Cells []policy.Cell
}

// ReadResult is the full read outcome.
type ReadResult struct {
	Rows      []ResultRow
	Truncated bool
	Epoch     int
	touched   int // applicable row policies + subject mask rules examined
}

// NewReader wraps a policy store.
func NewReader(store *policy.Store) *Reader { return &Reader{store: store} }

// Read validates the request, takes one epoch-consistent snapshot, evaluates
// row visibility on plaintext and where on masked values.
func (r *Reader) Read(roles []string, table string, selectCols []string, where []policy.Atom, limit int) (*ReadResult, error) {
	// Phase 1: argument validity (before any existence lookup).
	if len(roles) < 1 || len(roles) > 8 || len(selectCols) < 1 || len(selectCols) > 32 ||
		limit < 1 || limit > 1000 || len(where) > 4 || !validName(table) {
		return nil, fmt.Errorf("read %q: %w", table, policy.ErrInvalidArgument)
	}
	roleSeen := map[string]bool{}
	for _, ro := range roles {
		if !validName(ro) || roleSeen[ro] {
			return nil, fmt.Errorf("read %q: %w", table, policy.ErrInvalidArgument)
		}
		roleSeen[ro] = true
	}
	colSeen := map[string]bool{}
	for _, c := range selectCols {
		if !validName(c) || colSeen[c] {
			return nil, fmt.Errorf("read %q: %w", table, policy.ErrInvalidArgument)
		}
		colSeen[c] = true
	}
	for _, a := range where {
		if !validName(a.Col) || !validOp(a.Op) || a.Constant == nil || !validCell(a.Constant) {
			return nil, fmt.Errorf("read %q: %w", table, policy.ErrInvalidArgument)
		}
	}

	// Phase 2: table existence (needed to resolve every column below).
	needed := make(map[string]bool, len(selectCols)+len(where))
	for _, c := range selectCols {
		needed[c] = true
	}
	for _, a := range where {
		needed[a.Col] = true
	}
	touched := 0
	snap, err := r.store.SnapshotRead(table, roles, needed, &touched)
	if err != nil {
		return nil, err
	}
	colIdx := make(map[string]int, len(snap.Columns))
	for i, c := range snap.Columns {
		colIdx[c.Name] = i
	}

	// Phase 3: column existence, select columns first then where atoms.
	for _, c := range selectCols {
		if _, ok := colIdx[c]; !ok {
			return nil, fmt.Errorf("read %q col %q: %w", table, c, policy.ErrColumnNotFound)
		}
	}
	for _, a := range where {
		if _, ok := colIdx[a.Col]; !ok {
			return nil, fmt.Errorf("read %q col %q: %w", table, a.Col, policy.ErrColumnNotFound)
		}
	}

	// Effective per-column mask level: minimum over every subject role; a
	// role without an explicit rule participates with the column default.
	levelOf := make(map[string]int, len(needed))
	explicit := map[string]map[string]int{} // col -> role -> level
	for _, m := range snap.Masks {
		if explicit[m.Col] == nil {
			explicit[m.Col] = map[string]int{}
		}
		explicit[m.Col][m.Role] = m.Level
	}
	for name := range needed {
		def := snap.Columns[colIdx[name]].Def
		l := mask.Denied + 1 // above the maximum; roles is never empty
		for _, ro := range roles {
			rl := def
			if rm, ok := explicit[name][ro]; ok {
				rl = rm
			}
			if rl < l {
				l = rl
			}
		}
		levelOf[name] = l
	}

	// Phase 4: denied columns, select columns first then where atoms.
	for _, c := range selectCols {
		if levelOf[c] == mask.Denied {
			return nil, fmt.Errorf("read %q col %q: %w", table, c, policy.ErrColumnDenied)
		}
	}
	for _, a := range where {
		if levelOf[a.Col] == mask.Denied {
			return nil, fmt.Errorf("read %q col %q: %w", table, a.Col, policy.ErrColumnDenied)
		}
	}

	// Phase 5: where constant/operator type vs. the post-mask type.
	// Level 2 turns int cells into str; level 3 keeps the declared type for
	// validation even though every atom evaluates false (always NULL).
	for _, a := range where {
		ct := snap.Columns[colIdx[a.Col]].Type
		if levelOf[a.Col] == mask.Hashed {
			ct = policy.TypeStr
		}
		if cellType(a.Constant) != ct || !opAllowed(ct, a.Op) {
			return nil, fmt.Errorf("read %q where %q: %w", table, a.Col, policy.ErrTypeMismatch)
		}
	}

	// Evaluation under the single snapshot epoch.
	var out []ResultRow
	truncated := false
	for rowNo, row := range snap.Rows {
		if !rowVisible(snap.Policies, snap.Columns, row) {
			continue
		}
		maskedRow := make([]policy.Cell, len(snap.Columns))
		for i, c := range snap.Columns {
			maskedRow[i] = mask.Apply(row[i], c.Type, levelOf[c.Name])
		}
		if !whereHits(where, colIdx, snap.Columns, levelOf, maskedRow) {
			continue
		}
		if len(out) >= limit {
			truncated = true
			break
		}
		cells := make([]policy.Cell, len(selectCols))
		for i, c := range selectCols {
			cells[i] = maskedRow[colIdx[c]]
		}
		out = append(out, ResultRow{RowNo: rowNo + 1, Cells: cells})
	}

	return &ReadResult{Rows: out, Truncated: truncated, Epoch: snap.Epoch, touched: touched}, nil
}

// rowVisible: at least one applicable permissive hits and every applicable
// restrictive hits, evaluated on plaintext. Applicability is pre-filtered in
// the snapshot (role is "*" or in the subject), so every policy here applies.
func rowVisible(pols []policy.RowPolicy, cols []policy.Column, row []policy.Cell) bool {
	anyPermissive := false
	for _, p := range pols {
		hit := predHits(p.Pred, cols, row)
		switch p.Kind {
		case policy.Permissive:
			if hit {
				anyPermissive = true
			}
		case policy.Restrictive:
			if !hit {
				return false
			}
		}
	}
	return anyPermissive
}

// predHits is conjunction evaluation on plaintext cells; NULL makes any atom
// (including !=) false.
func predHits(atoms []policy.Atom, cols []policy.Column, row []policy.Cell) bool {
	for _, a := range atoms {
		if !atomHits(a, colIndexOf(cols, a.Col), row, false) {
			return false
		}
	}
	return true
}

// whereHits evaluates where on masked cells. A level-3 (always NULL) column
// makes every atom on it false.
func whereHits(atoms []policy.Atom, idx map[string]int, cols []policy.Column, levelOf map[string]int, row []policy.Cell) bool {
	for _, a := range atoms {
		if levelOf[a.Col] == mask.Nulled {
			return false
		}
		if !atomHits(a, idx[a.Col], row, true) {
			return false
		}
	}
	return true
}

func atomHits(a policy.Atom, ci int, row []policy.Cell, masked bool) bool {
	v := row[ci]
	if v == nil {
		return false
	}
	switch a.Op {
	case policy.OpEq:
		return cellEq(v, a.Constant)
	case policy.OpNe:
		return !cellEq(v, a.Constant)
	case policy.OpLt:
		return v.(int64) < a.Constant.(int64)
	case policy.OpLe:
		return v.(int64) <= a.Constant.(int64)
	default:
		return false
	}
}

func cellEq(a, b policy.Cell) bool {
	switch x := a.(type) {
	case int64:
		y, ok := b.(int64)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	default:
		return false
	}
}

func colIndexOf(cols []policy.Column, name string) int {
	for i := range cols {
		if cols[i].Name == name {
			return i
		}
	}
	return -1
}

func validName(n string) bool { return len(n) >= 1 && len(n) <= 64 }

func validOp(o policy.Op) bool { return o >= policy.OpEq && o <= policy.OpLe }

func validCell(c policy.Cell) bool {
	switch c.(type) {
	case int64, string:
		return true
	default:
		return false
	}
}

func cellType(c policy.Cell) policy.ColType {
	if _, ok := c.(int64); ok {
		return policy.TypeInt
	}
	return policy.TypeStr
}

func opAllowed(ct policy.ColType, o policy.Op) bool {
	if ct == policy.TypeInt {
		return o >= policy.OpEq && o <= policy.OpLe
	}
	return o == policy.OpEq || o == policy.OpNe
}
