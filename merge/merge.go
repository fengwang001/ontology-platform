package merge

import (
	"fmt"
	"strings"
)

// keyState accumulates the in-batch state for one primary key.
type keyState struct {
	key       string
	existsPre bool // existed in the pre-batch table
	isInsert  bool // first event for the key was an insert

	// insertRow is the full inserted row; it evolves as updates are folded in
	// (insert + update stays an insert).
	insertRow Row

	// sim is the simulated current row used to validate before-images of
	// subsequent in-batch events against the actual evolving state.
	sim Row

	// update accumulation (only when !isInsert)
	updCols ColumnSet // union of updated columns
	changed Row       // col -> latest value (last write wins)
	before  Row       // col -> first in-batch before-image value (the original)
}

// mergeBatch is the pure, lock-free merge engine. It validates the whole batch
// against the pre-batch rows and, if valid, produces the merged result. It
// never mutates pre. On any violation it returns a *BatchError and no result.
func mergeBatch(schema ColumnSet, pre map[string]Row, events []Event, log Logger) (*Result, *BatchError) {
	if log == nil {
		log = noopLogger{}
	}
	log.Logf("merge: start batch events=%d schema=%v preRows=%d", len(events), schema.Sorted(), len(pre))

	states := make(map[string]*keyState)
	order := make([]string, 0, len(events))

	stateFor := func(key string, ix int) *keyState {
		st, ok := states[key]
		if !ok {
			st = &keyState{key: key}
			if preRow, exists := pre[key]; exists {
				st.existsPre = true
				st.sim = preRow.Clone()
			}
			states[key] = st
			order = append(order, key)
		}
		return st
	}

	for ix, ev := range events {
		st := stateFor(ev.Key, ix)
		log.Logf("event[%d]: kind=%s key=%q columns=%s before=%s",
			ix, ev.Kind, ev.Key, formatRow(ev.Columns), formatRow(ev.Before))

		switch ev.Kind {
		case EventInsert:
			if berr := checkInsertColumns(schema, ev, ix); berr != nil {
				log.Logf("event[%d]: reject: %s", ix, berr.Detail)
				return nil, berr
			}
			if st.existsPre || st.isInsert {
				berr := &BatchError{Kind: ErrKeyExists, Key: ev.Key, EventIx: ix,
					Detail: "insert targets a key that already exists"}
				log.Logf("event[%d]: reject: %s", ix, berr.Detail)
				return nil, berr
			}
			st.isInsert = true
			st.insertRow = fullRow(schema, ev.Columns)
			st.sim = st.insertRow.Clone()
			log.Logf("event[%d]: insert accepted key=%q row=%s", ix, ev.Key, formatRow(st.insertRow))

		case EventUpdate:
			if berr := checkUpdateColumns(schema, ev, ix); berr != nil {
				log.Logf("event[%d]: reject: %s", ix, berr.Detail)
				return nil, berr
			}
			if !st.existsPre && !st.isInsert {
				berr := &BatchError{Kind: ErrKeyNotFound, Key: ev.Key, EventIx: ix,
					Detail: "update targets a key that does not exist"}
				log.Logf("event[%d]: reject: %s", ix, berr.Detail)
				return nil, berr
			}
			// Before-image value check against the actual current (simulated) row.
			for _, col := range ColumnsOf(ev.Before).Sorted() {
				actual := st.sim[col]
				if !actual.Equal(ev.Before[col]) {
					berr := &BatchError{Kind: ErrBeforeImageMismatch, Key: ev.Key, Column: col, EventIx: ix,
						Detail: fmt.Sprintf("before-image %s does not match actual %s", ev.Before[col], actual)}
					log.Logf("event[%d]: reject: column=%s %s", ix, col, berr.Detail)
					return nil, berr
				}
			}

			if st.isInsert {
				// insert + update => still an insert; fold values in.
				for col, v := range ev.Columns {
					st.insertRow[col] = v
					st.sim[col] = v
				}
				log.Logf("event[%d]: folded update into insert key=%q row=%s", ix, ev.Key, formatRow(st.insertRow))
			} else {
				if st.updCols == nil {
					st.updCols = ColumnSet{}
					st.changed = Row{}
					st.before = Row{}
				}
				for col, v := range ev.Columns {
					if !st.updCols.Has(col) {
						st.updCols[col] = struct{}{}
						st.before[col] = ev.Before[col] // first in-batch before value
					}
					st.changed[col] = v // latest value wins
					st.sim[col] = v
				}
				log.Logf("event[%d]: merged update key=%q changed=%s before=%s", ix, ev.Key, formatRow(st.changed), formatRow(st.before))
			}
		}
	}

	// Build the merged output in first-appearance order.
	res := &Result{Changes: make([]Change, 0, len(order))}
	for _, key := range order {
		st := states[key]
		switch {
		case st.isInsert:
			// Inserts are emitted in full; no pruning.
			res.Changes = append(res.Changes, Change{Key: key, Kind: EventInsert, Columns: st.insertRow.Clone()})
			log.Logf("merge: emit insert key=%q row=%s", key, formatRow(st.insertRow))
		case len(st.updCols) > 0:
			// Prune columns whose latest value equals the original value.
			surviving := Row{}
			survivingBefore := Row{}
			pruned := []string{}
			for _, col := range st.updCols.Sorted() {
				latest := st.changed[col]
				orig := st.before[col]
				if latest.Equal(orig) {
					pruned = append(pruned, col)
					continue
				}
				surviving[col] = latest
				survivingBefore[col] = orig
			}
			if len(surviving) == 0 {
				log.Logf("merge: drop key=%q (all updates no-op; pruned=%v)", key, pruned)
				continue
			}
			if len(pruned) > 0 {
				log.Logf("merge: prune no-op columns key=%q pruned=%v", key, pruned)
			}
			res.Changes = append(res.Changes, Change{Key: key, Kind: EventUpdate, Columns: surviving, Before: survivingBefore})
			log.Logf("merge: emit update key=%q columns=%s before=%s", key, formatRow(surviving), formatRow(survivingBefore))
		default:
			log.Logf("merge: drop key=%q (no surviving change)", key)
		}
	}
	log.Logf("merge: done outputKeys=%v changes=%d", res.Keys(), len(res.Changes))
	return res, nil
}

