package ontology

import (
	"fmt"
	"testing"
)

// TestQueryCostIndependentOfHistory is the deterministic, independently
// verifiable proof that a grouping query costs O(result size), not
// O(processed events) or O(migrations): the engine exposes the exact number
// of operations the last query performed, and we show it stays constant
// while the event history and migration count grow arbitrarily, and scales
// only with the result size.
func TestQueryCostIndependentOfHistory(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "Asia/Shanghai"))
	mustDo(t, e.DefineTimezone("B", "UTC"))

	// place 100 objects (50 links) into the view
	place := func(n int, dayOffset int) {
		for i := 0; i < n; i++ {
			a := fmt.Sprintf("a%03d-%d", i, dayOffset)
			b := fmt.Sprintf("b%03d-%d", i, dayOffset)
			wa, _ := e.Write(a, "A", "ta", MustWall(fmt.Sprintf("2026-01-%02dT10:00:00", 10+dayOffset)))
			wb, _ := e.Write(b, "B", "tb", MustWall(fmt.Sprintf("2026-01-%02dT11:00:00", 10+dayOffset)))
			l, _ := e.Link("L", a, "A", b, "B")
			e.Dispatch(wa, wb, l)
		}
	}
	place(50, 1)

	groups1, _, err := e.Query("v")
	mustDo(t, err)
	ops1, err := e.LastQueryOps("v")
	mustDo(t, err)
	if want := int64(countItems(groups1) + len(groups1)); ops1 != want {
		t.Fatalf("query ops %d != result size %d", ops1, want)
	}

	// grow the history massively without changing the result:
	// - 2000 rewrites of the same objects with the same values
	// - 60 timezone definition migrations
	// - 2000 events for objects that never join the view
	// - redelivery of already-applied events
	var replay []Event
	for i := 0; i < 1000; i++ {
		a := fmt.Sprintf("a%03d-1", i%50)
		w, _ := e.Write(a, "A", "ta", MustWall("2026-01-11T10:00:00"))
		e.Dispatch(w)
		replay = append(replay, w)
		u := fmt.Sprintf("u%04d", i)
		wu, _ := e.Write(u, "B", "tb", MustWall("2026-01-11T12:00:00"))
		e.Dispatch(wu) // never linked: never placed
	}
	for i := 0; i < 30; i++ {
		mustDo(t, e.MigrateTimezone("A", tzZones[i%len(tzZones)], e.CurrentTZVersion("A")))
		mustDo(t, e.MigrateTimezone("B", tzZones[(i+3)%len(tzZones)], e.CurrentTZVersion("B")))
	}
	e.Dispatch(replay...) // duplicates: must be ignored idempotently

	groups2, _, err := e.Query("v")
	mustDo(t, err)
	ops2, err := e.LastQueryOps("v")
	mustDo(t, err)

	if diff := CompareGroups(groups1, groups2); diff != "" {
		t.Fatalf("history growth changed the result: %s", diff)
	}
	if ops2 != ops1 {
		t.Fatalf("query cost grew with history: ops1=%d ops2=%d (log=%d events, 60 migrations)",
			ops1, ops2, len(e.Log()))
	}
	t.Logf("query ops constant at %d over %d log events and 60 migrations",
		ops2, len(e.Log()))

	// doubling the result size doubles the cost: cost tracks result size.
	place(50, 2)
	groups3, _, err := e.Query("v")
	mustDo(t, err)
	ops3, err := e.LastQueryOps("v")
	mustDo(t, err)
	if want := int64(countItems(groups3) + len(groups3)); ops3 != want {
		t.Fatalf("query ops %d != result size %d", ops3, want)
	}
	if ops3 <= ops1 {
		t.Fatalf("query cost did not track result growth: %d -> %d", ops1, ops3)
	}
}

// BenchmarkViewQuery measures query wall time with a large preloaded
// history, complementing the deterministic ops-counter proof.
func BenchmarkViewQuery(b *testing.B) {
	e := NewEngine()
	must := func(err error) {
		if err != nil {
			b.Fatal(err)
		}
	}
	must(e.DefineObjectType("A", "ta", "ta"))
	must(e.DefineObjectType("B", "tb", "tb"))
	must(e.DefineTimezone("A", "Asia/Shanghai"))
	must(e.DefineTimezone("B", "UTC"))
	must(e.DefineLinkType("L", "A", "B"))
	must(e.NewView("v", "L"))
	for i := 0; i < 200; i++ {
		a, bb := fmt.Sprintf("a%04d", i), fmt.Sprintf("b%04d", i)
		wa, _ := e.Write(a, "A", "ta", MustWall("2026-01-15T10:00:00"))
		wb, _ := e.Write(bb, "B", "tb", MustWall("2026-01-15T11:00:00"))
		l, _ := e.Link("L", a, "A", bb, "B")
		e.Dispatch(wa, wb, l)
	}
	// heavy history: 20k extra events + 100 migrations
	for i := 0; i < 10000; i++ {
		w, _ := e.Write(fmt.Sprintf("a%04d", i%200), "A", "ta", MustWall("2026-01-15T10:00:00"))
		e.Dispatch(w)
		w2, _ := e.Write(fmt.Sprintf("x%05d", i), "B", "tb", MustWall("2026-01-15T12:00:00"))
		e.Dispatch(w2)
	}
	for i := 0; i < 100; i++ {
		must(e.MigrateTimezone("A", tzZones[i%len(tzZones)], e.CurrentTZVersion("A")))
	}
	if _, _, err := e.Query("v"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := e.Query("v"); err != nil {
			b.Fatal(err)
		}
	}
}
