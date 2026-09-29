package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
)

func testSchema(unique bool) Schema {
	return Schema{
		Columns:     []string{"id", "v", "tag"},
		PrimaryKey:  "id",
		IndexColumn: "v",
		UniqueIndex: unique,
	}
}

func mustInsert(t *testing.T, tbl *Table, id, v, tag int64) {
	t.Helper()
	if err := tbl.Insert(id, map[string]int64{"id": id, "v": v, "tag": tag}); err != nil {
		t.Fatalf("insert id=%d: %v", id, err)
	}
}

func valuesByID(tbl *Table) map[int64]map[string]int64 {
	out := make(map[int64]map[string]int64)
	for _, row := range tbl.Snapshot() {
		out[row.ID] = row.Values
	}
	return out
}

func dumpRows(m map[int64]map[string]int64) string { return fmt.Sprint(m) }

// assertIndexInvariant checks that every row appears exactly once in the
// index and the indexed key matches the row value.
func assertIndexInvariant(t *testing.T, tbl *Table) {
	t.Helper()
	tbl.mu.RLock()
	defer tbl.mu.RUnlock()
	if len(tbl.index) != len(tbl.rows) {
		t.Fatalf("index has %d entries but table has %d rows", len(tbl.index), len(tbl.rows))
	}
	seen := make(map[int64]int, len(tbl.rows))
	for _, e := range tbl.index {
		if got := tbl.rows[e.id][tbl.schema.IndexColumn]; got != e.key {
			t.Fatalf("stale index entry for id=%d: index key=%d row value=%d", e.id, e.key, got)
		}
		if _, ok := tbl.rows[e.id]; !ok {
			t.Fatalf("index references missing row id=%d", e.id)
		}
		seen[e.id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("row id=%d appears %d times in index", id, n)
		}
	}
}

func assertV(t *testing.T, rows map[int64]map[string]int64, id, wantV int64) {
	t.Helper()
	if got := rows[id]["v"]; got != wantV {
		t.Fatalf("row id=%d: v=%d want %d", id, got, wantV)
	}
}

