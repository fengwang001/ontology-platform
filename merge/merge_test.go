package merge

import (
	"errors"
	"reflect"
	"testing"
)

var testSchema = []string{"a", "b", "c"}

func newTable() *Table { return NewTable(testSchema, nil) }

func ins(key string, cols Row) Event {
	return Event{Kind: EventInsert, Key: key, Columns: cols}
}

func upd(key string, cols, before Row) Event {
	return Event{Kind: EventUpdate, Key: key, Columns: cols, Before: before}
}

// seed inserts a row directly via a committed insert batch.
func seed(t *testing.T, tbl *Table, key string, cols Row) {
	t.Helper()
	if _, err := tbl.Commit([]Event{ins(key, cols)}); err != nil {
		t.Fatalf("seed insert %q: %v", key, err)
	}
}

func mustErrKind(t *testing.T, err error, want ErrorKind) *BatchError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind %v, got nil", want)
	}
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BatchError, got %T (%v)", err, err)
	}
	if be.Kind != want {
		t.Fatalf("expected kind %v, got %v (%v)", want, be.Kind, err)
	}
	return be
}

// --- Tri-state distinction -------------------------------------------------

func TestTriStateValuesAreDistinct(t *testing.T) {
	absent, null, empty, s := Absent(), Null(), Str(""), Str("x")

	if absent.Equal(null) || absent.Equal(empty) || null.Equal(empty) {
		t.Fatal("absent, null and empty-string must be mutually distinct")
	}
	if !null.Equal(Null()) {
		t.Fatal("null must equal null")
	}
	if !empty.Equal(Str("")) {
		t.Fatal("empty string must equal empty string")
	}
	if empty.Equal(s) || s.Equal(empty) {
		t.Fatal("empty string must differ from non-empty string")
	}
	if !absent.IsAbsent() || !null.IsNullValue() || !empty.IsString() {
		t.Fatal("tri-state predicates wrong")
	}
}

func TestRowStoresNullAndEmptyStringDistinctly(t *testing.T) {
	tbl := newTable()
	seed(t, tbl, "k1", Row{"a": Null(), "b": Str(""), "c": Str("v")})

	row, ok := tbl.Get("k1")
	if !ok {
		t.Fatal("row k1 missing")
	}
	if !row["a"].IsNullValue() {
		t.Fatalf("column a: want explicit null, got %v", row["a"])
	}
	if !row["b"].IsString() || row["b"].Str != "" {
		t.Fatalf("column b: want empty string, got %v", row["b"])
	}
	if row["a"].Equal(row["b"]) {
		t.Fatal("null and empty string must not compare equal")
	}
	if err := tbl.SelfCheck(); err != nil {
		t.Fatalf("self-check: %v", err)
	}
}

// --- Insert validation -----------------------------------------------------

func TestInsertRequiresExactlyAllColumns(t *testing.T) {
	cases := []struct {
		name string
		cols Row
	}{
		{"missing column", Row{"a": Str("1"), "b": Str("2")}},
		{"extra column", Row{"a": Str("1"), "b": Str("2"), "c": Str("3"), "zz": Str("9")}},
		{"absent value", Row{"a": Str("1"), "b": Str("2"), "c": Absent()}},
		{"unknown column", Row{"a": Str("1"), "b": Str("2"), "zz": Str("3")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tbl := newTable()
			before := tbl.Snapshot()
			_, err := tbl.Commit([]Event{ins("k", tc.cols)})
			mustErrKind(t, err, ErrIllegalColumn)
			if !reflect.DeepEqual(before, tbl.Snapshot()) {
				t.Fatal("rejected insert must not change the table")
			}
		})
	}
}

// --- Insert then update stays an insert ------------------------------------

func TestInsertThenUpdateStaysInsert(t *testing.T) {
	tbl := newTable()
	res, err := tbl.Commit([]Event{
		ins("k", Row{"a": Str("1"), "b": Str("2"), "c": Str("3")}),
		upd("k", Row{"b": Str("B"), "c": Null()}, Row{"b": Str("2"), "c": Str("3")}),
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("want 1 merged change, got %d", len(res.Changes))
	}
	ch := res.Changes[0]
	if ch.Kind != EventInsert {
		t.Fatalf("insert+update must stay an insert, got %v", ch.Kind)
	}
	want := Row{"a": Str("1"), "b": Str("B"), "c": Null()}
	if !reflect.DeepEqual(ch.Columns, want) {
		t.Fatalf("merged insert row = %v, want %v", ch.Columns, want)
	}
	row, _ := tbl.Get("k")
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("table row = %v, want %v", row, want)
	}
}

// --- Update merge: union, last-wins, first-before --------------------------

