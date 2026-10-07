package bitemporal

import (
	"bytes"
	"sync"
	"testing"
)

func testStore(t *testing.T) (*Store, *Exporter, *bytes.Buffer) {
	t.Helper()
	s := NewStore(0, 0)
	if err := s.RegisterSchema("person", 0, map[string]FieldKind{"name": KindString, "age": KindInt}); err != nil {
		t.Fatal(err)
	}
	buf := &bytes.Buffer{}
	return s, NewExporter(s, NewTextLogger(buf)), buf
}

func iv(t *testing.T, a, b Tick) Interval {
	t.Helper()
	i, err := NewInterval(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func val(name string, age int64) Value {
	return Value{Fields: map[string]any{"name": name, "age": age}}
}

func mustWrite(t *testing.T, s *Store, oid string, a, b Tick, v Value) *Record {
	t.Helper()
	r, err := s.Write(oid, "person", iv(t, a, b), v)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	return r
}

// TestHalfOpenBoundary verifies start is covered, end is not, and unknown runs
// are emitted rather than default-filled.
func TestHalfOpenBoundary(t *testing.T) {
	s, exp, log := testStore(t)
	mustWrite(t, s, "o1", 10, 20, val("a", 1))

	c, err := exp.Freeze(100)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := exp.ExportObject(c, "person", "o1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Segments) != 3 {
		t.Fatalf("want unknown/known/unknown = 3 segments, got %d: %+v", len(snap.Segments), snap.Segments)
	}
	if !snap.Segments[0].Unknown() || snap.Segments[0].Start != MinTick || snap.Segments[0].End != 10 {
		t.Fatalf("segment0 wrong: %+v", snap.Segments[0])
	}
	if snap.Segments[1].Unknown() || snap.Segments[1].Start != 10 || snap.Segments[1].End != 20 {
		t.Fatalf("segment1 wrong: %+v", snap.Segments[1])
	}
	if !snap.Segments[2].Unknown() || snap.Segments[2].Start != 20 || snap.Segments[2].End != MaxTick {
		t.Fatalf("segment2 wrong: %+v", snap.Segments[2])
	}
	// Point probes at the exact boundary.
	if r, _ := exp.VisibleAt(c, "o1", 9); r != nil {
		t.Fatal("v=9 must be unknown")
	}
	if r, _ := exp.VisibleAt(c, "o1", 10); r == nil {
		t.Fatal("v=10 must be covered")
	}
	if r, _ := exp.VisibleAt(c, "o1", 19); r == nil {
		t.Fatal("v=19 must be covered")
	}
	if r, _ := exp.VisibleAt(c, "o1", 20); r != nil {
		t.Fatal("v=20 must be unknown (half-open)")
	}
	if log.Len() == 0 {
		t.Fatal("decision log must record every decision")
	}
}

// TestCorrectionCollapse verifies that same-interval corrections keep only the
// newest eligible record in exports, while audits keep both.
func TestCorrectionCollapse(t *testing.T) {
	s, exp, _ := testStore(t)
	old := mustWrite(t, s, "o1", 0, 100, val("old", 1))
	s.AdvanceClock(10)
	new := mustWrite(t, s, "o1", 0, 100, val("new", 2))

	// At T=5 only the old record exists.
	c5, err := exp.Freeze(5)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := exp.VisibleAt(c5, "o1", 50)
	if r == nil || r.Seq != old.Seq {
		t.Fatalf("at T=5 want old seq %d, got %v", old.Seq, r)
	}
	// At T=10 the correction wins.
	c10, err := exp.Freeze(10)
	if err != nil {
		t.Fatal(err)
	}
	r, _ = exp.VisibleAt(c10, "o1", 50)
	if r == nil || r.Seq != new.Seq {
		t.Fatalf("at T=10 want new seq %d, got %v", new.Seq, r)
	}
	snap, err := exp.ExportObject(c10, "person", "o1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Segments) != 3 || snap.Segments[1].Value == nil || !snap.Segments[1].Value.Equal(val("new", 2)) {
		t.Fatalf("corrected snapshot wrong: %+v", snap.Segments)
	}
	// Audit view retains the corrected-away old record.
	hist, err := NewAuditView(s).History("o1")
	if err != nil || len(hist) != 2 {
		t.Fatalf("audit must retain both records, got %d (%v)", len(hist), err)
	}
	vers, err := NewAuditView(s).VersionsOver("o1", 50, 10)
	if err != nil || len(vers) != 2 || vers[0].Seq != new.Seq || vers[1].Seq != old.Seq {
		t.Fatalf("audit versions must expose new then old, got %+v", vers)
	}
}

// TestTransactionBoundT verifies arrivals exactly at tick T: those present at
// the freeze binding are included; later arrivals at the same tick are not.
func TestTransactionBoundT(t *testing.T) {
	s, exp, _ := testStore(t)
	mustWrite(t, s, "o1", 0, 100, val("a", 1))
	c, err := exp.Freeze(0)
	if err != nil {
		t.Fatal(err)
	}
	// A same-tick write arriving AFTER the binding must not enter the snapshot.
	mustWrite(t, s, "o1", 0, 100, val("late", 9))
	r, _ := exp.VisibleAt(c, "o1", 50)
	if r == nil || !r.Value.Equal(val("a", 1)) {
		t.Fatalf("frozen snapshot must ignore later same-tick arrival, got %+v", r)
	}
	// A fresh freeze at the same tick includes it; the rule is binding-wide.
	c2, err := exp.Freeze(0)
	if err != nil {
		t.Fatal(err)
	}
	r, _ = exp.VisibleAt(c2, "o1", 50)
	if r == nil || !r.Value.Equal(val("late", 9)) {
		t.Fatalf("new binding must include arrived same-tick write, got %+v", r)
	}
}

// TestRejectionPriority verifies retention > schema undefined > invalid range.
func TestRejectionPriority(t *testing.T) {
	s := NewStore(100, 100)
	if err := s.RegisterSchema("late", 200, map[string]FieldKind{"x": KindBool}); err != nil {
		t.Fatal(err)
	}
	exp := NewExporter(s, NopLogger{})
	bad, _ := NewInterval(50, 10) // start >= end

	// Retention wins even when schema and range are also bad.
	_, err := exp.Prepare(50, "late", &bad)
	if ee, ok := AsExportError(err); !ok || ee.Code != CodeRetention {
		t.Fatalf("want retention, got %v", err)
	}
	// Past retention but schema undefined at T: schema wins over bad range.
	_, err = exp.Prepare(150, "late", &bad)
	if ee, ok := AsExportError(err); !ok || ee.Code != CodeSchemaUndefined {
		t.Fatalf("want schema undefined, got %v", err)
	}
	// Defined and retained: bad range is now reported.
	_, err = exp.Prepare(250, "late", &bad)
	if ee, ok := AsExportError(err); !ok || ee.Code != CodeInvalidRange {
		t.Fatalf("want invalid range, got %v", err)
	}
	// Future T is rejected as invalid parameter after schema checks.
	_, err = exp.Prepare(150, "missing", nil)
	if ee, ok := AsExportError(err); !ok || ee.Code != CodeSchemaUndefined {
		t.Fatalf("want schema undefined for missing type, got %v", err)
	}
}

// TestBatchingOneCutoff verifies every batch shares the single frozen binding.
func TestBatchingOneCutoff(t *testing.T) {
	s, exp, _ := testStore(t)
	mustWrite(t, s, "b", 0, 100, val("b", 1))
	mustWrite(t, s, "a", 0, 100, val("a", 1))
	c, err := exp.Freeze(0)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.AdvanceClock(5)
		_, _ = s.Write("a", "person", iv(t, 0, 100), val("a2", 2))
	}()
	snaps, err := exp.ExportBatch(c, "person", []ObjectRef{{"b"}, {"a"}, {"b"}, {"a"}}, nil)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	// Sorted, de-duplicated? IDs are sorted but not deduped: 4 entries.
	want := []string{"a", "a", "b", "b"}
	for i, sn := range snaps {
		if sn.ObjectID != want[i] || sn.Cutoff != c {
			t.Fatalf("batch ordering/binding wrong at %d: %+v", i, sn)
		}
		// Every object reflects the frozen slice, never the concurrent write.
		known := sn.Segments[1]
		if known.Value == nil || known.Value.Fields["name"] != want[i][:1] {
			t.Fatalf("frozen batch object %s polluted by concurrent write: %+v", sn.ObjectID, sn.Segments)
		}
	}
}

func TestDeterministicReexport(t *testing.T) {
	s, exp, _ := testStore(t)
	mustWrite(t, s, "o1", 0, 100, val("a", 1))
	s.AdvanceClock(3)
	mustWrite(t, s, "o1", 50, 150, val("b", 2))
	s.AdvanceClock(7)
	mustWrite(t, s, "o1", 30, 80, val("c", 3))

	var enc1, enc2 []byte
	c1, _ := exp.Freeze(3)
	snap1, err := exp.ExportObject(c1, "person", "o1", nil)
	if err != nil {
		t.Fatal(err)
	}
	enc1 = snap1.Canonical()
	// Advance the clock with no new records for this object; export same T again.
	s.AdvanceClock(50)
	c2, _ := exp.Freeze(3)
	snap2, err := exp.ExportObject(c2, "person", "o1", nil)
	if err != nil {
		t.Fatal(err)
	}
	enc2 = snap2.Canonical()
	// Cutoff seq differs only if records arrived; here none did for the object,
	// but global seq is identical too. Compare full bytes.
	if !bytes.Equal(enc1, enc2) {
		t.Fatalf("byte-level determinism violated:\n%s\n%s", enc1, enc2)
	}
}
