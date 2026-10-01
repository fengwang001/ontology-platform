package ontology

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, q, w, cap int64) *Debouncer {
	t.Helper()
	d, err := NewDebouncer(q, w, cap)
	if err != nil {
		t.Fatalf("NewDebouncer(%d,%d,%d): %v", q, w, cap, err)
	}
	return d
}

func mustAdd(t *testing.T, d *Debouncer, now int64, ev Event) {
	t.Helper()
	if err := d.Add(now, ev); err != nil {
		t.Fatalf("Add(%d, %+v): %v", now, ev, err)
	}
}

func mustFlush(t *testing.T, d *Debouncer, now int64) []Entry {
	t.Helper()
	got, err := d.Flush(now)
	if err != nil {
		t.Fatalf("Flush(%d): %v", now, err)
	}
	return got
}

func pendingCount(d *Debouncer) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending)
}

func assertEntries(t *testing.T, got, want []Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("entries len: got %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDeleteThenCreateFoldsToModifyWithStableFirst(t *testing.T) {
	// Atomic save: Delete then Create folds to M; first keeps the Delete time.
	d := mustNew(t, 10, 100, 4)
	mustAdd(t, d, 5, Event{Kind: Delete, Path: "f"})
	mustAdd(t, d, 8, Event{Kind: Create, Path: "f"})

	got := mustFlush(t, d, 18)
	want := []Entry{{Path: "f", Kind: NetModify, First: 5, Last: 8}}
	t.Logf("input=[Delete@5 Create@8] Flush@18; due: 18-last(8)=10>=Q10; output=%v", got)
	assertEntries(t, got, want)
}

func TestCreateThenDeleteCancelsAndLaterEventStartsFresh(t *testing.T) {
	d := mustNew(t, 10, 100, 4)
	mustAdd(t, d, 1, Event{Kind: Create, Path: "f"})
	mustAdd(t, d, 2, Event{Kind: Delete, Path: "f"})
	if n := pendingCount(d); n != 0 {
		t.Fatalf("entry should be cancelled, pending=%d", n)
	}
	t.Log("input=[Create@1 Delete@2]; C+Delete cancels; pending=0")

	mustAdd(t, d, 5, Event{Kind: Modify, Path: "f"})
	got := mustFlush(t, d, 15)
	want := []Entry{{Path: "f", Kind: NetModify, First: 5, Last: 5}}
	t.Logf("then Modify@5 (no existing entry => fresh M) Flush@15; due: 15-5=10>=Q10; output=%v", got)
	assertEntries(t, got, want)
}

func TestCreateDeleteCreateRestartsCreateAtNewTime(t *testing.T) {
	d := mustNew(t, 10, 100, 4)
	mustAdd(t, d, 1, Event{Kind: Create, Path: "f"})
	mustAdd(t, d, 2, Event{Kind: Delete, Path: "f"})
	mustAdd(t, d, 9, Event{Kind: Create, Path: "f"})

	got := mustFlush(t, d, 10)
	t.Logf("input=[Create@1 Delete@2 Create@9] Flush@10; 10-9=1<Q10 and <W100; output=%v (want empty)", got)
	assertEntries(t, got, nil)

	got = mustFlush(t, d, 19)
	want := []Entry{{Path: "f", Kind: NetCreate, First: 9, Last: 9}}
	t.Logf("Flush@19; due: 19-9=10>=Q10; output=%v", got)
	assertEntries(t, got, want)
}

func TestQuietBoundaryExactlyQAndOneLess(t *testing.T) {
	d := mustNew(t, 10, 100, 4)
	mustAdd(t, d, 0, Event{Kind: Modify, Path: "f"})
	got := mustFlush(t, d, 9)
	t.Logf("Modify@0 Flush@9; 9-0=9<Q10 and 9<W100; output=%v (want empty)", got)
	assertEntries(t, got, nil)

	got = mustFlush(t, d, 10)
	want := []Entry{{Path: "f", Kind: NetModify, First: 0, Last: 0}}
	t.Logf("Flush@10; 10-0=10>=Q10; output=%v", got)
	assertEntries(t, got, want)
}

func TestMaxWaitBoundaryExactlyWAndOneLess(t *testing.T) {
	// Constant activity keeps the quiet period from elapsing; only first+W fires.
	d := mustNew(t, 10, 10, 4)
	for now := int64(1); now <= 9; now++ {
		mustAdd(t, d, now, Event{Kind: Modify, Path: "f"})
	}
	got := mustFlush(t, d, 10)
	t.Logf("Modify@1..9 Flush@10; 10-first(1)=9<W10, 10-last(9)=1<Q10; output=%v (want empty)", got)
	assertEntries(t, got, nil)

	got = mustFlush(t, d, 11)
	want := []Entry{{Path: "f", Kind: NetModify, First: 1, Last: 9}}
	t.Logf("Flush@11; 11-first(1)=10>=W10; output=%v", got)
	assertEntries(t, got, want)
}

func TestContinuousEventsForcedByMaxWait(t *testing.T) {
	d := mustNew(t, 5, 7, 4)
	mustAdd(t, d, 0, Event{Kind: Create, Path: "f"})
	mustAdd(t, d, 2, Event{Kind: Modify, Path: "f"})
	mustAdd(t, d, 4, Event{Kind: Modify, Path: "f"})
	got := mustFlush(t, d, 7)
	want := []Entry{{Path: "f", Kind: NetCreate, First: 0, Last: 4}}
	t.Logf("Create@0 Modify@2 Modify@4 Flush@7; quiet 7-last(4)=3<Q5 but 7-first(0)=7>=W7; output=%v", got)
	assertEntries(t, got, want)
}

func TestDeleteDirSwallowsChildrenButNotSiblingPrefix(t *testing.T) {
	d := mustNew(t, 10, 100, 8)
	mustAdd(t, d, 1, Event{Kind: Create, Path: "a/b"})
	mustAdd(t, d, 1, Event{Kind: Create, Path: "a/b/c"})
	mustAdd(t, d, 1, Event{Kind: Create, Path: "ab/c"})
	mustAdd(t, d, 1, Event{Kind: Create, Path: "a"})

	mustAdd(t, d, 2, Event{Kind: DeleteDir, Path: "a"})
	got := mustFlush(t, d, 12)
	want := []Entry{{Path: "ab/c", Kind: NetCreate, First: 1, Last: 1}}
	t.Logf("DeleteDir a@2 removes prefix \"a/\" (a/b, a/b/c); a itself C+Delete cancels; ab/c kept; output=%v", got)
	assertEntries(t, got, want)
}

func TestDeleteDirCapacityRules(t *testing.T) {
	// Full capacity and no entry for the DeleteDir path itself => rejected
	// before any descendant is removed.
	d := mustNew(t, 10, 100, 2)
	mustAdd(t, d, 1, Event{Kind: Create, Path: "a/b"})
	mustAdd(t, d, 1, Event{Kind: Create, Path: "a/c"})
	err := d.Add(2, Event{Kind: DeleteDir, Path: "a"})
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("want ErrCapacityExceeded, got %v", err)
	}
	if n := pendingCount(d); n != 2 {
		t.Fatalf("rejected DeleteDir must leave entries intact, pending=%d", n)
	}
	t.Logf("full cap, no entry for a: DeleteDir rejected (%v); pending=%d intact", err, pendingCount(d))

	// The path itself being present means capacity does not block the op.
	d2 := mustNew(t, 10, 100, 2)
	mustAdd(t, d2, 1, Event{Kind: Create, Path: "d"})
	mustAdd(t, d2, 1, Event{Kind: Create, Path: "d/c"})
	mustAdd(t, d2, 2, Event{Kind: DeleteDir, Path: "d"})
	if n := pendingCount(d2); n != 0 {
		t.Fatalf("DeleteDir should swallow d/c and cancel d, pending=%d", n)
	}
	t.Log("entry for d exists at full cap: DeleteDir accepted; d/c swallowed, d cancelled; pending=0")
}

