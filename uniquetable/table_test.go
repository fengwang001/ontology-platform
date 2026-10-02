package uniquetable

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustTable(t *testing.T, deferrable, initiallyDeferred bool) *Table {
	t.Helper()
	tbl, err := New(deferrable, initiallyDeferred)
	if err != nil {
		t.Fatalf("New(%v, %v): %v", deferrable, initiallyDeferred, err)
	}
	return tbl
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func violationKey(t *testing.T, err error) string {
	t.Helper()
	var verr *ViolationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *ViolationError, got %v", err)
	}
	return verr.Key
}

func mustKeys(t *testing.T, tbl *Table, want ...Entry) {
	t.Helper()
	got := tbl.Keys()
	if len(got) == 0 {
		got = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
}

func TestConstructorValidation(t *testing.T) {
	if _, err := New(false, true); !errors.Is(err, ErrInvalidInitialMode) {
		t.Fatalf("New(false, true) = %v, want ErrInvalidInitialMode", err)
	}
	for _, cfg := range [][2]bool{{false, false}, {true, false}, {true, true}} {
		if _, err := New(cfg[0], cfg[1]); err != nil {
			t.Fatalf("New(%v, %v): %v", cfg[0], cfg[1], err)
		}
	}
}

func TestSwapKeysDeferrableImmediateSucceeds(t *testing.T) {
	tbl := mustTable(t, true, false)
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Insert("r1", Str("a")), Insert("r2", Str("b"))}))
	must(t, tbl.Commit())

	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Update("r1", Str("b")), Update("r2", Str("a"))}))
	mustKeys(t, tbl,
		Entry{Row: "r1", Key: Str("b")},
		Entry{Row: "r2", Key: Str("a")},
	)
	must(t, tbl.Commit())
}

func TestSwapKeysNonDeferrableFailsAtFirstOp(t *testing.T) {
	tbl := mustTable(t, false, false)
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Insert("r1", Str("a")), Insert("r2", Str("b"))}))
	must(t, tbl.Commit())

	must(t, tbl.Begin())
	err := tbl.Apply([]Op{Update("r1", Str("b")), Update("r2", Str("a"))})
	if got := violationKey(t, err); got != "b" {
		t.Fatalf("violation key = %q, want %q", got, "b")
	}
	// The whole statement is undone; the transaction is still valid.
	mustKeys(t, tbl,
		Entry{Row: "r1", Key: Str("a")},
		Entry{Row: "r2", Key: Str("b")},
	)
	must(t, tbl.Apply([]Op{Update("r1", Str("c"))}))
	must(t, tbl.Commit())
	mustKeys(t, tbl,
		Entry{Row: "r1", Key: Str("c")},
		Entry{Row: "r2", Key: Str("b")},
	)
}

func TestTwoStatementsDeferredCommitSucceeds(t *testing.T) {
	tbl := mustTable(t, true, true)
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Insert("r1", Str("a")), Insert("r2", Str("b"))}))
	must(t, tbl.Commit())

	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Update("r1", Str("b"))}))
	must(t, tbl.Apply([]Op{Update("r2", Str("a"))}))
	must(t, tbl.Commit())
	mustKeys(t, tbl,
		Entry{Row: "r1", Key: Str("b")},
		Entry{Row: "r2", Key: Str("a")},
	)
}

func TestTwoStatementsImmediateFirstFails(t *testing.T) {
	tbl := mustTable(t, true, false)
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Insert("r1", Str("a")), Insert("r2", Str("b"))}))
	must(t, tbl.Commit())

	must(t, tbl.Begin())
	err := tbl.Apply([]Op{Update("r1", Str("b"))})
	if got := violationKey(t, err); got != "b" {
		t.Fatalf("violation key = %q, want %q", got, "b")
	}
	mustKeys(t, tbl,
		Entry{Row: "r1", Key: Str("a")},
		Entry{Row: "r2", Key: Str("b")},
	)
	must(t, tbl.Rollback())
}

