package instance

import (
	"testing"
	"time"
)

func testStore() (*Store, *time.Time) {
	clock := time.Unix(1_700_000_000, 0)
	return newStoreWithClock(func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}), &clock
}

func TestCreateStartsVersionOne(t *testing.T) {
	store, _ := testStore()

	inst, err := store.Create("Person", "p1", map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if inst.Version != 1 {
		t.Fatalf("initial version = %d, want 1", inst.Version)
	}
	if inst.LastWriteTime.IsZero() {
		t.Fatal("LastWriteTime not set")
	}
	if inst.ObjectType != "Person" || inst.Key != "p1" {
		t.Fatalf("identity = %s/%s", inst.ObjectType, inst.Key)
	}

	got, err := store.Get("Person", "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != 1 || got.Attributes["name"] != "Ada" {
		t.Fatalf("Get returned %+v", got)
	}
}

func TestCreateDuplicateRejected(t *testing.T) {
	store, _ := testStore()
	_, _ = store.Create("Person", "p1", nil)

	_, err := store.Create("Person", "p1", nil)
	we, ok := AsWriteError(err)
	if !ok || we.Reason != ReasonAlreadyExists || we.Actual != 1 {
		t.Fatalf("err = %v, want AlreadyExists actual=1", err)
	}
}

func TestUpdateAdvancesVersionMonotonically(t *testing.T) {
	store, _ := testStore()
	inst, _ := store.Create("Person", "p1", nil)

	updated, err := store.Update("Person", "p1", inst.Version, map[string]any{"n": 2})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}

	_, err = store.Update("Person", "p1", 1, nil)
	we, ok := AsWriteError(err)
	if !ok || we.Reason != ReasonVersionConflict {
		t.Fatalf("err = %v, want version conflict", err)
	}
	if we.Expected != 1 || we.Actual != 2 {
		t.Fatalf("expected=%d actual=%d, want 1/2", we.Expected, we.Actual)
	}

	// Negative or zero expected versions never match.
	_, err = store.Update("Person", "p1", 0, nil)
	if we, ok := AsWriteError(err); !ok || we.Reason != ReasonVersionConflict {
		t.Fatalf("zero expected err = %v, want conflict", err)
	}
}

func TestVersionConflictMessageDistinguishesVersions(t *testing.T) {
	store, _ := testStore()
	_, _ = store.Create("Person", "p1", nil)
	_, err := store.Update("Person", "p1", 7, nil)
	if err == nil || err.Error() == "" {
		t.Fatal("expected descriptive error")
	}
	we, _ := AsWriteError(err)
	if we.Expected != 7 || we.Actual != 1 {
		t.Fatalf("versions not exposed: %+v", we)
	}
}

func TestUpdateAndDeleteNeverExistedDifferFromConflict(t *testing.T) {
	store, _ := testStore()

	_, err := store.Update("Person", "ghost", 1, nil)
	we, ok := AsWriteError(err)
	if !ok || we.Reason != ReasonNotFound {
		t.Fatalf("update ghost err = %v, want NotFound", err)
	}

	err = store.Delete("Person", "ghost", 1)
	if we, ok := AsWriteError(err); !ok || we.Reason != ReasonNotFound {
		t.Fatalf("delete ghost err = %v, want NotFound", err)
	}
}

func TestFailedUpdateDoesNotAdvanceVersionOrTime(t *testing.T) {
	store, _ := testStore()
	inst, _ := store.Create("Person", "p1", map[string]any{"v": 1})
	before := inst.LastWriteTime

	_, _ = store.Update("Person", "p1", inst.Version+1, map[string]any{"v": 2})
	got, _ := store.Get("Person", "p1")
	if got.Version != inst.Version || !got.LastWriteTime.Equal(before) {
		t.Fatalf("state changed after failed update: %+v", got)
	}
	if got.Attributes["v"] != 1 {
		t.Fatalf("attributes changed after failed update: %+v", got.Attributes)
	}
}
