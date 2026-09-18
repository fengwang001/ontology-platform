package instance

import "testing"

func TestDeleteIsLogicalAndRetainsVersion(t *testing.T) {
	store, _ := testStore()
	inst, _ := store.Create("Person", "p1", nil)
	updated, _ := store.Update("Person", "p1", inst.Version, nil)

	if err := store.Delete("Person", "p1", updated.Version); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := store.Get("Person", "p1")
	we, ok := AsWriteError(err)
	if !ok || we.Reason != ReasonDeleted {
		t.Fatalf("Get after delete err = %v, want Deleted", err)
	}
	// Delete is itself a write: 1 create, 1 update, 1 delete -> version 3.
	if we.Actual != 3 {
		t.Fatalf("deleted actual version = %d, want 3", we.Actual)
	}
}

func TestDeletedIsDistinctFromConflictAndNotFound(t *testing.T) {
	store, _ := testStore()
	inst, _ := store.Create("Person", "p1", nil)
	_ = store.Delete("Person", "p1", inst.Version)

	_, err := store.Update("Person", "p1", 1, nil)
	we, ok := AsWriteError(err)
	if !ok {
		t.Fatalf("want WriteError, got %T", err)
	}
	if we.Reason != ReasonDeleted {
		t.Fatalf("update deleted = %v, want Deleted", we.Reason)
	}

	err = store.Delete("Person", "p1", 1)
	if we, _ := AsWriteError(err); we.Reason != ReasonDeleted {
		t.Fatalf("delete deleted = %v, want Deleted (not NotFound/conflict)", we.Reason)
	}

	_, err = store.Get("Person", "never")
	if we, _ := AsWriteError(err); we.Reason != ReasonNotFound {
		t.Fatalf("get unknown = %v, want NotFound", we.Reason)
	}
}

func TestDeleteVersionConflictKeepsInstanceAlive(t *testing.T) {
	store, _ := testStore()
	inst, _ := store.Create("Person", "p1", nil)

	err := store.Delete("Person", "p1", inst.Version+1)
	we, ok := AsWriteError(err)
	if !ok || we.Reason != ReasonVersionConflict || we.Expected != 2 || we.Actual != 1 {
		t.Fatalf("err = %v, want conflict 2 vs 1", err)
	}

	got, err := store.Get("Person", "p1")
	if err != nil || got.Version != 1 {
		t.Fatalf("instance must remain alive at v1, got %v %v", got, err)
	}
}

func TestResurrectContinuesVersion(t *testing.T) {
	store, _ := testStore()
	inst, _ := store.Create("Person", "p1", nil)
	_ = store.Delete("Person", "p1", inst.Version) // version is now 2

	// Recreating the same primary key resurrects; version must not reset to 1.
	revived, err := store.Create("Person", "p1", map[string]any{"state": "back"})
	if err != nil {
		t.Fatalf("resurrect Create: %v", err)
	}
	if revived.Version != 3 {
		t.Fatalf("resurrected version = %d, want 3", revived.Version)
	}
	if revived.Deleted {
		t.Fatal("resurrected instance must not be marked deleted")
	}

	got, err := store.Get("Person", "p1")
	if err != nil {
		t.Fatalf("Get resurrected: %v", err)
	}
	if got.Version != 3 || got.Attributes["state"] != "back" {
		t.Fatalf("resurrected snapshot wrong: %+v", got)
	}

	// Subsequent updates continue from 3; an update expecting 1 conflicts.
	_, err = store.Update("Person", "p1", 1, nil)
	if we, _ := AsWriteError(err); we.Reason != ReasonVersionConflict {
		t.Fatalf("stale update on resurrected = %v, want conflict", err)
	}
	again, err := store.Update("Person", "p1", 3, nil)
	if err != nil || again.Version != 4 {
		t.Fatalf("update after resurrect: %v %+v", err, again)
	}
}

func TestRepeatedDeleteCreateCyclesKeepClimbing(t *testing.T) {
	store, _ := testStore()
	inst, _ := store.Create("Widget", "w", nil) // v1
	v := inst.Version
	for want := int64(2); want <= 6; want++ {
		if want%2 == 0 {
			if err := store.Delete("Widget", "w", v); err != nil {
				t.Fatalf("cycle delete at v%d: %v", v, err)
			}
		} else {
			if _, err := store.Create("Widget", "w", nil); err != nil {
				t.Fatalf("cycle create: %v", err)
			}
		}
		v = want
		if want%2 == 1 {
			r, err := store.Get("Widget", "w")
			if err != nil || r.Version != want {
				t.Fatalf("live version at cycle %d = %v (%v), want %d", want, r, err, want)
			}
		}
	}
}
