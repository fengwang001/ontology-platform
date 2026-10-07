package ontology

import (
	"reflect"
	"testing"
)

// TestWriteTimeAnchoring: grouping and ordering use the timezone definition
// version in effect when the value was written, not the latest one.
func TestWriteTimeAnchoring(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "Asia/Shanghai")) // v1, UTC+8
	mustDo(t, e.DefineTimezone("B", "UTC"))           // v1

	w1, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T08:00:00")) // = 00:00 UTC
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T00:30:00")) // = 00:30 UTC

	mustDo(t, e.MigrateTimezone("A", "UTC", 1)) // v2

	w2, _ := e.Write("a2", "A", "ta", MustWall("2026-01-15T08:00:00")) // v2: = 08:00 UTC
	l2, _ := e.Link("L", "a2", "A", "b1", "B")

	e.Dispatch(w1, l1, wb, w2, l2)
	groups, rep, err := e.Query("v")
	mustDo(t, err)
	if rep.Err != nil {
		t.Fatalf("unexpected view error: %v", rep.Err)
	}
	got := flatten(groups)
	want := []string{"2026-01-15/a1", "2026-01-15/b1", "2026-01-15/a2"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// a1 used tz v1, a2 used tz v2
	items := groups[0].Items
	if items[0].TZVersion != 1 || items[2].TZVersion != 2 {
		t.Fatalf("tz versions not anchored at write time: %+v", items)
	}
	if items[0].Normalized.Hour() != 0 || items[2].Normalized.Hour() != 8 {
		t.Fatalf("normalization wrong: %+v", items)
	}
	checkViewInvariants(t, e, "v")
}

// TestMigrationKeepsExistingPlacements: a timezone definition migration must
// not move already-grouped objects (no time regression, no duplication).
func TestMigrationKeepsExistingPlacements(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "Asia/Shanghai"))
	mustDo(t, e.DefineTimezone("B", "UTC"))

	w1, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T08:00:00"))
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T00:30:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	e.Dispatch(w1, wb, l1)

	before, _, err := e.Query("v")
	mustDo(t, err)

	// migrate twice; no events in between
	mustDo(t, e.MigrateTimezone("A", "Europe/Berlin", 1))
	mustDo(t, e.MigrateTimezone("A", "America/New_York", 2))

	after, rep, err := e.Query("v")
	mustDo(t, err)
	if rep.Err != nil {
		t.Fatalf("unexpected view error: %v", rep.Err)
	}
	if !reflect.DeepEqual(groupSnapshot(before), groupSnapshot(after)) {
		t.Fatalf("migration moved existing objects:\nbefore=%v\nafter=%v",
			groupSnapshot(before), groupSnapshot(after))
	}
	checkViewInvariants(t, e, "v")
}

// TestLateArrivalAnchoredToWriteTime: an event written before a migration
// but delivered after it must still be grouped by the pre-migration version.
func TestLateArrivalAnchoredToWriteTime(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "Asia/Shanghai")) // v1: +8
	mustDo(t, e.DefineTimezone("B", "UTC"))

	// written under v1, held back
	late, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T08:00:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T00:30:00"))

	mustDo(t, e.MigrateTimezone("A", "UTC", 1)) // v2 now in effect

	// dispatched only after the migration
	e.Dispatch(wb, late, l1)
	groups, rep, err := e.Query("v")
	mustDo(t, err)
	if rep.Err != nil {
		t.Fatalf("unexpected view error: %v", rep.Err)
	}
	got := flatten(groups)
	// a1 normalized with v1 (+8): 08:00 -> 00:00 UTC, before b1 at 00:30
	want := []string{"2026-01-15/a1", "2026-01-15/b1"}
	if !equalStrings(got, want) {
		t.Fatalf("late event not anchored to write-time version: got %v, want %v", got, want)
	}
	if groups[0].Items[0].TZVersion != 1 {
		t.Fatalf("late event used tz v%d, want v1", groups[0].Items[0].TZVersion)
	}
	checkViewInvariants(t, e, "v")
}

