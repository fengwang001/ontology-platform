package ontology

import (
	"fmt"
	"io"
	"math"
	"sort"
)

// Assignment mutates one column: either setting it to a constant or to
// the result of adding/multiplying its current value by a constant.
type Assignment struct {
	Column string
	Op     AssignOp
	Value  int64
}

type AssignOp int

const (
	AssignSet AssignOp = iota
	AssignAdd
	AssignMul
)

// RangeUpdate selects rows whose indexed column is in [Lo, Hi) and which
// additionally match every equality filter, then applies the assignments.
type RangeUpdate struct {
	Lo, Hi      int64
	Filters     map[string]int64
	Assignments []Assignment
}

// Result reports the number of rows updated and the number of index
// entries examined along the access path.
type Result struct {
	RowsUpdated     int
	EntriesExamined int
}

type Executor struct {
	table *Table
	log   io.Writer
}

func NewExecutor(table *Table, log io.Writer) *Executor {
	if log == nil {
		log = io.Discard
	}
	return &Executor{table: table, log: log}
}

// Execute runs one range-update statement serially against the table.
//
// Semantics are exactly those of "select all matching rows from a
// pre-update snapshot, then update each of them once": the selection set
// is materialized from the index before any row is mutated, so a row
// whose new key moves ahead of the scan cursor (or leaves and re-enters
// the range) is never examined or updated twice. Nothing is applied
// until every new value is computed and statement-end uniqueness has
// been verified; any failure leaves table and index byte-for-byte as
// they were before the call.
func (e *Executor) Execute(stmt RangeUpdate) (Result, error) {
	// 1. Static validation: unknown columns are rejected before range
	// bounds and before any state is touched.
	if err := e.validate(stmt); err != nil {
		e.logf(stmt, Result{}, err, "static validation failed: %v", err)
		return Result{}, err
	}

	// 2. Bounds validation: lo > hi is an illegal range.
	if stmt.Lo > stmt.Hi {
		err := ErrInvalidRange
		e.logf(stmt, Result{}, err, "rejected: lo=%d > hi=%d", stmt.Lo, stmt.Hi)
		return Result{}, err
	}

	unlock := e.table.lock()
	defer unlock()

	// 3. Access path: one ordered index range scan over [lo, hi) on the
	// pre-update snapshot. EntriesExamined is exactly the number of
	// index entries that fall in the range before the statement.
	candidateIDs := e.table.rangeIDs(stmt.Lo, stmt.Hi)
	examined := len(candidateIDs)

	// lo == hi (or an empty range for any reason) updates zero rows.
	if examined == 0 {
		res := Result{RowsUpdated: 0, EntriesExamined: 0}
		e.logf(stmt, res, nil, "empty range [%d,%d): 0 entries examined, 0 rows updated", stmt.Lo, stmt.Hi)
		return res, nil
	}

	// 4. Materialize the matching set from the snapshot. Each candidate
	// appears once in candidateIDs, so each matching row is updated at
	// most once regardless of where its new key lands.
	type target struct {
		id      int64
		newVals map[string]int64
	}
	targets := make([]target, 0, examined)
	for _, id := range candidateIDs {
		current := e.table.rows[id]
		if !matchesFilters(current, stmt.Filters) {
			continue
		}
		newVals, err := applyAssignments(current, stmt.Assignments)
		if err != nil {
			res := Result{RowsUpdated: 0, EntriesExamined: examined}
			// Nothing has been written: rows still hold their pre-update
			// values and the index is unchanged, i.e. full rollback.
			e.logf(stmt, res, err, "overflow while computing new value for row id=%d (examined=%d); no writes performed, rolled back", id, examined)
			return res, err
		}
		targets = append(targets, target{id: id, newVals: newVals})
	}

	// 5. Uniqueness is judged against the whole post-update image at
	// statement end; transient duplicates created midway do not conflict.
	if e.table.schema.UniqueIndex {
		idxCol := e.table.schema.IndexColumn
		finalKey := make(map[int64]int64, len(e.table.rows))
		for id, row := range e.table.rows {
			finalKey[id] = row[idxCol]
		}
		for _, tgt := range targets {
			if key, ok := tgt.newVals[idxCol]; ok {
				finalKey[tgt.id] = key
			}
		}
		seen := make(map[int64]int64, len(finalKey))
		for id, newKey := range finalKey {
			if holder, ok := seen[newKey]; ok {
				res := Result{RowsUpdated: 0, EntriesExamined: examined}
				e.logf(stmt, res, ErrUniqueViolation,
					"statement-end unique index conflict: rows id=%d and id=%d both end at key %d (examined=%d); no writes performed, rolled back",
					holder, id, newKey, examined)
				return res, ErrUniqueViolation
			}
			seen[newKey] = id
		}
	}

	// 6. Commit: apply targets in the snapshot's (key, id) order, one
	// index delete + one index insert per row, so every row appears
	// exactly once in the index at every instant and the key matches.
	for _, tgt := range targets {
		e.table.applyTarget(tgt.id, tgt.newVals)
	}

	res := Result{RowsUpdated: len(targets), EntriesExamined: examined}
	e.logf(stmt, res, nil,
		"committed: scanned %d pre-update index entries in [%d,%d), %d passed filters and were updated exactly once",
		examined, stmt.Lo, stmt.Hi, res.RowsUpdated)
	return res, nil
}

