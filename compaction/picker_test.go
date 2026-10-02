package compaction

import (
	"errors"
	"reflect"
	"testing"
)

func tf(id uint64, level int, min, max string, size int64) File {
	return File{ID: id, Level: level, MinKey: []byte(min), MaxKey: []byte(max), Size: size}
}

func mustNew(t *testing.T, L int, caps []int64, x int64) *Picker {
	t.Helper()
	p, err := NewPicker(L, caps, x)
	if err != nil {
		t.Fatalf("NewPicker(%d, %v, %d) failed: %v", L, caps, x, err)
	}
	return p
}

func mustAdd(t *testing.T, p *Picker, files ...File) {
	t.Helper()
	for _, f := range files {
		if err := p.AddFile(f); err != nil {
			t.Fatalf("AddFile(%+v) failed: %v", f, err)
		}
	}
}

func idsOf(files []File) []uint64 {
	ids := make([]uint64, len(files))
	for i, f := range files {
		ids[i] = f.ID
	}
	return ids
}

func mustPtr(t *testing.T, p *Picker, level int, wantSet bool, wantKey string) {
	t.Helper()
	key, set, err := p.Ptr(level)
	if err != nil {
		t.Fatalf("Ptr(%d) failed: %v", level, err)
	}
	if set != wantSet {
		t.Fatalf("Ptr(%d) set = %v, want %v", level, set, wantSet)
	}
	if wantSet && string(key) != wantKey {
		t.Fatalf("Ptr(%d) key = %q, want %q", level, key, wantKey)
	}
}

func TestConstructorValidation(t *testing.T) {
	cases := []struct {
		name string
		L    int
		caps []int64
		x    int64
		want error
	}{
		{"L too small", 1, nil, 10, ErrInvalidLevelCount},
		{"L zero", 0, nil, 10, ErrInvalidLevelCount},
		{"cap count mismatch", 3, []int64{10}, 10, ErrCapCountMismatch},
		{"cap count too many", 2, []int64{10, 20}, 10, ErrCapCountMismatch},
		{"cap zero", 2, []int64{0}, 10, ErrNonPositiveCap},
		{"cap negative", 3, []int64{10, -5}, 10, ErrNonPositiveCap},
		{"x zero", 2, []int64{10}, 0, ErrNonPositiveX},
		{"x negative", 2, []int64{10}, -1, ErrNonPositiveX},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewPicker(tc.L, tc.caps, tc.x)
			if !errors.Is(err, tc.want) {
				t.Fatalf("NewPicker(%d, %v, %d) err = %v, want %v", tc.L, tc.caps, tc.x, err, tc.want)
			}
		})
	}
	if _, err := NewPicker(2, []int64{10}, 1); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestAddFileValidationAndOrder(t *testing.T) {
	p := mustNew(t, 2, []int64{100}, 1000)
	mustAdd(t, p, tf(1, 1, "a", "c", 10))

	cases := []struct {
		name string
		f    File
		want error
	}{
		{"level out of range", tf(2, 3, "a", "b", 10), ErrLevelOutOfRange},
		{"level zero", tf(2, 0, "a", "b", 10), ErrLevelOutOfRange},
		{"bad key range", tf(2, 1, "d", "a", 10), ErrInvalidKeyRange},
		{"non-positive size", tf(2, 1, "x", "z", 0), ErrNonPositiveSize},
		{"negative size", tf(2, 1, "x", "z", -3), ErrNonPositiveSize},
		{"duplicate id", tf(1, 1, "x", "z", 10), ErrDuplicateFileID},
		{"overlap", tf(2, 1, "b", "d", 10), ErrOverlappingFile},
		{"touching endpoint overlaps", tf(2, 1, "c", "d", 10), ErrOverlappingFile},
		// Ordering: first failing check wins.
		{"level beats size", tf(1, 9, "a", "b", 0), ErrLevelOutOfRange},
		{"range beats duplicate id", tf(1, 1, "z", "a", 10), ErrInvalidKeyRange},
		{"size beats duplicate id", tf(1, 1, "x", "z", 0), ErrNonPositiveSize},
		{"duplicate beats overlap", tf(1, 1, "a", "c", 10), ErrDuplicateFileID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.AddFile(tc.f)
			if !errors.Is(err, tc.want) {
				t.Fatalf("AddFile(%+v) err = %v, want %v", tc.f, err, tc.want)
			}
		})
	}

	// Rejections must not change the file set.
	files, err := p.Files(1)
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(files); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("files after rejections = %v, want [1]", got)
	}
	mustPtr(t, p, 1, false, "")
}

