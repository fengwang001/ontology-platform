// Package access evaluates subject reads with row filtering and dynamic masking.
package access

import (
	"errors"
	"sync/atomic"

	"ontology/mask"
	"ontology/policy"
)

var (
	// ErrColumnDenied means the effective mask level of a requested column is 4.
	ErrColumnDenied = errors.New("access: column denied by mask level 4")
	// ErrTypeMismatch means a where constant does not match the masked type,
	// or < / <= is applied to a masked-str column.
	ErrTypeMismatch = errors.New("access: where constant type mismatch")
)

// Reader evaluates reads against a policy store.
type Reader struct {
	store   *policy.Store
	touched atomic.Int64
}

// NewReader creates a Reader.
func NewReader(s *policy.Store) *Reader { return &Reader{store: s} }

// ResultRow is one output row with its original 1-based row number.
type ResultRow struct {
	RowNo int
	Cells []mask.Value
}

// Result is the read result.
type Result struct {
	Rows      []ResultRow
	Truncated bool
	Epoch     uint64
}

type readState struct {
	touched int
	colErr  error
}

// Read evaluates one subject read.
func (r *Reader) Read(roles []string, table string, selectCols []string,
	where policy.Predicate, limit int) (Result, error) {
	// --- 1. parameter validation (before any state lookup) ---
	if len(roles) < 1 || len(roles) > 8 ||
		!policy.ValidName(table) ||
		len(selectCols) < 1 || len(selectCols) > 32 ||
		limit < 1 || limit > 1000 ||
		len(where.Atoms) > 4 {
		return Result{}, policy.ErrInvalidArgument
	}
	roleSet := map[string]struct{}{}
	for _, role := range roles {
		if !policy.ValidName(role) || role == "*" {
			return Result{}, policy.ErrInvalidArgument
		}
		if _, dup := roleSet[role]; dup {
			return Result{}, policy.ErrInvalidArgument
		}
		roleSet[role] = struct{}{}
	}
	seenCols := map[string]struct{}{}
	for _, c := range selectCols {
		if !policy.ValidName(c) {
			return Result{}, policy.ErrInvalidArgument
		}
		if _, dup := seenCols[c]; dup {
			return Result{}, policy.ErrInvalidArgument
		}
		seenCols[c] = struct{}{}
	}
	for _, a := range where.Atoms {
		if !policy.ValidName(a.Col) || a.Const.Null {
			return Result{}, policy.ErrInvalidArgument
		}
	}

	state := readState{}
	var out Result
	r.store.View(func(v *policy.View) {
		tv := v.Table(table)
		if tv == nil {
			state.colErr = policy.ErrTableNotFound
			return
		}

		// --- column existence: select order, then where atom order ---
		selectIdx := make([]int, len(selectCols))
		for i, c := range selectCols {
			ci, ok := tv.ColumnIndex(c)
			if !ok {
				state.colErr = policy.ErrColumnNotFound
				return
			}
			selectIdx[i] = ci
		}
		whereIdx := make([]int, len(where.Atoms))
		for i, a := range where.Atoms {
			ci, ok := tv.ColumnIndex(a.Col)
			if !ok {
				state.colErr = policy.ErrColumnNotFound
				return
			}
			whereIdx[i] = ci
		}

		// --- effective levels with per-column caching (touched counted once) ---
		cols := tv.Columns()
		levels := map[int]mask.Level{}
		levelOf := func(colIdx int) mask.Level {
			if lv, ok := levels[colIdx]; ok {
				return lv
			}
			lv, n := tv.MaskLevel(colIdx, roles)
			state.touched += n
			levels[colIdx] = lv
			return lv
		}

		// --- denied columns: select first, then where ---
		for _, ci := range selectIdx {
			if levelOf(ci) == mask.LevelDeny {
				state.colErr = ErrColumnDenied
				return
			}
		}
		// --- type mismatches against masked types, in where atom order ---
		for i, ci := range whereIdx {
			lv := levelOf(ci)
			if lv == mask.LevelDeny {
				state.colErr = ErrColumnDenied
				return
			}
			mt := maskedType(cols[ci].Type, lv)
			if where.Atoms[i].Const.Type != mt {
				state.colErr = ErrTypeMismatch
				return
			}
			// A level-3 column keeps its underlying type; its atoms are simply
			// false at evaluation. Only masked-str columns reject < / <=.
			if lv != mask.LevelNull && mt == mask.TypeStr &&
				(where.Atoms[i].Op == policy.OpLt || where.Atoms[i].Op == policy.OpLe) {
				state.colErr = ErrTypeMismatch
				return
			}
		}

		pols := tv.ApplicablePolicies(roleSet)
		state.touched += len(pols)

		out.Epoch = v.Epoch()
		for rowNo, row := range tv.Rows() {
			if !rowVisible(row, cols, pols) || !whereHits(row, whereIdx, where, levels) {
				continue
			}
			if len(out.Rows) == limit {
				out.Truncated = true
				break
			}
			cells := make([]mask.Value, len(selectIdx))
			for i, ci := range selectIdx {
				cells[i] = mask.Apply(row[ci], levels[ci])
			}
			out.Rows = append(out.Rows, ResultRow{RowNo: rowNo + 1, Cells: cells})
		}
	})

	r.touched.Store(int64(state.touched))
	if state.colErr != nil {
		return Result{}, state.colErr
	}
	return out, nil
}

// Touched returns the examined-rule count of the last Read call.
func (r *Reader) Touched() int { return int(r.touched.Load()) }

func whereHits(row []mask.Value, whereIdx []int, where policy.Predicate,
	levels map[int]mask.Level) bool {
	for i, a := range where.Atoms {
		lv := levels[whereIdx[i]]
		if lv == mask.LevelNull {
			return false
		}
		if !policy.EvalAtom(mask.Apply(row[whereIdx[i]], lv), a) {
			return false
		}
	}
	return true
}

func maskedType(t mask.Type, lv mask.Level) mask.Type {
	if lv == mask.LevelHash {
		return mask.TypeStr
	}
	return t
}

// rowVisible: at least one applicable permissive hits AND every applicable
// restrictive hits. No applicable permissive means default deny.
func rowVisible(row []mask.Value, cols []policy.Column, pols []policy.RowPolicy) bool {
	sawPermissive := false
	sawPermissiveHit := false
	for _, p := range pols {
		hit := evalPolicy(row, cols, p)
		switch {
		case p.Kind == policy.Restrictive && !hit:
			return false
		case p.Kind == policy.Permissive:
			sawPermissive = true
			if hit {
				sawPermissiveHit = true
			}
		}
	}
	return sawPermissive && sawPermissiveHit
}

func evalPolicy(row []mask.Value, cols []policy.Column, p policy.RowPolicy) bool {
	for _, a := range p.Pred.Atoms {
		ci := columnIndex(cols, a.Col)
		if ci < 0 || !policy.EvalAtom(row[ci], a) {
			return false
		}
	}
	return true
}

func columnIndex(cols []policy.Column, name string) int {
	for i, c := range cols {
		if c.Name == name {
			return i
		}
	}
	return -1
}