func (e *Executor) validate(stmt RangeUpdate) error {
	s := e.table.schema
	known := make(map[string]bool, len(s.Columns)+1)
	for _, col := range s.Columns {
		known[col] = true
	}
	for col := range stmt.Filters {
		if !known[col] {
			return fmt.Errorf("%w: %q", ErrUnknownColumn, col)
		}
	}
	for _, a := range stmt.Assignments {
		if a.Column == s.PrimaryKey || !known[a.Column] {
			return fmt.Errorf("%w: %q", ErrUnknownColumn, a.Column)
		}
	}
	return nil
}

func matchesFilters(row map[string]int64, filters map[string]int64) bool {
	for col, want := range filters {
		if row[col] != want {
			return false
		}
	}
	return true
}

func applyAssignments(row map[string]int64, assignments []Assignment) (map[string]int64, error) {
	out := make(map[string]int64, len(assignments))
	for _, a := range assignments {
		switch a.Op {
		case AssignSet:
			out[a.Column] = a.Value
		case AssignAdd:
			sum, ok := addOverflow(row[a.Column], a.Value)
			if !ok {
				return nil, fmt.Errorf("%w: %d + %d", ErrArithmeticOverflow, row[a.Column], a.Value)
			}
			out[a.Column] = sum
		case AssignMul:
			prod, ok := mulOverflow(row[a.Column], a.Value)
			if !ok {
				return nil, fmt.Errorf("%w: %d * %d", ErrArithmeticOverflow, row[a.Column], a.Value)
			}
			out[a.Column] = prod
		default:
			return nil, fmt.Errorf("ontology: unknown assignment operator %d", a.Op)
		}
	}
	return out, nil
}

func addOverflow(a, b int64) (int64, bool) {
	s := a + b
	if (a > 0 && b > 0 && s <= 0) || (a < 0 && b < 0 && s >= 0) {
		return 0, false
	}
	return s, true
}

func mulOverflow(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	// The only products that overflow are those where a factor is -1 and
	// the other factor is MinInt64 (its negation has no int64 value); the
	// division reconstruction below then stays panic-free.
	if (a == math.MinInt64 || b == math.MinInt64) && (a == -1 || b == -1) {
		return 0, false
	}
	p := a * b
	if p/b != a {
		return 0, false
	}
	return p, true
}

func (e *Executor) logf(stmt RangeUpdate, res Result, err error, decision string, args ...any) {
	if e.log == nil {
		return
	}
	fmt.Fprintf(e.log, "RangeUpdate input : range=[%d,%d) filters=%v assignments=%v\n",
		stmt.Lo, stmt.Hi, formatFilters(stmt.Filters), formatAssignments(stmt.Assignments))
	if err != nil {
		fmt.Fprintf(e.log, "RangeUpdate output: ERROR %v (rows_updated=%d entries_examined=%d)\n",
			err, res.RowsUpdated, res.EntriesExamined)
	} else {
		fmt.Fprintf(e.log, "RangeUpdate output: OK rows_updated=%d entries_examined=%d\n",
			res.RowsUpdated, res.EntriesExamined)
	}
	fmt.Fprintf(e.log, "RangeUpdate decide: %s\n", fmt.Sprintf(decision, args...))
}

func formatFilters(filters map[string]int64) string {
	if len(filters) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(filters))
	for k := range filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := "{"
	for i, k := range keys {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s=%d", k, filters[k])
	}
	return s + "}"
}

func formatAssignments(assignments []Assignment) string {
	if len(assignments) == 0 {
		return "[]"
	}
	s := "["
	for i, a := range assignments {
		if i > 0 {
			s += ", "
		}
		switch a.Op {
		case AssignSet:
			s += fmt.Sprintf("%s:=%d", a.Column, a.Value)
		case AssignAdd:
			s += fmt.Sprintf("%s+=%d", a.Column, a.Value)
		case AssignMul:
			s += fmt.Sprintf("%s*=%d", a.Column, a.Value)
		}
	}
	return s + "]"
}