func TestScoreExactlyOneCompacts(t *testing.T) {
	p := mustNew(t, 2, []int64{100}, 1000)
	mustAdd(t, p, tf(1, 1, "a", "c", 60), tf(2, 1, "d", "f", 40))
	t.Logf("input: level1 bytes=100 cap=100 score=1")
	inputs, overlaps, err := p.Pick()
	if err != nil {
		t.Fatalf("Pick with score exactly 1 failed: %v", err)
	}
	t.Logf("output: inputs=%v overlaps=%v", idsOf(inputs), idsOf(overlaps))
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("inputs = %v, want [1] (start file only)", got)
	}
	if len(overlaps) != 0 {
		t.Fatalf("overlaps = %v, want empty", idsOf(overlaps))
	}
	mustPtr(t, p, 1, true, "a")
}

func TestScoreJustBelowOneDoesNothing(t *testing.T) {
	p := mustNew(t, 2, []int64{100}, 1000)
	mustAdd(t, p, tf(1, 1, "a", "c", 99))
	t.Logf("input: level1 bytes=99 cap=100 score=0.99 < 1")
	_, _, err := p.Pick()
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("Pick err = %v, want ErrNothingToCompact", err)
	}
	// Failed pick must not touch pointers.
	mustPtr(t, p, 1, false, "")
}

func TestScoreTiePicksSmallerLevel(t *testing.T) {
	p := mustNew(t, 3, []int64{100, 200}, 10000)
	mustAdd(t, p,
		tf(1, 1, "a", "c", 100), // score 100/100 = 1
		tf(2, 2, "e", "g", 200), // score 200/200 = 1
	)
	t.Logf("input: level1 score=100/100, level2 score=200/200, tie -> level 1")
	inputs, _, err := p.Pick()
	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	t.Logf("output: inputs=%v (level %d)", idsOf(inputs), inputs[0].Level)
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("inputs = %v, want [1] (tie broken toward smaller level)", got)
	}
	mustPtr(t, p, 1, true, "a")
	mustPtr(t, p, 2, false, "")
}

func TestHigherScoreWins(t *testing.T) {
	p := mustNew(t, 3, []int64{100, 200}, 10000)
	mustAdd(t, p,
		tf(1, 1, "a", "c", 100), // score 1
		tf(2, 2, "e", "g", 300), // score 1.5
	)
	t.Logf("input: level1 score=1, level2 score=1.5 -> level 2")
	inputs, _, err := p.Pick()
	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{2}) {
		t.Fatalf("inputs = %v, want [2]", got)
	}
	mustPtr(t, p, 2, true, "e")
	mustPtr(t, p, 1, false, "")
}

func TestStartPtrEqualPicksNextAndWraps(t *testing.T) {
	// X=1 disables expansion (sizes are positive, so sum is never < 1).
	p := mustNew(t, 2, []int64{1}, 1)
	mustAdd(t, p,
		tf(1, 1, "10", "19", 1),
		tf(2, 1, "20", "29", 1),
		tf(3, 1, "30", "39", 1),
	)

	// ptr is "none" -> first file.
	inputs, _, err := p.Pick()
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("pick 1 inputs = %v, want [1]", got)
	}
	mustPtr(t, p, 1, true, "10")

	// ptr == min key of file 1 -> strictly greater -> file 2.
	t.Logf("ptr=%q equals min key of file 1; next start must be file 2", "10")
	inputs, _, err = p.Pick()
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{2}) {
		t.Fatalf("pick 2 inputs = %v, want [2]", got)
	}
	mustPtr(t, p, 1, true, "20")

	inputs, _, err = p.Pick()
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{3}) {
		t.Fatalf("pick 3 inputs = %v, want [3]", got)
	}
	mustPtr(t, p, 1, true, "30")

	// No min key strictly greater than "30" -> wrap to the first file.
	t.Logf("ptr=%q has no greater min key; wrap to file 1", "30")
	inputs, _, err = p.Pick()
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("pick 4 inputs = %v, want [1] after wrap", got)
	}
	mustPtr(t, p, 1, true, "10")
}

func TestOverlapsFromNextLevel(t *testing.T) {
	p := mustNew(t, 2, []int64{1}, 1)
	mustAdd(t, p,
		tf(1, 1, "10", "19", 1),
		tf(2, 2, "05", "09", 1), // disjoint, before
		tf(3, 2, "15", "18", 1), // overlaps
		tf(4, 2, "19", "30", 1), // touches endpoint 19 -> overlaps
		tf(5, 2, "31", "40", 1), // disjoint, after
	)
	inputs, overlaps, err := p.Pick()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("start=%v, level2 files 2..5, overlapping ids=%v", idsOf(inputs), idsOf(overlaps))
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("inputs = %v, want [1]", got)
	}
	if got := idsOf(overlaps); !reflect.DeepEqual(got, []uint64{3, 4}) {
		t.Fatalf("overlaps = %v, want [3 4]", got)
	}
}

