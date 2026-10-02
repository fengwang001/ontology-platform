package debounce

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, q, w int64, cap int) *Debouncer {
	t.Helper()
	d, err := New(q, w, cap)
	if err != nil {
		t.Fatalf("New(%d, %d, %d): %v", q, w, cap, err)
	}
	return d
}

func mustAdd(t *testing.T, d *Debouncer, now int64, kind Kind, path string) {
	t.Helper()
	if err := d.Add(now, Event{Kind: kind, Path: path}); err != nil {
		t.Fatalf("Add(%d, %v %q): %v", now, kind, path, err)
	}
}

func mustFlush(t *testing.T, d *Debouncer, now int64) []Entry {
	t.Helper()
	out, err := d.Flush(now)
	if err != nil {
		t.Fatalf("Flush(%d): %v", now, err)
	}
	return out
}

func TestConstructorValidationOrder(t *testing.T) {
	cases := []struct {
		q, w int64
		cap  int
		want error
	}{
		{0, 0, 0, ErrQuietTooSmall},
		{-5, 100, 10, ErrQuietTooSmall},
		{1, 0, 0, ErrMaxWaitTooSmall},
		{10, 9, 1, ErrMaxWaitTooSmall},
		{1, 1, 0, ErrCapTooSmall},
		{10, 10, -3, ErrCapTooSmall},
	}
	for _, c := range cases {
		if _, err := New(c.q, c.w, c.cap); !errors.Is(err, c.want) {
			t.Errorf("New(%d, %d, %d) = %v, want %v", c.q, c.w, c.cap, err, c.want)
		}
	}
	if _, err := New(1, 1, 1); err != nil {
		t.Errorf("New(1, 1, 1) = %v, want nil", err)
	}
}

