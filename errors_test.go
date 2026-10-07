package ontology

import "testing"

// TestErrNoTimezoneAtWrite: the type had no default timezone when the value
// was written.
func TestErrNoTimezoneAtWrite(t *testing.T) {
	e := setupEnv(t) // no DefineTimezone for A
	mustDo(t, e.DefineTimezone("B", "UTC"))

	wa, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T08:00:00")) // anchored to "none"
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T01:00:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	e.Dispatch(wa, wb, l1)

	groups, rep, err := e.Query("v")
	mustDo(t, err)
	if rep.Err == nil || rep.Err.Code != ErrNoTimezoneAtWrite {
		t.Fatalf("expected ErrNoTimezoneAtWrite, got %+v", rep.Err)
	}
	// a1 quarantined, b1 still placed; no duplication, no crash
	got := flatten(groups)
	if !equalStrings(got, []string{"2026-01-15/b1"}) {
		t.Fatalf("got %v", got)
	}
	checkViewInvariants(t, e, "v")
}

// TestErrMigrationInvalid: a write pinned to a timezone definition version
// whose migration failed validation.
func TestErrMigrationInvalid(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "UTC")) // v1
	mustDo(t, e.DefineTimezone("B", "UTC"))

	// migration to v2 fails validation (wrong expected base)
	if err := e.MigrateTimezone("A", "Europe/Berlin", 7); err == nil {
		t.Fatal("expected migration validation failure")
	} else if _, ok := err.(ErrMigrationFailed); !ok {
		t.Fatalf("expected ErrMigrationFailed, got %T", err)
	}
	// a client that believed v2 existed pins its write to v2
	wa, err := e.WritePinned("a1", "A", "ta", MustWall("2026-01-15T08:00:00"), 2)
	mustDo(t, err)
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T01:00:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	e.Dispatch(wa, wb, l1)

	groups, rep, err := e.Query("v")
	mustDo(t, err)
	if rep.Err == nil || rep.Err.Code != ErrMigrationInvalid {
		t.Fatalf("expected ErrMigrationInvalid, got %+v", rep.Err)
	}
	if got := flatten(groups); !equalStrings(got, []string{"2026-01-15/b1"}) {
		t.Fatalf("got %v", got)
	}
	checkViewInvariants(t, e, "v")
}

// TestErrPropertyDeprecated: the grouping property was deprecated by a type
// version migration; affected objects leave the view without duplication.
func TestErrPropertyDeprecated(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "UTC"))
	mustDo(t, e.DefineTimezone("B", "UTC"))

	wa, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T01:00:00"))
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T02:00:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	e.Dispatch(wa, wb, l1)
	if _, rep, _ := e.Query("v"); rep.Err != nil {
		t.Fatalf("unexpected error before deprecation: %v", rep.Err)
	}

	mustDo(t, e.DeprecateProperty("A", "ta"))
	groups, rep, err := e.Query("v")
	mustDo(t, err)
	if rep.Err == nil || rep.Err.Code != ErrPropertyDeprecated {
		t.Fatalf("expected ErrPropertyDeprecated, got %+v", rep.Err)
	}
	if got := flatten(groups); !equalStrings(got, []string{"2026-01-15/b1"}) {
		t.Fatalf("deprecated object still in view: %v", got)
	}
	checkViewInvariants(t, e, "v")
}

// TestErrLinkEndpointMissing: an endpoint object type of the link relation
// no longer exists.
func TestErrLinkEndpointMissing(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "UTC"))
	mustDo(t, e.DefineTimezone("B", "UTC"))

	wa, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T01:00:00"))
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T02:00:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	e.Dispatch(wa, wb, l1)
	if _, rep, _ := e.Query("v"); rep.Err != nil {
		t.Fatalf("unexpected error before deletion: %v", rep.Err)
	}

	mustDo(t, e.DeleteObjectType("B"))
	groups, rep, err := e.Query("v")
	mustDo(t, err)
	if rep.Err == nil || rep.Err.Code != ErrLinkEndpointMissing {
		t.Fatalf("expected ErrLinkEndpointMissing, got %+v", rep.Err)
	}
	if len(groups) != 0 {
		t.Fatalf("view not quarantined: %v", flatten(groups))
	}
	checkViewInvariants(t, e, "v")
}

// TestErrorPriority: when several categories are triggered in one
// maintenance pass, only the highest-priority one is reported, while every
// affected object is still quarantined (and audited) individually.
func TestErrorPriority(t *testing.T) {
	e := setupEnv(t)
	// A has NO timezone (E1 for a1); B has one.
	mustDo(t, e.DefineTimezone("B", "UTC"))

	// a2 will be pinned to a nonexistent version (E3).
	wa1, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T01:00:00"))
	wa2, _ := e.WritePinned("a2", "A", "ta", MustWall("2026-01-15T02:00:00"), 9)
	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T03:00:00"))
	l1, _ := e.Link("L", "a1", "A", "b1", "B")
	l2, _ := e.Link("L", "a2", "A", "b1", "B")
	e.Dispatch(wa1, wa2, wb, l1, l2)

	// also deprecate the grouping property so E4 conditions hold too
	mustDo(t, e.DeprecateProperty("A", "ta"))

	_, rep, err := e.Query("v")
	mustDo(t, err)
	if rep.Err == nil || rep.Err.Code != ErrNoTimezoneAtWrite {
		t.Fatalf("E1 must win over E3/E4, got %+v", rep.Err)
	}
	if !equalStrings(rep.Err.Objects, []string{"a1"}) {
		t.Fatalf("reported objects: %v", rep.Err.Objects)
	}

	// E2 dominates everything
	mustDo(t, e.DeleteObjectType("B"))
	_, rep, err = e.Query("v")
	mustDo(t, err)
	if rep.Err == nil || rep.Err.Code != ErrLinkEndpointMissing {
		t.Fatalf("E2 must dominate, got %+v", rep.Err)
	}

	// audit still records each object's own category
	audit, err := e.Audit("v")
	mustDo(t, err)
	codes := make(map[ErrCode]bool)
	for _, a := range audit {
		if a.Decision == "quarantined" {
			codes[a.ErrCode] = true
		}
	}
	for _, want := range []ErrCode{ErrNoTimezoneAtWrite, ErrMigrationInvalid, ErrLinkEndpointMissing} {
		if !codes[want] {
			t.Fatalf("audit missing quarantine for %v; entries=%+v", want, audit)
		}
	}
	checkViewInvariants(t, e, "v")
}

// TestMigrationValidationRules: rejected migrations consume no version.
func TestMigrationValidationRules(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "UTC"))
	if err := e.MigrateTimezone("A", "Not/AZone", 1); err == nil {
		t.Fatal("invalid zone must be rejected")
	}
	if err := e.MigrateTimezone("A", "Europe/Berlin", 3); err == nil {
		t.Fatal("non-sequential base must be rejected")
	}
	if got := e.CurrentTZVersion("A"); got != 1 {
		t.Fatalf("rejected migrations consumed a version: %d", got)
	}
	mustDo(t, e.MigrateTimezone("A", "Europe/Berlin", 1))
	if got := e.CurrentTZVersion("A"); got != 2 {
		t.Fatalf("version = %d, want 2", got)
	}
}