func TestClockRewindRejectedWithoutSideEffects(t *testing.T) {
	d := mustNew(t, 10, 100, 4)
	mustAdd(t, d, 10, Event{Kind: Create, Path: "f"})

	if err := d.Add(9, Event{Kind: Modify, Path: "f"}); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("Add rewind: want ErrClockRewind, got %v", err)
	}
	got := mustFlush(t, d, 20)
	want := []Entry{{Path: "f", Kind: NetCreate, First: 10, Last: 10}}
	t.Logf("Add Modify@9 after Add@10 rejected (%v); rejected op changed nothing: output=%v", ErrClockRewind, got)
	assertEntries(t, got, want)

	d3 := mustNew(t, 10, 100, 4)
	mustAdd(t, d3, 10, Event{Kind: Create, Path: "g"})
	if _, err := d3.Flush(9); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("Flush rewind: want ErrClockRewind, got %v", err)
	}
	t.Logf("Flush@9 after Add@10 rejected (%v); pending unchanged", ErrClockRewind)
}

func TestConstructorValidationOrder(t *testing.T) {
	cases := []struct {
		name       string
		q, w, c    int64
		wantErr    error
		wantAccept bool
	}{
		{"Q<1 reported before W and Cap", 0, 0, 0, ErrInvalidQuietPeriod, false},
		{"W<Q reported before Cap", 5, 4, 0, ErrInvalidWaitLimit, false},
		{"Cap<1 reported last", 5, 5, 0, ErrInvalidCapacity, false},
		{"boundaries W==Q and cap 1 valid", 5, 5, 1, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewDebouncer(tc.q, tc.w, tc.c)
			if tc.wantAccept {
				if err != nil || d == nil {
					t.Fatalf("want accepted, got d=%v err=%v", d, err)
				}
				t.Logf("New(%d,%d,%d) accepted", tc.q, tc.w, tc.c)
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
			t.Logf("New(%d,%d,%d) rejected with %v (priority order)", tc.q, tc.w, tc.c, err)
		})
	}
}

