package ftl

import (
	"errors"
	"reflect"
	"testing"
)

func testConfig() Config {
	return Config{
		BlockCount:    6,
		PagesPerBlock: 4,
		LogicalPages:  18,
		LowWatermark:  2,
		HighWatermark: 4,
		EraseLimit:    100,
		WearThreshold: 2,
	}
}

func mustNew(t *testing.T, cfg Config) *FTL {
	t.Helper()
	f, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return f
}

func requireErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func writes(t *testing.T, f *FTL, lpns ...int) {
	t.Helper()
	for _, lpn := range lpns {
		if err := f.Write(lpn); err != nil {
			t.Fatalf("Write(%d) error = %v", lpn, err)
		}
	}
}

func requirePhysical(t *testing.T, f *FTL, lpn int, want PhysicalPage) {
	t.Helper()
	got, err := f.Read(lpn)
	if err != nil {
		t.Fatalf("Read(%d) error = %v", lpn, err)
	}
	if got != want {
		t.Fatalf("Read(%d) = %+v, want %+v", lpn, got, want)
	}
}

func fillBlock(t *testing.T, f *FTL, start int) {
	t.Helper()
	for i := 0; i < f.cfg.PagesPerBlock; i++ {
		writes(t, f, start+i)
	}
}

func countPageStates(pages [][]PageInfo) (valid, invalid, free int) {
	for blockPages := range pages {
		for _, current := range pages[blockPages] {
			switch current.State {
			case PageValid:
				valid++
			case PageInvalid:
				invalid++
			case PageFree:
				free++
			}
		}
	}
	return valid, invalid, free
}

func TestReadAndDiscardErrors(t *testing.T) {
	f := mustNew(t, testConfig())

	_, err := f.Read(-1)
	requireErrIs(t, err, ErrInvalidArgument)
	_, err = f.Read(18)
	requireErrIs(t, err, ErrInvalidArgument)
	_, err = f.Read(0)
	requireErrIs(t, err, ErrUnwritten)

	writes(t, f, 0)
	requirePhysical(t, f, 0, PhysicalPage{0, 0})
	requireErrIs(t, f.Discard(0), nil)
	_, err = f.Read(0)
	requireErrIs(t, err, ErrUnwritten)
	requireErrIs(t, f.Discard(0), nil)
	requireErrIs(t, f.Discard(18), ErrInvalidArgument)
}

func TestSequentialProgrammingAndBlockSelection(t *testing.T) {
	f := mustNew(t, testConfig())
	fillBlock(t, f, 0)

	for pageID := 0; pageID < 4; pageID++ {
		requirePhysical(t, f, pageID, PhysicalPage{0, pageID})
	}
	if f.Blocks()[0].State != BlockFull {
		t.Fatalf("first block state = %v, want full", f.Blocks()[0].State)
	}
	if got := f.Stats().FreeBlocks; got != 5 {
		t.Fatalf("free blocks = %d, want 5", got)
	}

	writes(t, f, 4)
	requirePhysical(t, f, 4, PhysicalPage{1, 0})
}

func TestStatisticsConservation(t *testing.T) {
	f := mustNew(t, testConfig())
	writes(t, f, 0, 1, 2, 3, 0, 1, 4)

	stats := f.Stats()
	if stats.LogicalWrites != 7 || stats.PhysicalPrograms != 7 {
		t.Fatalf("stats = %+v, want 7 logical and 7 physical", stats)
	}
	valid, invalid, freePages := countPageStates(f.PhysicalPages())
	if valid != f.mappedPages || invalid != 2 || freePages != 17 {
		t.Fatalf("valid=%d invalid=%d free=%d mapped=%d", valid, invalid, freePages, f.mappedPages)
	}
	num, den := f.WriteAmplification()
	if num != 7 || den != 7 {
		t.Fatalf("write amplification = %d/%d, want 7/7", num, den)
	}
}

func assertModelEqual(t *testing.T, actual *FTL, reference *NaiveFTL) {
	t.Helper()
	if !reflect.DeepEqual(actual.Stats(), reference.Stats()) {
		t.Fatalf("stats differ: actual=%+v reference=%+v", actual.Stats(), reference.Stats())
	}
	if !reflect.DeepEqual(actual.Blocks(), reference.Blocks()) {
		t.Fatalf("blocks differ:\nactual=%+v\nreference=%+v", actual.Blocks(), reference.Blocks())
	}
	if !reflect.DeepEqual(actual.PhysicalPages(), reference.PhysicalPages()) {
		t.Fatalf("physical pages differ:\nactual=%v\nreference=%v",
			actual.PhysicalPages(), reference.PhysicalPages())
	}
	if !reflect.DeepEqual(actual.mapping, reference.mapping) {
		t.Fatalf("mapping differ: actual=%v reference=%v", actual.mapping, reference.mapping)
	}
}