// TestTieBreakRule: equal normalized instants are ordered by (type id,
// object id), independent of processing order.
func TestTieBreakRule(t *testing.T) {
	build := func(order []int) []string {
		e := setupEnv(t)
		mustDo(t, e.DefineTimezone("A", "Asia/Shanghai")) // +8
		mustDo(t, e.DefineTimezone("B", "UTC"))
		// both normalize to 2026-01-15T00:00:00Z
		wa, _ := e.Write("a9", "A", "ta", MustWall("2026-01-15T08:00:00"))
		wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T00:00:00"))
		l, _ := e.Link("L", "a9", "A", "b1", "B")
		evs := []Event{wa, wb, l}
		for _, i := range order {
			e.Dispatch(evs[i])
		}
		groups, _, err := e.Query("v")
		mustDo(t, err)
		return flatten(groups)
	}
	want := []string{"2026-01-15/a9", "2026-01-15/b1"} // type A < B
	for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 2}, {2, 0, 1}} {
		if got := build(order); !equalStrings(got, want) {
			t.Fatalf("order %v: got %v, want %v", order, got, want)
		}
	}
}

// TestRewriteReanchors: rewriting the time property re-anchors to the
// version in effect at the new write and moves the object exactly once.
func TestRewriteReanchors(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "Asia/Shanghai"))
	mustDo(t, e.DefineTimezone("B", "UTC"))

	w1, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T08:00:00")) // v1 -> 00:00 UTC
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T00:30:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	e.Dispatch(w1, wb, l1)
	mustDo(t, e.MigrateTimezone("A", "UTC", 1))

	w2, _ := e.Write("a1", "A", "ta", MustWall("2026-01-16T09:00:00")) // v2 -> 09:00 UTC, next day
	e.Dispatch(w2)

	groups, _, err := e.Query("v")
	mustDo(t, err)
	got := flatten(groups)
	want := []string{"2026-01-15/b1", "2026-01-16/a1"}
	if !equalStrings(got, want) {
		t.Fatalf("rewrite did not re-anchor: got %v, want %v", got, want)
	}
	checkViewInvariants(t, e, "v")
}

// TestStaleWriteLoses: an older write arriving after a newer one is ignored.
func TestStaleWriteLoses(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "UTC"))
	mustDo(t, e.DefineTimezone("B", "UTC"))

	old, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T01:00:00"))
	newer, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T02:00:00"))
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T03:00:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")

	e.Dispatch(newer, wb, l1, old) // old arrives last and must lose
	groups, _, err := e.Query("v")
	mustDo(t, err)
	items := groups[0].Items
	if items[0].ObjID != "a1" || items[0].Normalized.Hour() != 2 {
		t.Fatalf("stale write won: %+v", items[0])
	}
	checkViewInvariants(t, e, "v")
}

// TestUnlinkRemovesFromView.
func TestUnlinkRemovesFromView(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "UTC"))
	mustDo(t, e.DefineTimezone("B", "UTC"))
	wa, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T01:00:00"))
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T02:00:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	u1, _ := e.Unlink("L", "a1", "A", "b1", "B")
	e.Dispatch(wa, wb, l1, u1)
	groups, _, err := e.Query("v")
	mustDo(t, err)
	if len(groups) != 0 {
		t.Fatalf("expected empty view after unlink, got %v", flatten(groups))
	}
	checkViewInvariants(t, e, "v")
}

// TestAuditLogRecordsDecisions: every placement decision records its inputs,
// the timezone version relied on, and the conclusion.
func TestAuditLogRecordsDecisions(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "Asia/Shanghai"))
	mustDo(t, e.DefineTimezone("B", "UTC"))
	wa, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T08:00:00"))
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T00:30:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	e.Dispatch(wa, wb, l1)
	if _, _, err := e.Query("v"); err != nil {
		t.Fatal(err)
	}
	audit, err := e.Audit("v")
	mustDo(t, err)
	placed := 0
	for _, a := range audit {
		if a.Seq == 0 || a.EventLogSeq == 0 || a.EventKind == "" || a.Decision == "" {
			t.Fatalf("audit entry incomplete: %+v", a)
		}
		if a.Decision == "placed" {
			placed++
			if a.ObjID == "" || a.TypeID == "" || a.TZVersion != 1 || a.Wall == "" ||
				a.Group == "" || a.Normalized == "" || a.WriteSeq == 0 {
				t.Fatalf("placed decision missing fields: %+v", a)
			}
		}
	}
	if placed != 2 {
		t.Fatalf("expected 2 placed decisions, got %d", placed)
	}
}