func TestUpdateMergeUnionLastWinsFirstBefore(t *testing.T) {
	tbl := newTable()
	seed(t, tbl, "k", Row{"a": Str("A"), "b": Str("B"), "c": Str("C")})

	res, err := tbl.Commit([]Event{
		upd("k", Row{"a": Str("a1"), "b": Str("b1")}, Row{"a": Str("A"), "b": Str("B")}),
		upd("k", Row{"a": Str("a2"), "c": Str("c2")}, Row{"a": Str("a1"), "c": Str("C")}),
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("want 1 merged change, got %d", len(res.Changes))
	}
	ch := res.Changes[0]
	if ch.Kind != EventUpdate {
		t.Fatalf("want update, got %v", ch.Kind)
	}
	// union of columns {a,b} ∪ {a,c} = {a,b,c}; a last-wins a2; before a = first (A).
	wantCols := Row{"a": Str("a2"), "b": Str("b1"), "c": Str("c2")}
	wantBefore := Row{"a": Str("A"), "b": Str("B"), "c": Str("C")}
	if !reflect.DeepEqual(ch.Columns, wantCols) {
		t.Fatalf("merged columns = %v, want %v", ch.Columns, wantCols)
	}
	if !reflect.DeepEqual(ch.Before, wantBefore) {
		t.Fatalf("merged before = %v, want %v", ch.Before, wantBefore)
	}
}

// --- No-change pruning -----------------------------------------------------

func TestNoChangeColumnsPrunedAndKeyDropped(t *testing.T) {
	tbl := newTable()
	seed(t, tbl, "k", Row{"a": Str("A"), "b": Str("B"), "c": Str("C")})
	before := tbl.Snapshot()

	// a: A->a1->A (net no-op), b: B->b2 (real change), c untouched.
	res, err := tbl.Commit([]Event{
		upd("k", Row{"a": Str("a1"), "b": Str("b2")}, Row{"a": Str("A"), "b": Str("B")}),
		upd("k", Row{"a": Str("A")}, Row{"a": Str("a1")}),
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("want 1 change, got %d", len(res.Changes))
	}
	ch := res.Changes[0]
	if _, has := ch.Columns["a"]; has {
		t.Fatalf("column a net-no-op must be pruned, got %v", ch.Columns)
	}
	if !ch.Columns["b"].Equal(Str("b2")) {
		t.Fatalf("column b must survive as b2, got %v", ch.Columns)
	}

	// Now a batch that is entirely no-op => key dropped, table unchanged.
	res2, err := tbl.Commit([]Event{
		upd("k", Row{"b": Str("B")}, Row{"b": Str("b2")}),
		upd("k", Row{"b": Str("b2")}, Row{"b": Str("B")}),
	})
	if err != nil {
		t.Fatalf("commit2: %v", err)
	}
	if len(res2.Changes) != 0 {
		t.Fatalf("all-no-op batch must emit nothing, got %v", res2.Keys())
	}
	_ = before
}

func TestEntirelyNoOpUpdateLeavesTableUnchanged(t *testing.T) {
	tbl := newTable()
	seed(t, tbl, "k", Row{"a": Str("A"), "b": Str("B"), "c": Str("C")})
	before := tbl.Snapshot()
	res, err := tbl.Commit([]Event{upd("k", Row{"a": Str("A")}, Row{"a": Str("A")})})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(res.Changes) != 0 {
		t.Fatalf("no-op update must emit nothing, got %v", res.Keys())
	}
	if !reflect.DeepEqual(before, tbl.Snapshot()) {
		t.Fatal("no-op update must not change the table")
	}
}

// --- Illegal inputs are rejected with distinct kinds; table unchanged ------

func TestRejectionsHaveDistinctKindsAndLeaveTableUnchanged(t *testing.T) {
	tbl := newTable()
	seed(t, tbl, "exist", Row{"a": Str("A"), "b": Str("B"), "c": Str("C")})

	cases := []struct {
		name   string
		events []Event
		want   ErrorKind
	}{
		{"illegal column unknown", []Event{upd("exist", Row{"zz": Str("1")}, Row{"zz": Str("1")})}, ErrIllegalColumn},
		{"illegal column empty update", []Event{upd("exist", Row{}, Row{})}, ErrIllegalColumn},
		{"illegal before-set mismatch", []Event{upd("exist", Row{"a": Str("x")}, Row{"a": Str("A"), "b": Str("B")})}, ErrIllegalColumn},
		{"key not found", []Event{upd("ghost", Row{"a": Str("1")}, Row{"a": Str("1")})}, ErrKeyNotFound},
		{"key exists", []Event{ins("exist", Row{"a": Str("1"), "b": Str("2"), "c": Str("3")})}, ErrKeyExists},
		{"before image mismatch", []Event{upd("exist", Row{"a": Str("x")}, Row{"a": Str("WRONG")})}, ErrBeforeImageMismatch},
	}

	seen := map[ErrorKind]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tbl.Snapshot()
			_, err := tbl.Commit(tc.events)
			be := mustErrKind(t, err, tc.want)
			seen[be.Kind] = true
			if !reflect.DeepEqual(before, tbl.Snapshot()) {
				t.Fatalf("rejected batch (%v) changed the table", tc.want)
			}
		})
	}
	// All four required categories exercised and distinguishable.
	for _, k := range []ErrorKind{ErrIllegalColumn, ErrKeyNotFound, ErrKeyExists, ErrBeforeImageMismatch} {
		if !seen[k] {
			t.Fatalf("error kind %v not exercised", k)
		}
	}
}

