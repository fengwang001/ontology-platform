package secindex

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// logState prints the operation, every index group and the current record
// count. Every test logs through it so that -v output shows the operation,
// all index groups, query results and the basis of each verdict.
func logState(t *testing.T, op string, m *Maintainer) {
	t.Helper()
	t.Logf("op=%s | groups=%s | records=%d | rescan=%v",
		op, formatGroups(m.IndexGroups()), m.Len(), m.VerifyByRescan())
}

func logQuery(t *testing.T, query string, got, want []string) {
	t.Helper()
	t.Logf("query=%s | got=%v | want=%v | verdict=%s",
		query, got, want, verdict(reflect.DeepEqual(got, want)))
}

func verdict(ok bool) string {
	if ok {
		return "PASS (equal)"
	}
	return "FAIL (mismatch)"
}

func must(t *testing.T, op string, m *Maintainer, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", op, err)
	}
	logState(t, op, m)
}

func assertReject(t *testing.T, op string, err error, cause error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected rejection (%v), got nil", op, cause)
	}
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("%s: error is not *BatchError: %v", op, err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("%s: cause=%v, want errors.Is %v (full: %v)", op, be.Cause, cause, err)
	}
	t.Logf("op=%s | rejected as required | error=%q | cause matches %v",
		op, err, cause)
}

// TestLookupAndGroupOrder covers insert ordering inside one value group:
// keys arrive out of order and must come back ascending.
func TestLookupAndGroupOrder(t *testing.T) {
	m := New()
	must(t, `Upsert("delta",10)`, m, m.Upsert("delta", 10))
	must(t, `Upsert("alpha",10)`, m, m.Upsert("alpha", 10))
	must(t, `Upsert("charlie",10)`, m, m.Upsert("charlie", 10))
	must(t, `Upsert("bravo",10)`, m, m.Upsert("bravo", 10))

	got := m.Lookup(10)
	want := []string{"alpha", "bravo", "charlie", "delta"}
	logQuery(t, "Lookup(10)", got, want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lookup(10)=%v, want %v", got, want)
	}

	if got := m.Lookup(99); !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("Lookup(99)=%v, want empty non-nil slice", got)
	} else {
		t.Logf("query=Lookup(99) | got=%v | verdict=PASS (missing value -> empty)", got)
	}
}

// TestIndexKeyUpdate is the core incremental-maintenance case: moving a key
// from the old value group to the new value group must be atomic, and after
// the update the key exists only in the new group.
func TestIndexKeyUpdate(t *testing.T) {
	m := New()
	must(t, `Upsert("k1",10)`, m, m.Upsert("k1", 10))
	must(t, `Upsert("k2",10)`, m, m.Upsert("k2", 10))
	must(t, `Upsert("k1",20) [UPDATE]`, m, m.Upsert("k1", 20))

	oldGroup := m.Lookup(10)
	newGroup := m.Lookup(20)
	t.Logf("after update: Lookup(10)=%v (old group must not contain k1), Lookup(20)=%v (new group must contain k1)",
		oldGroup, newGroup)
	if !reflect.DeepEqual(oldGroup, []string{"k2"}) {
		t.Fatalf("old group=%v, want [k2]: stale or duplicated entry", oldGroup)
	}
	if !reflect.DeepEqual(newGroup, []string{"k1"}) {
		t.Fatalf("new group=%v, want [k1]", newGroup)
	}
	if v, ok := m.Get("k1"); !ok || v != 20 {
		t.Fatalf("Get(k1)=(%d,%v), want (20,true)", v, ok)
	}
	logState(t, "post-update invariant check", m)
}

// TestEqualValueNoDuplicate rewrites the same value repeatedly: the key must
// appear exactly once in the group (no duplicate index entries).
func TestEqualValueNoDuplicate(t *testing.T) {
	m := New()
	must(t, `Upsert("k1",10)`, m, m.Upsert("k1", 10))
	for i := 0; i < 3; i++ {
		must(t, fmt.Sprintf(`Upsert("k1",10) equal-value rewrite #%d (no-op)`, i+1),
			m, m.Upsert("k1", 10))
	}
	got := m.Lookup(10)
	want := []string{"k1"}
	logQuery(t, "Lookup(10) after 3 equal-value rewrites", got, want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("equal-value rewrite created duplicates: %v", got)
	}
	if m.Len() != 1 {
		t.Fatalf("Len=%d, want 1", m.Len())
	}
}

// TestRangeLeftClosedRightOpen verifies [lo, hi): lo included, hi excluded.
func TestRangeLeftClosedRightOpen(t *testing.T) {
	m := New()
	for _, rec := range []struct {
		key   string
		value int64
	}{
		{"a", 0}, {"b", 10}, {"c", 10}, {"d", 20}, {"e", 30},
	} {
		must(t, fmt.Sprintf(`Upsert(%q,%d)`, rec.key, rec.value), m, m.Upsert(rec.key, rec.value))
	}

	cases := []struct {
		lo, hi int64
		want   []string
	}{
		{10, 30, []string{"b", "c", "d"}}, // 10 and 20 in; 30 excluded
		{10, 20, []string{"b", "c"}},      // right-open: 20 excluded
		{10, 10, []string{}},              // empty interval
		{5, 6, []string{}},                // no values in interval
		{0, 31, []string{"a", "b", "c", "d", "e"}},
	}
	for _, tc := range cases {
		got := m.Range(tc.lo, tc.hi)
		logQuery(t, fmt.Sprintf("Range(%d,%d) [lo,hi)", tc.lo, tc.hi), got, tc.want)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Range(%d,%d)=%v, want %v", tc.lo, tc.hi, got, tc.want)
		}
	}
}