func TestAtomicSaveDeleteThenCreateFoldsToModify(t *testing.T) {
	d := mustNew(t, 10, 100, 8)
	mustAdd(t, d, 0, Delete, "a")
	mustAdd(t, d, 5, Create, "a")
	got := mustFlush(t, d, 15)
	want := []Entry{{Path: "a", Net: NetModify, First: 0, Last: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flush = %v, want %v", got, want)
	}
}

func TestCreateThenDeleteCancelsAndNextEventStartsFresh(t *testing.T) {
	d := mustNew(t, 10, 100, 8)
	mustAdd(t, d, 0, Create, "a")
	mustAdd(t, d, 1, Delete, "a")
	if got := mustFlush(t, d, 11); len(got) != 0 {
		t.Fatalf("Flush after cancel = %v, want empty", got)
	}
	mustAdd(t, d, 20, Modify, "a")
	got := mustFlush(t, d, 30)
	want := []Entry{{Path: "a", Net: NetModify, First: 20, Last: 20}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flush = %v, want %v", got, want)
	}
}

func TestCreateDeleteCreateYieldsFreshCreate(t *testing.T) {
	d := mustNew(t, 10, 100, 8)
	mustAdd(t, d, 0, Create, "a")
	mustAdd(t, d, 1, Delete, "a")
	mustAdd(t, d, 7, Create, "a")
	got := mustFlush(t, d, 17)
	want := []Entry{{Path: "a", Net: NetCreate, First: 7, Last: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flush = %v, want %v", got, want)
	}
}

func TestQuietPeriodBoundary(t *testing.T) {
	const q = 10
	d := mustNew(t, q, 1000, 8)
	mustAdd(t, d, 0, Modify, "a")
	if got := mustFlush(t, d, q-1); len(got) != 0 {
		t.Fatalf("Flush at Q-1 = %v, want empty", got)
	}
	got := mustFlush(t, d, q)
	want := []Entry{{Path: "a", Net: NetModify, First: 0, Last: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flush at Q = %v, want %v", got, want)
	}
}

func TestMaxWaitBoundary(t *testing.T) {
	const w = 25
	d := mustNew(t, 10, w, 8)
	mustAdd(t, d, 0, Modify, "a")
	mustAdd(t, d, 9, Modify, "a")
	mustAdd(t, d, 18, Modify, "a")
	if got := mustFlush(t, d, w-1); len(got) != 0 {
		t.Fatalf("Flush at W-1 = %v, want empty", got)
	}
	got := mustFlush(t, d, w)
	want := []Entry{{Path: "a", Net: NetModify, First: 0, Last: 18}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flush at W = %v, want %v", got, want)
	}
}

func TestMaxWaitForcesFlushUnderContinuousEvents(t *testing.T) {
	d := mustNew(t, 10, 22, 8)
	for _, now := range []int64{0, 5, 10, 15, 20} {
		mustAdd(t, d, now, Modify, "a")
	}
	if got := mustFlush(t, d, 21); len(got) != 0 {
		t.Fatalf("Flush at 21 = %v, want empty", got)
	}
	got := mustFlush(t, d, 22)
	want := []Entry{{Path: "a", Net: NetModify, First: 0, Last: 20}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flush at 22 = %v, want %v", got, want)
	}
}

func TestDeleteDirSwallowsDescendantsOnly(t *testing.T) {
	d := mustNew(t, 10, 100, 8)
	mustAdd(t, d, 0, Create, "a/b")
	mustAdd(t, d, 0, Create, "ab/c")
	mustAdd(t, d, 1, DeleteDir, "a")
	got := mustFlush(t, d, 11)
	want := []Entry{
		{Path: "a", Net: NetDelete, First: 1, Last: 1},
		{Path: "ab/c", Net: NetCreate, First: 0, Last: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flush = %v, want %v", got, want)
	}
}

func TestDeleteDirCapacityCheckBeforeDescendantRemoval(t *testing.T) {
	d := mustNew(t, 10, 100, 1)
	mustAdd(t, d, 0, Create, "x/y")
	if err := d.Add(1, Event{Kind: DeleteDir, Path: "x"}); !errors.Is(err, ErrCapFull) {
		t.Fatalf("Add DeleteDir at full cap = %v, want ErrCapFull", err)
	}
	if got := d.Len(); got != 1 {
		t.Fatalf("Len after rejected DeleteDir = %d, want 1", got)
	}

	d2 := mustNew(t, 10, 100, 1)
	mustAdd(t, d2, 0, Create, "x")
	if err := d2.Add(1, Event{Kind: DeleteDir, Path: "x"}); err != nil {
		t.Fatalf("Add DeleteDir with own entry = %v, want nil", err)
	}
	if got := d2.Len(); got != 0 {
		t.Fatalf("Len after C+DeleteDir cancel = %d, want 0", got)
	}
}

func TestClockBackwardsRejectedWithoutSideEffects(t *testing.T) {
	d := mustNew(t, 10, 100, 8)
	mustAdd(t, d, 10, Modify, "a")
	if err := d.Add(5, Event{Kind: Create, Path: "b"}); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("Add backwards = %v, want ErrClockBackwards", err)
	}
	if _, err := d.Flush(9); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("Flush backwards = %v, want ErrClockBackwards", err)
	}
	if got := d.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1", got)
	}
	due, ok := d.NextDue()
	if !ok || due != 20 {
		t.Fatalf("NextDue = (%d, %v), want (20, true)", due, ok)
	}
	mustAdd(t, d, 10, Create, "b")
	got := mustFlush(t, d, 20)
	want := []Entry{
		{Path: "a", Net: NetModify, First: 10, Last: 10},
		{Path: "b", Net: NetCreate, First: 10, Last: 10},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flush = %v, want %v", got, want)
	}
}

func TestAddValidationOrder(t *testing.T) {
	d := mustNew(t, 10, 100, 1)
	if err := d.Add(0, Event{Kind: Kind(99), Path: "/bad"}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown kind vs bad path = %v, want ErrUnknownKind", err)
	}
	mustAdd(t, d, 10, Modify, "a")
	if err := d.Add(5, Event{Kind: Create, Path: ""}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("bad path vs clock = %v, want ErrInvalidPath", err)
	}
	if err := d.Add(5, Event{Kind: Create, Path: "b"}); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("clock vs cap = %v, want ErrClockBackwards", err)
	}
	if err := d.Add(10, Event{Kind: Create, Path: "b"}); !errors.Is(err, ErrCapFull) {
		t.Fatalf("cap full = %v, want ErrCapFull", err)
	}
	badPaths := []string{"", "/a", "a/", "a//b", "/"}
	for _, p := range badPaths {
		if err := d.Add(10, Event{Kind: Create, Path: p}); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("path %q = %v, want ErrInvalidPath", p, err)
		}
	}
}