// applyResult applies a merged result to rows (mutating). Callers must hold
// the appropriate lock.
func applyResult(rows map[string]Row, res *Result) {
	for _, ch := range res.Changes {
		switch ch.Kind {
		case EventInsert:
			rows[ch.Key] = ch.Columns.Clone()
		case EventUpdate:
			r := rows[ch.Key]
			if r == nil {
				r = Row{}
				rows[ch.Key] = r
			}
			for col, v := range ch.Columns {
				r[col] = v
			}
		}
	}
}

// checkInsertColumns validates that an insert provides exactly the schema
// columns, all present (null or string, never absent).
func checkInsertColumns(schema ColumnSet, ev Event, ix int) *BatchError {
	cols := ColumnsOf(ev.Columns)
	if !cols.EqualSet(schema) {
		return &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, EventIx: ix,
			Detail: fmt.Sprintf("insert must provide exactly the schema columns %v, got %v", schema.Sorted(), cols.Sorted())}
	}
	for _, c := range schema.Sorted() {
		if ev.Columns[c].IsAbsent() {
			return &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, Column: c, EventIx: ix,
				Detail: "insert column value must be present (null or string), not absent"}
		}
	}
	return nil
}

// checkUpdateColumns validates that an update changes at least one known
// column, all values present, and that the before-image column set equals the
// changed column set.
func checkUpdateColumns(schema ColumnSet, ev Event, ix int) *BatchError {
	if len(ev.Columns) == 0 {
		return &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, EventIx: ix,
			Detail: "update must change at least one column"}
	}
	for _, c := range ColumnsOf(ev.Columns).Sorted() {
		if !schema.Has(c) {
			return &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, Column: c, EventIx: ix,
				Detail: "unknown column (not in schema)"}
		}
		if ev.Columns[c].IsAbsent() {
			return &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, Column: c, EventIx: ix,
				Detail: "update column value must be present (null or string), not absent"}
		}
	}
	if !ColumnsOf(ev.Before).EqualSet(ColumnsOf(ev.Columns)) {
		return &BatchError{Kind: ErrIllegalColumn, Key: ev.Key, EventIx: ix,
			Detail: fmt.Sprintf("before-image columns %v must equal changed columns %v",
				ColumnsOf(ev.Before).Sorted(), ColumnsOf(ev.Columns).Sorted())}
	}
	return nil
}

// fullRow builds a complete row from an insert's columns (schema-validated).
func fullRow(schema ColumnSet, cols Row) Row {
	out := make(Row, len(schema))
	for c := range schema {
		out[c] = cols[c]
	}
	return out
}

// formatRow renders a row deterministically for logs.
func formatRow(r Row) string {
	if len(r) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{")
	for i, c := range ColumnsOf(r).Sorted() {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s=%s", c, r[c])
	}
	b.WriteString("}")
	return b.String()
}