func TestWholeBatchRejectedWhenOneEventInvalid(t *testing.T) {
	tbl := newTable()
	seed(t, tbl, "good", Row{"a": Str("A"), "b": Str("B"), "c": Str("C")})
	before := tbl.Snapshot()

	// First event is a valid update; second is invalid. Whole batch must be
	// rejected and the valid update must NOT be applied.
	_, err := tbl.Commit([]Event{
		upd("good", Row{"a": Str("CHANGED")}, Row{"a": Str("A")}),
		upd("ghost", Row{"a": Str("1")}, Row{"a": Str("1")}),
	})
	mustErrKind(t, err, ErrKeyNotFound)
	if !reflect.DeepEqual(before, tbl.Snapshot()) {
		t.Fatal("batch with one invalid event must be rejected wholesale (no partial apply)")
	}
	row, _ := tbl.Get("good")
	if !row["a"].Equal(Str("A")) {
		t.Fatalf("valid-then-invalid batch leaked a write: a=%v", row["a"])
	}
}

// --- Output ordering & uniqueness ------------------------------------------

func TestOutputOrderIsFirstAppearanceAndUnique(t *testing.T) {
	tbl := newTable()
	for _, k := range []string{"k1", "k2", "k3"} {
		seed(t, tbl, k, Row{"a": Str("0"), "b": Str("0"), "c": Str("0")})
	}
	res, err := tbl.Commit([]Event{
		upd("k2", Row{"a": Str("1")}, Row{"a": Str("0")}),
		upd("k1", Row{"a": Str("1")}, Row{"a": Str("0")}),
		upd("k2", Row{"a": Str("2")}, Row{"a": Str("1")}),
		ins("k9", Row{"a": Str("x"), "b": Str("y"), "c": Str("z")}),
		upd("k1", Row{"a": Str("9")}, Row{"a": Str("1")}),
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	got := res.Keys()
	want := []string{"k2", "k1", "k9"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output order = %v, want first-appearance %v", got, want)
	}
	// uniqueness
	seen := map[string]int{}
	for _, k := range got {
		seen[k]++
		if seen[k] > 1 {
			t.Fatalf("key %q appears more than once in output", k)
		}
	}
}

// --- Reproducibility ---------------------------------------------------------

func TestMergeIsReproducible(t *testing.T) {
	build := func() *Table {
		tbl := newTable()
		seed(t, tbl, "k", Row{"a": Str("A"), "b": Str("B"), "c": Str("C")})
		return tbl
	}
	events := []Event{
		upd("k", Row{"a": Str("a1"), "b": Str("b1")}, Row{"a": Str("A"), "b": Str("B")}),
		upd("k", Row{"a": Str("a2")}, Row{"a": Str("a1")}),
	}
	r1, err1 := build().Merge(events)
	r2, err2 := build().Merge(events)
	if err1 != nil || err2 != nil {
		t.Fatalf("merge errors: %v %v", err1, err2)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatal("same batch merged twice must be identical (reproducible)")
	}
}

// --- Empty string vs null updates -------------------------------------------

func TestEmptyStringAndNullUpdatesAreDistinct(t *testing.T) {
	tbl := newTable()
	seed(t, tbl, "k", Row{"a": Str("A"), "b": Str("B"), "c": Str("C")})
	_, err := tbl.Commit([]Event{
		upd("k", Row{"a": Str(""), "b": Null()}, Row{"a": Str("A"), "b": Str("B")}),
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	row, _ := tbl.Get("k")
	if !row["a"].IsString() || row["a"].Str != "" {
		t.Fatalf("a must be empty string, got %v", row["a"])
	}
	if !row["b"].IsNullValue() {
		t.Fatalf("b must be explicit null, got %v", row["b"])
	}
	if row["a"].Equal(row["b"]) {
		t.Fatal("empty string and null must remain distinct after update")
	}
}