func TestExpansionPositive(t *testing.T) {
	p := mustNew(t, 2, []int64{1}, 1000)
	mustAdd(t, p,
		tf(1, 1, "10", "19", 10),
		tf(2, 1, "20", "29", 10),
		tf(3, 1, "50", "59", 10),
		tf(4, 2, "15", "25", 10),
	)
	// Start=1, O={4}, R=[10,25], T={1,2}, bytes(T)+bytes(O)=30<1000,
	// hull(T)=[10,29] still only overlaps {4} -> expand.
	inputs, overlaps, err := p.Pick()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("expanded inputs=%v overlaps=%v", idsOf(inputs), idsOf(overlaps))
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{1, 2}) {
		t.Fatalf("inputs = %v, want [1 2]", got)
	}
	if got := idsOf(overlaps); !reflect.DeepEqual(got, []uint64{4}) {
		t.Fatalf("overlaps = %v, want [4]", got)
	}
	// Pointer advances to the largest min key among inputs.
	mustPtr(t, p, 1, true, "20")
}

func TestExpansionFailsWhenTIsOnlyStart(t *testing.T) {
	p := mustNew(t, 2, []int64{1}, 1000)
	mustAdd(t, p,
		tf(1, 1, "10", "19", 10),
		tf(2, 1, "50", "59", 10), // far away, not in R
		tf(3, 2, "15", "25", 10),
	)
	// R=[10,25]; T={1} only -> condition "T more than start" fails.
	inputs, _, err := p.Pick()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("T has only the start file; inputs=%v", idsOf(inputs))
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("inputs = %v, want [1]", got)
	}
	mustPtr(t, p, 1, true, "10")
}

func TestExpansionFailsWhenBytesEqualX(t *testing.T) {
	p := mustNew(t, 2, []int64{1}, 30)
	mustAdd(t, p,
		tf(1, 1, "10", "19", 10),
		tf(2, 1, "20", "29", 10),
		tf(3, 2, "15", "25", 10),
	)
	// bytes(T)+bytes(O) = 30 == X; strict less-than required -> no expansion.
	inputs, _, err := p.Pick()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("bytes(T)+bytes(O)=30 == X=30; inputs=%v", idsOf(inputs))
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("inputs = %v, want [1] (sum == X must not expand)", got)
	}
	mustPtr(t, p, 1, true, "10")
}

func TestExpansionFailsWhenOverlapSetGrows(t *testing.T) {
	p := mustNew(t, 2, []int64{1}, 1000)
	mustAdd(t, p,
		tf(1, 1, "10", "19", 10),
		tf(2, 1, "20", "29", 10),
		tf(3, 2, "15", "25", 10),
		tf(4, 2, "26", "30", 10),
	)
	// O={3}; R=[10,25]; T={1,2}; hull(T)=[10,29] also hits file 4 -> O' != O.
	inputs, overlaps, err := p.Pick()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("expansion would grow O to {3,4}; inputs=%v overlaps=%v", idsOf(inputs), idsOf(overlaps))
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("inputs = %v, want [1] (grown overlap set must not expand)", got)
	}
	if got := idsOf(overlaps); !reflect.DeepEqual(got, []uint64{3}) {
		t.Fatalf("overlaps = %v, want [3]", got)
	}
	mustPtr(t, p, 1, true, "10")
}

func TestPtrAdvancesToLargestInputMinKey(t *testing.T) {
	p := mustNew(t, 2, []int64{1}, 1000)
	mustAdd(t, p,
		tf(1, 1, "10", "19", 10),
		tf(2, 1, "20", "29", 10),
		tf(3, 1, "30", "39", 10),
		tf(4, 2, "15", "35", 10),
	)
	// O={4}, R=[10,35], T={1,2,3}, sum=40<1000, hull(T)=[10,39] overlaps only {4}.
	inputs, _, err := p.Pick()
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(inputs); !reflect.DeepEqual(got, []uint64{1, 2, 3}) {
		t.Fatalf("inputs = %v, want [1 2 3]", got)
	}
	mustPtr(t, p, 1, true, "30")
}

func TestQueryValidation(t *testing.T) {
	p := mustNew(t, 2, []int64{10}, 10)
	if _, err := p.Files(0); !errors.Is(err, ErrLevelOutOfRange) {
		t.Fatalf("Files(0) err = %v, want ErrLevelOutOfRange", err)
	}
	if _, err := p.Files(3); !errors.Is(err, ErrLevelOutOfRange) {
		t.Fatalf("Files(3) err = %v, want ErrLevelOutOfRange", err)
	}
	if _, _, err := p.Ptr(0); !errors.Is(err, ErrLevelOutOfRange) {
		t.Fatalf("Ptr(0) err = %v, want ErrLevelOutOfRange", err)
	}
}