// multiplyByTwoMovesAhead: doubling moves each key ahead of the scan
// cursor. A dynamic scan would revisit rows; snapshot semantics update
// every pre-update match exactly once.
func TestMultiplyByTwoMovesAhead(t *testing.T) {
	tbl := NewTable(testSchema(false))
	for id := int64(1); id <= 5; id++ {
		mustInsert(t, tbl, id, id, 0)
	}
	exec := NewExecutor(tbl, os.Stdout)

	res, err := exec.Execute(RangeUpdate{Lo: 1, Hi: 10,
		Assignments: []Assignment{{Column: "v", Op: AssignMul, Value: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.EntriesExamined != 5 {
		t.Fatalf("entries examined=%d want 5 (pre-update range population)", res.EntriesExamined)
	}
	if res.RowsUpdated != 5 {
		t.Fatalf("rows updated=%d want 5", res.RowsUpdated)
	}
	rows := valuesByID(tbl)
	assertV(t, rows, 1, 2)
	assertV(t, rows, 2, 4)
	assertV(t, rows, 3, 6)
	assertV(t, rows, 4, 8)
	assertV(t, rows, 5, 10)
	assertIndexInvariant(t, tbl)
}

func TestRowLeavesRangeAndIsNotReapplied(t *testing.T) {
	tbl := NewTable(testSchema(false))
	mustInsert(t, tbl, 1, 3, 0) // would be 3*2=6 (out of [0,5)); never 12
	exec := NewExecutor(tbl, os.Stdout)
	res, err := exec.Execute(RangeUpdate{Lo: 0, Hi: 5,
		Assignments: []Assignment{{Column: "v", Op: AssignMul, Value: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.EntriesExamined != 1 || res.RowsUpdated != 1 {
		t.Fatalf("got %+v want examined=1 updated=1", res)
	}
	assertV(t, valuesByID(tbl), 1, 6)
	assertIndexInvariant(t, tbl)
}

func TestUniqueIncrementAll(t *testing.T) {
	tbl := NewTable(testSchema(true))
	for id := int64(1); id <= 5; id++ {
		mustInsert(t, tbl, id, id, 0)
	}
	exec := NewExecutor(tbl, os.Stdout)
	res, err := exec.Execute(RangeUpdate{Lo: 1, Hi: 6,
		Assignments: []Assignment{{Column: "v", Op: AssignAdd, Value: 1}}})
	if err != nil {
		t.Fatalf("shift up on unique index must tolerate transient dupes: %v", err)
	}
	if res.RowsUpdated != 5 || res.EntriesExamined != 5 {
		t.Fatalf("got %+v want 5/5", res)
	}
	rows := valuesByID(tbl)
	for id := int64(1); id <= 5; id++ {
		assertV(t, rows, id, id+1)
	}
	assertIndexInvariant(t, tbl)
}

func TestUniqueDecrementAll(t *testing.T) {
	tbl := NewTable(testSchema(true))
	for id := int64(1); id <= 5; id++ {
		mustInsert(t, tbl, id, id, 0)
	}
	exec := NewExecutor(tbl, os.Stdout)
	res, err := exec.Execute(RangeUpdate{Lo: 1, Hi: 6,
		Assignments: []Assignment{{Column: "v", Op: AssignAdd, Value: -1}}})
	if err != nil {
		t.Fatalf("shift down on unique index must tolerate transient dupes: %v", err)
	}
	if res.RowsUpdated != 5 || res.EntriesExamined != 5 {
		t.Fatalf("got %+v want 5/5", res)
	}
	rows := valuesByID(tbl)
	assertV(t, rows, 1, 0)
	assertV(t, rows, 5, 4)
	assertIndexInvariant(t, tbl)
}

func TestSubtractMovesBelowLowerBound(t *testing.T) {
	tbl := NewTable(testSchema(false))
	mustInsert(t, tbl, 1, 2, 0) // 2-10 = -8 leaves [0,10) below lo
	mustInsert(t, tbl, 2, 20, 0)
	exec := NewExecutor(tbl, os.Stdout)
	res, err := exec.Execute(RangeUpdate{Lo: 0, Hi: 10,
		Assignments: []Assignment{{Column: "v", Op: AssignAdd, Value: -10}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsUpdated != 1 || res.EntriesExamined != 1 {
		t.Fatalf("got %+v want examined=1 updated=1", res)
	}
	rows := valuesByID(tbl)
	assertV(t, rows, 1, -8) // revisited rows would have become -18
	assertV(t, rows, 2, 20)
	assertIndexInvariant(t, tbl)
}

func TestUniqueConflictRollsBack(t *testing.T) {
	tbl := NewTable(testSchema(true))
	for id := int64(1); id <= 5; id++ {
		mustInsert(t, tbl, id, id, 0)
	}
	before := valuesByID(tbl)
	exec := NewExecutor(tbl, os.Stdout)

	_, err := exec.Execute(RangeUpdate{Lo: 2, Hi: 6,
		Assignments: []Assignment{{Column: "v", Op: AssignSet, Value: 1}}})
	if !errors.Is(err, ErrUniqueViolation) {
		t.Fatalf("want ErrUniqueViolation, got %v", err)
	}
	after := valuesByID(tbl)
	if dumpRows(after) != dumpRows(before) {
		t.Fatalf("rollback incomplete:\nbefore=%v\nafter =%v", before, after)
	}
	assertIndexInvariant(t, tbl)
}

// The 7th examined row overflows; the first six must roll back too.
func TestOverflowOnSeventhRollsBackSix(t *testing.T) {
	tbl := NewTable(testSchema(false))
	for id := int64(1); id <= 7; id++ {
		mustInsert(t, tbl, id, id, 0)
	}
	before := valuesByID(tbl)
	exec := NewExecutor(tbl, os.Stdout)

	// c = MaxInt64/7 + 1: 1*c .. 6*c stay in range, 7*c overflows.
	c := int64(math.MaxInt64/7 + 1)
	res, err := exec.Execute(RangeUpdate{Lo: 1, Hi: 8,
		Assignments: []Assignment{{Column: "v", Op: AssignMul, Value: c}}})
	if !errors.Is(err, ErrArithmeticOverflow) {
		t.Fatalf("want ErrArithmeticOverflow, got %v", err)
	}
	if res.EntriesExamined != 7 || res.RowsUpdated != 0 {
		t.Fatalf("got %+v want examined=7 updated=0", res)
	}
	after := valuesByID(tbl)
	if dumpRows(after) != dumpRows(before) {
		t.Fatalf("first six rows must roll back:\nbefore=%v\nafter =%v", before, after)
	}
	assertIndexInvariant(t, tbl)
}

func TestEmptyRange(t *testing.T) {
	tbl := NewTable(testSchema(false))
	mustInsert(t, tbl, 1, 5, 0)
	before := valuesByID(tbl)
	exec := NewExecutor(tbl, os.Stdout)

	res, err := exec.Execute(RangeUpdate{Lo: 5, Hi: 5,
		Assignments: []Assignment{{Column: "v", Op: AssignAdd, Value: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsUpdated != 0 || res.EntriesExamined != 0 {
		t.Fatalf("got %+v want zero/zero", res)
	}
	if dumpRows(valuesByID(tbl)) != dumpRows(before) {
		t.Fatalf("empty range changed data")
	}
	assertIndexInvariant(t, tbl)
}

func TestInvalidRange(t *testing.T) {
	tbl := NewTable(testSchema(false))
	mustInsert(t, tbl, 1, 5, 0)
	before := valuesByID(tbl)
	exec := NewExecutor(tbl, os.Stdout)

	_, err := exec.Execute(RangeUpdate{Lo: 8, Hi: 2,
		Assignments: []Assignment{{Column: "v", Op: AssignAdd, Value: 1}}})
	if !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("want ErrInvalidRange, got %v", err)
	}
	if dumpRows(valuesByID(tbl)) != dumpRows(before) {
		t.Fatalf("table changed after invalid range")
	}
}

func TestUnknownColumnRejected(t *testing.T) {
	tbl := NewTable(testSchema(false))
	mustInsert(t, tbl, 1, 5, 0)
	exec := NewExecutor(tbl, os.Stdout)

	_, err := exec.Execute(RangeUpdate{Lo: 0, Hi: 10,
		Assignments: []Assignment{{Column: "nope", Op: AssignSet, Value: 1}}})
	if !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("want ErrUnknownColumn in assignment, got %v", err)
	}
	_, err = exec.Execute(RangeUpdate{Lo: 0, Hi: 10,
		Filters: map[string]int64{"ghost": 9}})
	if !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("want ErrUnknownColumn in filter, got %v", err)
	}
	_, err = exec.Execute(RangeUpdate{Lo: 0, Hi: 10,
		Assignments: []Assignment{{Column: "id", Op: AssignSet, Value: 1}}})
	if !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("want ErrUnknownColumn for primary-key assignment, got %v", err)
	}
}

func TestFilterAndSetOtherColumn(t *testing.T) {
	tbl := NewTable(testSchema(false))
	mustInsert(t, tbl, 1, 1, 10)
	mustInsert(t, tbl, 2, 2, 20)
	mustInsert(t, tbl, 3, 3, 10)
	exec := NewExecutor(tbl, os.Stdout)

	res, err := exec.Execute(RangeUpdate{Lo: 1, Hi: 10,
		Filters:     map[string]int64{"tag": 10},
		Assignments: []Assignment{{Column: "tag", Op: AssignSet, Value: 99}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.EntriesExamined != 3 || res.RowsUpdated != 2 {
		t.Fatalf("got %+v want examined=3 updated=2", res)
	}
	rows := valuesByID(tbl)
	if rows[1]["tag"] != 99 || rows[3]["tag"] != 99 || rows[2]["tag"] != 20 {
		t.Fatalf("filter/update wrong: %v %v %v", rows[1], rows[2], rows[3])
	}
	assertIndexInvariant(t, tbl)
}

func TestConcurrentReadersSeeBeforeOrAfter(t *testing.T) {
	tbl := NewTable(testSchema(false))
	for id := int64(1); id <= 50; id++ {
		mustInsert(t, tbl, id, id, 0)
	}
	exec := NewExecutor(tbl, os.Stdout)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := tbl.Snapshot()
				for i, row := range snap {
					wantBefore := int64(i) + 1
					wantAfter := wantBefore * 3
					if got := row.Values["v"]; got != wantBefore && got != wantAfter {
						t.Errorf("reader observed torn state: id=%d v=%d", row.ID, got)
						return
					}
				}
			}
		}()
	}
	res, err := exec.Execute(RangeUpdate{Lo: 1, Hi: 51,
		Assignments: []Assignment{{Column: "v", Op: AssignMul, Value: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	if res.RowsUpdated != 50 {
		t.Fatalf("rows updated=%d want 50", res.RowsUpdated)
	}
	assertIndexInvariant(t, tbl)
}

func TestRepeatedExecutionIsDeterministic(t *testing.T) {
	build := func() *Table {
		tbl := NewTable(testSchema(false))
		for id := int64(1); id <= 6; id++ {
			mustInsert(t, tbl, id, id, 0)
		}
		return tbl
	}
	stmt := RangeUpdate{Lo: 1, Hi: 7,
		Assignments: []Assignment{{Column: "v", Op: AssignMul, Value: 2}}}

	var outs []string
	var resPrev Result
	for i := 0; i < 2; i++ {
		var buf bytes.Buffer
		exec := NewExecutor(build(), &buf)
		res, err := exec.Execute(stmt)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 && res != resPrev {
			t.Fatalf("result not reproducible: %+v vs %+v", res, resPrev)
		}
		resPrev = res
		outs = append(outs, dumpRows(valuesByID(exec.table)))
	}
	if outs[0] != outs[1] {
		t.Fatalf("repeated execution differs:\n%s\n%s", outs[0], outs[1])
	}
}

func TestOverflowArithmeticBoundaries(t *testing.T) {
	type tc struct {
		a, b    int64
		op      AssignOp
		wantOK  bool
		wantVal int64
	}
	cases := []tc{
		{1, 3, AssignMul, true, 3},
		{math.MaxInt64 / 7, 7, AssignMul, true, math.MaxInt64 / 7 * 7},
		{math.MaxInt64/7 + 1, 7, AssignMul, false, 0},
		{math.MinInt64, 1, AssignMul, true, math.MinInt64},
		{math.MinInt64, -1, AssignMul, false, 0},
		{math.MaxInt64, 1, AssignAdd, false, 0},
		{math.MinInt64, -1, AssignAdd, false, 0},
		{math.MinInt64, 0, AssignAdd, true, math.MinInt64},
	}
	for _, c := range cases {
		var (
			got int64
			ok  bool
		)
		if c.op == AssignMul {
			got, ok = mulOverflow(c.a, c.b)
		} else {
			got, ok = addOverflow(c.a, c.b)
		}
		if ok != c.wantOK {
			t.Fatalf("%d op %d: ok=%v want %v", c.a, c.b, ok, c.wantOK)
		}
		if ok && got != c.wantVal {
			t.Fatalf("%d op %d: got %d want %d", c.a, c.b, got, c.wantVal)
		}
	}
}

func TestLogShowsInputOutputAndDecision(t *testing.T) {
	tbl := NewTable(testSchema(false))
	mustInsert(t, tbl, 1, 1, 0)
	var buf bytes.Buffer
	exec := NewExecutor(tbl, &buf)
	res, err := exec.Execute(RangeUpdate{Lo: 1, Hi: 2,
		Assignments: []Assignment{{Column: "v", Op: AssignAdd, Value: 4}}})
	if err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{
		"RangeUpdate input : range=[1,2)",
		"v+=4",
		fmt.Sprintf("RangeUpdate output: OK rows_updated=%d entries_examined=%d", res.RowsUpdated, res.EntriesExamined),
		"RangeUpdate decide:",
		"exactly once",
	} {
		if !bytes.Contains([]byte(log), []byte(want)) {
			t.Fatalf("log missing %q in:\n%s", want, log)
		}
	}
}