func TestDeferredCommitViolationRollsBackWholeTx(t *testing.T) {
	tbl := mustTable(t, true, true)
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Insert("r1", Str("a")), Insert("r2", Str("b"))}))
	must(t, tbl.Commit())

	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Update("r1", Str("b"))}))
	must(t, tbl.Apply([]Op{Insert("r3", Str("c"))}))
	err := tbl.Commit()
	if got := violationKey(t, err); got != "b" {
		t.Fatalf("violation key = %q, want %q", got, "b")
	}
	// The whole transaction is rolled back: committed state is unchanged.
	mustKeys(t, tbl,
		Entry{Row: "r1", Key: Str("a")},
		Entry{Row: "r2", Key: Str("b")},
	)
	if _, found := tbl.Get("r3"); found {
		t.Fatal("r3 must not exist after commit rollback")
	}
	// No transaction remains active.
	if err := tbl.Rollback(); !errors.Is(err, ErrNoTx) {
		t.Fatalf("Rollback after failed Commit = %v, want ErrNoTx", err)
	}
}

func TestSetModeImmediateRejectedThenFixed(t *testing.T) {
	tbl := mustTable(t, true, true)
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Insert("r1", Str("x")), Insert("r2", Str("x"))}))

	err := tbl.SetMode(Immediate)
	if got := violationKey(t, err); got != "x" {
		t.Fatalf("violation key = %q, want %q", got, "x")
	}
	// Still deferred: no statement-level checks happen.
	must(t, tbl.Apply([]Op{Insert("r3", Str("x"))}))
	// Fix all duplicates, then the switch succeeds.
	must(t, tbl.Apply([]Op{Update("r2", Str("y")), Update("r3", Str("z"))}))
	must(t, tbl.SetMode(Immediate))
	must(t, tbl.Commit())
	mustKeys(t, tbl,
		Entry{Row: "r1", Key: Str("x")},
		Entry{Row: "r2", Key: Str("y")},
		Entry{Row: "r3", Key: Str("z")},
	)
}

func TestNullsCoexistAndEmptyStringDistinct(t *testing.T) {
	tbl := mustTable(t, false, false)
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{
		Insert("n1", nil),
		Insert("n2", nil),
		Insert("e1", Str("")),
	}))
	must(t, tbl.Commit())
	mustKeys(t, tbl,
		Entry{Row: "e1", Key: Str("")},
		Entry{Row: "n1", Key: nil},
		Entry{Row: "n2", Key: nil},
	)

	must(t, tbl.Begin())
	err := tbl.Apply([]Op{Insert("e2", Str(""))})
	if got := violationKey(t, err); got != "" {
		t.Fatalf("violation key = %q, want empty string", got)
	}
	must(t, tbl.Apply([]Op{Insert("n3", nil)}))
	must(t, tbl.Commit())
}

func TestFailedStatementUndoesEarlierOps(t *testing.T) {
	tbl := mustTable(t, false, false)
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Insert("r1", Str("a"))}))
	must(t, tbl.Commit())

	must(t, tbl.Begin())
	err := tbl.Apply([]Op{
		Insert("ok1", Str("k1")),
		Update("r1", Str("k2")),
		Insert("ok2", Str("k2")),
	})
	if got := violationKey(t, err); got != "k2" {
		t.Fatalf("violation key = %q, want %q", got, "k2")
	}
	mustKeys(t, tbl, Entry{Row: "r1", Key: Str("a")})
	must(t, tbl.Rollback())
}

func TestMultipleViolationsReturnSmallestKey(t *testing.T) {
	tbl := mustTable(t, true, true)
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{
		Insert("r1", Str("m")),
		Insert("r2", Str("m")),
		Insert("r3", Str("a")),
		Insert("r4", Str("a")),
	}))
	err := tbl.Commit()
	if got := violationKey(t, err); got != "a" {
		t.Fatalf("violation key = %q, want %q", got, "a")
	}
	mustKeys(t, tbl)
}