func TestAddErrorPriorityAndInvalidPaths(t *testing.T) {
	d := mustNew(t, 10, 100, 1)
	mustAdd(t, d, 5, Event{Kind: Create, Path: "f"}) // cap full, f exists

	if err := d.Add(4, Event{Kind: EventKind(99), Path: ""}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown kind must win, got %v", err)
	}
	for _, bad := range []string{"", "/abs", "trail/", "a//b"} {
		if err := d.Add(4, Event{Kind: Modify, Path: bad}); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("path %q: want ErrInvalidPath, got %v", bad, err)
		}
	}
	if err := d.Add(4, Event{Kind: Create, Path: "g"}); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("clock rewind must beat capacity, got %v", err)
	}
	if err := d.Add(6, Event{Kind: Create, Path: "g"}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("capacity reported last, got %v", err)
	}
	mustAdd(t, d, 6, Event{Kind: Modify, Path: "f"}) // existing path bypasses cap
	t.Log("error priority verified: unknown kind < invalid path < clock rewind < capacity; existing path bypasses cap")
}

func TestNextDueAndFlushOrder(t *testing.T) {
	d := mustNew(t, 10, 20, 8)
	if _, ok := d.NextDue(); ok {
		t.Fatal("NextDue on empty debouncer should report none")
	}
	mustAdd(t, d, 1, Event{Kind: Create, Path: "b"}) // min(last+10=11, first+20=21)=11
	mustAdd(t, d, 2, Event{Kind: Create, Path: "a"}) // min(12, 22)=12

	if next, ok := d.NextDue(); !ok || next != 11 {
		t.Fatalf("NextDue: got (%d,%v), want (11,true)", next, ok)
	}
	got := mustFlush(t, d, 11)
	// Only b is due at 11 (11-last(1)=10>=Q); a: 11-2=9<Q and 11-2<W.
	want := []Entry{{Path: "b", Kind: NetCreate, First: 1, Last: 1}}
	t.Logf("NextDue=11; Flush@11 emits only b, a stays; output=%v", got)
	assertEntries(t, got, want)

	got = mustFlush(t, d, 12)
	want = []Entry{{Path: "a", Kind: NetCreate, First: 2, Last: 2}}
	t.Logf("Flush@12 emits a in byte order; output=%v", got)
	assertEntries(t, got, want)
}