func TestNextDue(t *testing.T) {
	d := mustNew(t, 10, 35, 8)
	if _, ok := d.NextDue(); ok {
		t.Fatal("NextDue on empty = true, want false")
	}
	mustAdd(t, d, 0, Modify, "a")
	mustAdd(t, d, 5, Modify, "b")
	mustAdd(t, d, 8, Modify, "b")
	due, ok := d.NextDue()
	if !ok || due != 10 {
		t.Fatalf("NextDue = (%d, %v), want (10, true)", due, ok)
	}
	mustFlush(t, d, 10)
	due, ok = d.NextDue()
	if !ok || due != 18 {
		t.Fatalf("NextDue = (%d, %v), want (18, true)", due, ok)
	}
}

func TestFlushSortedByPathByteOrder(t *testing.T) {
	d := mustNew(t, 10, 100, 8)
	mustAdd(t, d, 0, Modify, "b")
	mustAdd(t, d, 0, Modify, "a/b")
	mustAdd(t, d, 0, Modify, "a")
	mustAdd(t, d, 0, Modify, "A")
	got := mustFlush(t, d, 10)
	var paths []string
	for _, e := range got {
		paths = append(paths, e.Path)
	}
	want := []string{"A", "a", "a/b", "b"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestFoldTable(t *testing.T) {
	cases := []struct {
		name string
		ops  []Kind
		want []Entry
	}{
		{"C+Create", []Kind{Create, Create}, []Entry{{"p", NetCreate, 0, 1}}},
		{"C+Modify", []Kind{Create, Modify}, []Entry{{"p", NetCreate, 0, 1}}},
		{"C+DeleteDir", []Kind{Create, DeleteDir}, nil},
		{"M+Create", []Kind{Modify, Create}, []Entry{{"p", NetModify, 0, 1}}},
		{"M+Delete", []Kind{Modify, Delete}, []Entry{{"p", NetDelete, 0, 1}}},
		{"M+DeleteDir", []Kind{Modify, DeleteDir}, []Entry{{"p", NetDelete, 0, 1}}},
		{"D+Create", []Kind{Delete, Create}, []Entry{{"p", NetModify, 0, 1}}},
		{"D+Modify", []Kind{Delete, Modify}, []Entry{{"p", NetDelete, 0, 1}}},
		{"D+Delete", []Kind{Delete, Delete}, []Entry{{"p", NetDelete, 0, 1}}},
		{"D+DeleteDir", []Kind{Delete, DeleteDir}, []Entry{{"p", NetDelete, 0, 1}}},
		{"DeleteDir fresh", []Kind{DeleteDir}, []Entry{{"p", NetDelete, 0, 0}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := mustNew(t, 10, 100, 8)
			for i, k := range c.ops {
				mustAdd(t, d, int64(i), k, "p")
			}
			got := mustFlush(t, d, 1000)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Flush = %v, want %v", got, c.want)
			}
		})
	}
}

func TestConcurrentUseAndDeterminism(t *testing.T) {
	const q, w = 5, 30
	run := func() []Entry {
		d := mustNew(t, q, w, 16)
		var mu sync.Mutex
		var out []Entry
		var wg sync.WaitGroup
		for worker := 0; worker < 4; worker++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					now := int64(i)
					switch i % 3 {
					case 0:
						_ = d.Add(now, Event{Kind: Kind(i % 4), Path: string(rune('a'+worker)) + "/f"})
					case 1:
						entries, _ := d.Flush(now)
						mu.Lock()
						out = append(out, entries...)
						mu.Unlock()
					case 2:
						_, _ = d.NextDue()
					}
				}
			}(worker)
		}
		wg.Wait()
		return out
	}
	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("replay lengths differ: %d vs %d", len(first), len(second))
	}
}