func TestErrorOrdering(t *testing.T) {
	tbl := mustTable(t, false, false)

	if err := tbl.Apply(nil); !errors.Is(err, ErrNoTx) {
		t.Fatalf("Apply without tx = %v, want ErrNoTx", err)
	}
	if err := tbl.SetMode(Immediate); !errors.Is(err, ErrNoTx) {
		t.Fatalf("SetMode without tx = %v, want ErrNoTx", err)
	}
	if err := tbl.Commit(); !errors.Is(err, ErrNoTx) {
		t.Fatalf("Commit without tx = %v, want ErrNoTx", err)
	}
	if err := tbl.Rollback(); !errors.Is(err, ErrNoTx) {
		t.Fatalf("Rollback without tx = %v, want ErrNoTx", err)
	}

	must(t, tbl.Begin())
	if err := tbl.Begin(); !errors.Is(err, ErrTxActive) {
		t.Fatalf("Begin inside tx = %v, want ErrTxActive", err)
	}
	// Invalid mode is reported before non-deferrable.
	if err := tbl.SetMode(Mode(99)); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("SetMode(99) = %v, want ErrInvalidMode", err)
	}
	if err := tbl.SetMode(Deferred); !errors.Is(err, ErrNotDeferrable) {
		t.Fatalf("SetMode(Deferred) on non-deferrable = %v, want ErrNotDeferrable", err)
	}

	// First op error wins inside a statement.
	if err := tbl.Apply([]Op{Insert("", Str("x"))}); !errors.Is(err, ErrEmptyRow) {
		t.Fatalf("empty row = %v, want ErrEmptyRow", err)
	}
	must(t, tbl.Apply([]Op{Insert("r1", Str("a"))}))
	if err := tbl.Apply([]Op{Insert("r1", Str("b")), Delete("ghost")}); !errors.Is(err, ErrRowExists) {
		t.Fatalf("duplicate insert = %v, want ErrRowExists", err)
	}
	if err := tbl.Apply([]Op{Update("ghost", Str("b"))}); !errors.Is(err, ErrRowNotFound) {
		t.Fatalf("update missing = %v, want ErrRowNotFound", err)
	}
	if err := tbl.Apply([]Op{Delete("ghost")}); !errors.Is(err, ErrRowNotFound) {
		t.Fatalf("delete missing = %v, want ErrRowNotFound", err)
	}
	must(t, tbl.Rollback())
}

func TestGetAndKeysViews(t *testing.T) {
	tbl := mustTable(t, true, false)
	if _, found := tbl.Get("r1"); found {
		t.Fatal("Get on empty table must report not found")
	}
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Insert("b", Str("2")), Insert("a", Str("1"))}))
	// Transactional view is visible inside the tx.
	mustKeys(t, tbl,
		Entry{Row: "a", Key: Str("1")},
		Entry{Row: "b", Key: Str("2")},
	)
	k, found := tbl.Get("a")
	if !found || k == nil || *k != "1" {
		t.Fatalf("Get(a) = %v, %v; want \"1\", true", k, found)
	}
	must(t, tbl.Commit())
	// Committed state is visible without a tx.
	mustKeys(t, tbl,
		Entry{Row: "a", Key: Str("1")},
		Entry{Row: "b", Key: Str("2")},
	)
}

func TestConcurrentBeginExactlyOneSucceeds(t *testing.T) {
	tbl := mustTable(t, true, false)
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = tbl.Begin()
		}(i)
	}
	wg.Wait()
	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrTxActive) {
			t.Fatalf("unexpected Begin error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d concurrent Begins succeeded, want exactly 1", succeeded)
	}
	must(t, tbl.Rollback())
}

func TestConcurrentOpsCommittedStateStaysUnique(t *testing.T) {
	tbl := mustTable(t, true, false)
	must(t, tbl.Begin())
	must(t, tbl.Apply([]Op{Insert("r1", Str("a"))}))
	must(t, tbl.Commit())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = tbl.Get("r1")
				_ = tbl.Keys()
			}
		}()
	}
	wg.Wait()
	mustKeys(t, tbl, Entry{Row: "r1", Key: Str("a")})
}