// TestDelete removes a record and its index entry, then rejects deleting the
// same (now missing) key.
func TestDelete(t *testing.T) {
	m := New()
	must(t, `Upsert("k1",10)`, m, m.Upsert("k1", 10))
	must(t, `Upsert("k2",10)`, m, m.Upsert("k2", 10))
	must(t, `Delete("k1")`, m, m.Delete("k1"))

	got := m.Lookup(10)
	want := []string{"k2"}
	logQuery(t, "Lookup(10) after delete", got, want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lookup(10)=%v after delete, want %v", got, want)
	}
	if _, ok := m.Get("k1"); ok {
		t.Fatal("k1 still visible via Get after delete")
	}
	assertReject(t, `Delete("k1") again [missing]`, m.Delete("k1"), ErrKeyNotFound)
	assertReject(t, `Delete("") [empty]`, m.Delete(""), ErrEmptyKey)
	assertReject(t, `Upsert("",1) [empty]`, m.Upsert("", 1), ErrEmptyKey)
	if m.Len() != 1 {
		t.Fatalf("Len=%d, failed deletes must not change state, want 1", m.Len())
	}
}

// TestBatchAtomicRejection checks all distinguishable rejection reasons and
// proves a rejected batch leaves record table and index untouched.
func TestBatchAtomicRejection(t *testing.T) {
	seed := []Op{
		{Kind: OpUpsert, Key: "a", Value: 10},
		{Kind: OpUpsert, Key: "b", Value: 20},
	}
	m := New()
	must(t, "Apply seed batch", m, m.Apply(seed))

	type testCase struct {
		name  string
		ops   []Op
		cause error
	}
	cases := []testCase{
		{"empty key", []Op{{Kind: OpUpsert, Key: "", Value: 1}}, ErrEmptyKey},
		{"delete missing key", []Op{{Kind: OpUpsert, Key: "c", Value: 1},
			{Kind: OpDelete, Key: "ghost"}}, ErrKeyNotFound},
		{"invalid op kind", []Op{{Kind: OpKind(99), Key: "a"}}, ErrInvalidOpKind},
		{"empty batch", nil, ErrEmptyBatch},
		{"delete twice within one batch",
			[]Op{{Kind: OpDelete, Key: "a"}, {Kind: OpDelete, Key: "a"}}, ErrKeyNotFound},
	}

	beforeGroups := m.IndexGroups()
	beforeLen := m.Len()
	for _, tc := range cases {
		err := m.Apply(tc.ops)
		assertReject(t, "Apply["+tc.name+"]", err, tc.cause)
		if !reflect.DeepEqual(m.IndexGroups(), beforeGroups) || m.Len() != beforeLen {
			t.Fatalf("rejected batch %q changed state:\n before=%v\n after =%v",
				tc.name, beforeGroups, m.IndexGroups())
		}
		t.Logf("batch=%q | state unchanged | groups=%s | records=%d",
			tc.name, formatGroups(m.IndexGroups()), m.Len())
	}
}

// TestBatchHappyPath applies a mixed batch atomically, including a key moved
// between value groups, and checks the post-batch index and queries.
func TestBatchHappyPath(t *testing.T) {
	m := New()
	seed := []Op{
		{Kind: OpUpsert, Key: "a", Value: 10},
		{Kind: OpUpsert, Key: "b", Value: 10},
		{Kind: OpUpsert, Key: "c", Value: 30},
	}
	must(t, "Apply seed batch", m, m.Apply(seed))

	batch := []Op{
		{Kind: OpUpsert, Key: "a", Value: 20}, // move 10 -> 20
		{Kind: OpUpsert, Key: "d", Value: 20}, // insert into 20
		{Kind: OpDelete, Key: "c"},            // remove from 30
		{Kind: OpUpsert, Key: "e", Value: 10}, // insert into 10
	}
	must(t, "Apply mixed update/insert/delete batch", m, m.Apply(batch))

	wantGroups := []IndexGroup{
		{Value: 10, Keys: []string{"b", "e"}},
		{Value: 20, Keys: []string{"a", "d"}},
	}
	gotGroups := m.IndexGroups()
	t.Logf("query=IndexGroups | got=%s | want=%s | verdict=%s",
		formatGroups(gotGroups), formatGroups(wantGroups),
		verdict(reflect.DeepEqual(gotGroups, wantGroups)))
	if !reflect.DeepEqual(gotGroups, wantGroups) {
		t.Fatalf("groups=%v, want %v", gotGroups, wantGroups)
	}
	if got := m.Lookup(30); len(got) != 0 {
		t.Fatalf("group 30 should be gone, got %v", got)
	}
}

// TestResultCopies ensures callers cannot corrupt internal index ordering by
// mutating a returned query slice (a reproducibility prerequisite).
func TestResultCopies(t *testing.T) {
	m := New()
	must(t, `Upsert("x",1)`, m, m.Upsert("x", 1))
	got := m.Lookup(1)
	got[0] = "tampered"
	again := m.Lookup(1)
	if !reflect.DeepEqual(again, []string{"x"}) {
		t.Fatalf("internal state mutated through returned slice: %v", again)
	}
	t.Logf("query=Lookup after mutating returned slice | got=%v | verdict=PASS (defensive copy)", again)
}
