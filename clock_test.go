package clock

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func TestRejectsInvalidInputsWithoutStateChange(t *testing.T) {
	replacer, err := New(2)
	if err != nil {
		t.Fatalf("New(2) error: %v", err)
	}

	if _, err := New(0); !errors.Is(err, ErrInvalidFrameCount) {
		t.Fatalf("New(0) error = %v, want %v", err, ErrInvalidFrameCount)
	}

	if _, err := replacer.Access(-1, true); !errors.Is(err, ErrNegativePage) {
		t.Fatalf("Access(-1) error = %v, want %v", err, ErrNegativePage)
	}
	if _, err := replacer.Flush(-1); !errors.Is(err, ErrNegativePage) {
		t.Fatalf("Flush(-1) error = %v, want %v", err, ErrNegativePage)
	}

	if _, err := replacer.Flush(7); !errors.Is(err, ErrPageNotResident) {
		t.Fatalf("Flush(non-resident) error = %v, want %v", err, ErrPageNotResident)
	}

	if _, err := replacer.Access(7, false); err != nil {
		t.Fatalf("Access(7, read) error: %v", err)
	}
	if _, err := replacer.Flush(7); !errors.Is(err, ErrPageNotDirty) {
		t.Fatalf("Flush(clean) error = %v, want %v", err, ErrPageNotDirty)
	}

	after := replacer.Snapshot()
	want := Snapshot{
		Frames:     []Frame{{Page: 7, Occupied: true, Referenced: true}, {}},
		Pointer:    0,
		WriteBacks: 0,
	}
	assertSnapshotsEqual(t, after, want)
	t.Logf("input: New(2), Access(-1,write), Flush(-1), Flush(7), Access(7,read), Flush(7); output: errors=%v,%v,%v,%v; basis: negative page has priority and all rejected operations preserve frames, pointer and write-backs", ErrNegativePage, ErrNegativePage, ErrPageNotResident, ErrPageNotDirty)
}

func TestFourBitCombinationsPreferPassAThenPassB(t *testing.T) {
	tests := []struct {
		name          string
		states        []Frame
		pointer       int
		wantVictim    int
		wantPass      string
		wantDirty     bool
		wantPointer   int
		wantWriteBack int
	}{
		{
			name: "pass A selects clean unreferenced first",
			states: []Frame{
				{Page: 10, Occupied: true, Referenced: true, Dirty: true},
				{Page: 11, Occupied: true, Referenced: true, Dirty: false},
				{Page: 12, Occupied: true, Referenced: false, Dirty: true},
				{Page: 13, Occupied: true, Referenced: false, Dirty: false},
			},
			wantVictim:    3,
			wantPass:      "A",
			wantPointer:   0,
			wantWriteBack: 0,
		},
		{
			name: "pass B selects dirty unreferenced and clears earlier referenced bits",
			states: []Frame{
				{Page: 20, Occupied: true, Referenced: true, Dirty: false},
				{Page: 21, Occupied: true, Referenced: true, Dirty: true},
				{Page: 22, Occupied: true, Referenced: false, Dirty: true},
				{Page: 23, Occupied: true, Referenced: true, Dirty: true},
			},
			wantVictim:    2,
			wantPass:      "B",
			wantDirty:     true,
			wantPointer:   3,
			wantWriteBack: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			replacer := seededReplacer(t, tc.states, tc.pointer, 0)
			result, err := replacer.Access(99, false)
			if err != nil {
				t.Fatalf("Access error: %v", err)
			}
			if result.Eviction == nil {
				t.Fatal("Access result missing eviction")
			}
			if result.Eviction.Frame != tc.wantVictim || result.Eviction.SelectedPass != tc.wantPass ||
				result.Eviction.Dirty != tc.wantDirty || result.Eviction.WriteBacks != tc.wantWriteBack {
				t.Fatalf("eviction = frame %d pass %s dirty %t writeBacks %d, want frame %d pass %s dirty %t writeBacks %d",
					result.Eviction.Frame, result.Eviction.SelectedPass, result.Eviction.Dirty, result.Eviction.WriteBacks,
					tc.wantVictim, tc.wantPass, tc.wantDirty, tc.wantWriteBack)
			}
			got := replacer.Snapshot()
			if got.Pointer != tc.wantPointer || got.WriteBacks != tc.wantWriteBack {
				t.Fatalf("snapshot pointer/writeBacks = %d/%d, want %d/%d", got.Pointer, got.WriteBacks, tc.wantPointer, tc.wantWriteBack)
			}
			if tc.wantPass == "B" && got.Frames[0].Referenced {
				t.Fatal("frame scanned before B victim did not have its reference bit cleared")
			}
			t.Logf("input: states=%v pointer=%d Access(99,read); output: victim frame=%d pass=%s dirty=%t pointer=%d writeBacks=%d; basis: A accepts only (R=0,D=0), B accepts (R=0,D=1) and clears referenced frames before victim", tc.states, tc.pointer, result.Eviction.Frame, result.Eviction.SelectedPass, result.Eviction.Dirty, got.Pointer, got.WriteBacks)
		})
	}
}

func TestPassBClearsBitsThenNextPassAHits(t *testing.T) {
	replacer := seededReplacer(t, []Frame{
		{Page: 1, Occupied: true, Referenced: true, Dirty: false},
		{Page: 2, Occupied: true, Referenced: true, Dirty: true},
		{Page: 3, Occupied: true, Referenced: true, Dirty: false},
		{Page: 4, Occupied: true, Referenced: false, Dirty: true},
	}, 0, 0)

	first, err := replacer.Access(10, false)
	if err != nil {
		t.Fatalf("first Access error: %v", err)
	}
	if first.Eviction == nil || first.Eviction.Frame != 3 || first.Eviction.SelectedPass != "B" {
		t.Fatalf("first eviction = %+v, want frame 3 selected by B", first.Eviction)
	}
	if got := replacer.Snapshot(); got.Pointer != 0 {
		t.Fatalf("pointer after frame 3 = %d, want wrap to 0", got.Pointer)
	}

	second, err := replacer.Access(11, false)
	if err != nil {
		t.Fatalf("second Access error: %v", err)
	}
	if second.Eviction == nil || second.Eviction.Frame != 0 || second.Eviction.SelectedPass != "A" {
		t.Fatalf("second eviction = %+v, want frame 0 selected by A after B cleared its reference bit", second.Eviction)
	}

	got := replacer.Snapshot()
	t.Logf("input: all resident then Access(10), Access(11); output: first=%+v second=%+v pointer=%d writeBacks=%d; basis: first B clears frames 0-2, pointer wraps from 3 to 0, so next A selects frame 0", first.Eviction, second.Eviction, got.Pointer, got.WriteBacks)
}

func TestAllReferencedRequiresTwoRounds(t *testing.T) {
	replacer := seededReplacer(t, []Frame{
		{Page: 1, Occupied: true, Referenced: true, Dirty: false},
		{Page: 2, Occupied: true, Referenced: true, Dirty: false},
		{Page: 3, Occupied: true, Referenced: true, Dirty: false},
	}, 1, 0)

	result, err := replacer.Access(9, true)
	if err != nil {
		t.Fatalf("Access error: %v", err)
	}
	if result.Eviction == nil || result.Eviction.SelectedPass != "A" || result.Eviction.Frame != 1 {
		t.Fatalf("eviction = %+v, want frame 1 via A after A+B failed in round one", result.Eviction)
	}
	if got := replacer.Snapshot(); got.Pointer != 2 {
		t.Fatalf("pointer = %d, want 2", got.Pointer)
	}
	t.Logf("input: three frames all R=1 pointer=1 Access(9,write); output: victim=%+v pointer=2; basis: first A finds nothing, first B clears all bits, second A starts from pointer 1 and selects it", result.Eviction)
}

func TestFreeFrameLoadDoesNotMovePointerAndFlushKeepsReference(t *testing.T) {
	replacer, err := New(3)
	if err != nil {
		t.Fatalf("New error: %v", err)
	}

	first, err := replacer.Access(5, true)
	if err != nil {
		t.Fatalf("first Access error: %v", err)
	}
	if first.Frame != 0 || first.Hit {
		t.Fatalf("first result = %+v, want miss in smallest free frame 0", first)
	}

	second, err := replacer.Access(8, false)
	if err != nil {
		t.Fatalf("second Access error: %v", err)
	}
	if second.Frame != 1 {
		t.Fatalf("second frame = %d, want 1", second.Frame)
	}
	if got := replacer.Snapshot(); got.Pointer != 0 {
		t.Fatalf("pointer after free-frame loads = %d, want unchanged 0", got.Pointer)
	}

	flush, err := replacer.Flush(5)
	if err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	got := replacer.Snapshot()
	if flush.WriteBacks != 1 || got.WriteBacks != 1 || got.Frames[0].Dirty || !got.Frames[0].Referenced {
		t.Fatalf("flush=%+v frame0=%+v, want one write-back, dirty cleared and reference retained", flush, got.Frames[0])
	}
	t.Logf("input: Access(5,write), Access(8,read), Flush(5); output: frames=%d,%d flush=%+v pointer=%d; basis: free loads use lowest empty frame without moving pointer; flush changes only dirty bit and counts one write-back", first.Frame, second.Frame, flush, got.Pointer)
}

func TestConcurrentAccessFlushQueryIsSerializable(t *testing.T) {
	replacer := seededReplacer(t, []Frame{
		{Page: 0, Occupied: true, Referenced: false, Dirty: true},
		{Page: 1, Occupied: true, Referenced: false, Dirty: true},
		{Page: 2, Occupied: true, Referenced: false, Dirty: true},
		{Page: 3, Occupied: true, Referenced: false, Dirty: true},
	}, 0, 0)

	var wg sync.WaitGroup
	for page := 4; page < 40; page++ {
		wg.Add(1)
		go func(page int) {
			defer wg.Done()
			if _, err := replacer.Access(page, page%2 == 0); err != nil {
				t.Errorf("Access(%d) error: %v", page, err)
			}
		}(page)
	}
	for page := 0; page < 40; page++ {
		wg.Add(1)
		go func(page int) {
			defer wg.Done()
			_ = replacer.Snapshot()
			_, _ = replacer.Flush(page)
		}(page)
	}
	wg.Wait()

	got := replacer.Snapshot()
	seen := map[int]bool{}
	for _, frame := range got.Frames {
		if !frame.Occupied || seen[frame.Page] {
			t.Fatalf("invalid resident set after concurrent operations: %+v", got.Frames)
		}
		seen[frame.Page] = true
	}
	if len(seen) != len(got.Frames) {
		t.Fatalf("resident page count = %d, want %d", len(seen), len(got.Frames))
	}
	if got.WriteBacks < 0 {
		t.Fatalf("writeBacks = %d", got.WriteBacks)
	}
	t.Logf("input: 36 concurrent accesses plus concurrent flushes and snapshots; output: residents=%v pointer=%d writeBacks=%d; basis: mutex serialization preserves distinct resident pages and frame capacity", got.Frames, got.Pointer, got.WriteBacks)
}

func TestRandomOperationsMatchNaiveSimulationAndReplay(t *testing.T) {
	const frameCount = 5

	operations := make([]testOperation, 400)
	random := rand.New(rand.NewSource(994))
	for index := range operations {
		switch random.Intn(10) {
		case 0, 1, 2, 3, 4:
			operations[index] = testOperation{kind: "access", page: random.Intn(frameCount * 3), write: random.Intn(2) == 0}
		case 5, 6:
			operations[index] = testOperation{kind: "access", page: -1, write: random.Intn(2) == 0}
		case 7, 8:
			operations[index] = testOperation{kind: "flush", page: random.Intn(frameCount * 3)}
		default:
			operations[index] = testOperation{kind: "flush", page: -1}
		}
	}

	first := runOperationsAgainstClock(t, frameCount, operations)
	second := runOperationsAgainstClock(t, frameCount, operations)
	naive := runOperationsAgainstNaive(t, frameCount, operations)

	if !reflect.DeepEqual(first.snapshot, naive.snapshot) || !reflect.DeepEqual(first.accesses, naive.accesses) ||
		!reflect.DeepEqual(first.flushes, naive.flushes) || !equalErrors(first.errors, naive.errors) {
		t.Fatalf("clock does not match naive simulation\nclock snapshot: %+v\nnaive snapshot: %+v\nlogs:\n%s",
			first.snapshot, naive.snapshot, first.log)
	}
	if !reflect.DeepEqual(first.snapshot, second.snapshot) || !reflect.DeepEqual(first.accesses, second.accesses) ||
		!reflect.DeepEqual(first.flushes, second.flushes) {
		t.Fatalf("replay is not deterministic\nfirst snapshot: %+v\nsecond snapshot: %+v", first.snapshot, second.snapshot)
	}

	residents := map[int]bool{}
	for _, frame := range first.snapshot.Frames {
		if frame.Occupied {
			if residents[frame.Page] {
				t.Fatalf("duplicate resident page %d in %+v", frame.Page, first.snapshot.Frames)
			}
			residents[frame.Page] = true
		}
	}

	t.Logf("input: %d deterministic access/flush operations across %d frames; output: residents=%v pointer=%d writeBacks=%d; basis: every result and snapshot equals step-by-step naive simulation and identical replay", len(operations), frameCount, residents, first.snapshot.Pointer, first.snapshot.WriteBacks)
}

type testOperation struct {
	kind  string
	page  int
	write bool
}

type operationLog struct {
	snapshot Snapshot
	accesses []AccessResult
	flushes  []FlushResult
	errors   []error
	log      string
}

func runOperationsAgainstClock(t *testing.T, n int, operations []testOperation) operationLog {
	t.Helper()

	replacer, err := New(n)
	if err != nil {
		t.Fatalf("New(%d) error: %v", n, err)
	}

	result := operationLog{}
	for index, operation := range operations {
		switch operation.kind {
		case "access":
			access, err := replacer.Access(operation.page, operation.write)
			result.accesses = append(result.accesses, access)
			result.errors = append(result.errors, err)
			result.log += fmt.Sprintf("%d input=Access(page=%d,write=%t) output=%+v error=%v\n", index, operation.page, operation.write, access, err)
		case "flush":
			flush, err := replacer.Flush(operation.page)
			result.flushes = append(result.flushes, flush)
			result.errors = append(result.errors, err)
			result.log += fmt.Sprintf("%d input=Flush(page=%d) output=%+v error=%v\n", index, operation.page, flush, err)
		default:
			t.Fatalf("unknown operation kind %q", operation.kind)
		}
		result.snapshot = replacer.Snapshot()
		result.log += fmt.Sprintf("   snapshot=%+v\n", result.snapshot)
	}
	result.snapshot = replacer.Snapshot()
	return result
}

func runOperationsAgainstNaive(t *testing.T, n int, operations []testOperation) operationLog {
	t.Helper()

	replacer := newNaive(n)
	result := operationLog{}
	for _, operation := range operations {
		switch operation.kind {
		case "access":
			access, err := replacer.access(operation.page, operation.write)
			result.accesses = append(result.accesses, accessFromNaive(access))
			result.errors = append(result.errors, err)
		case "flush":
			flush, err := replacer.flush(operation.page)
			result.flushes = append(result.flushes, flushFromNaive(flush))
			result.errors = append(result.errors, err)
		default:
			t.Fatalf("unknown operation kind %q", operation.kind)
		}
	}
	result.snapshot = snapshotFromNaive(replacer.snapshot())
	return result
}

func accessFromNaive(got naiveAccessResult) AccessResult {
	result := AccessResult{
		Page:       got.page,
		Write:      got.write,
		Hit:        got.hit,
		Frame:      got.frame,
		WriteBacks: got.writeBacks,
	}
	if got.eviction != nil {
		result.Eviction = &Eviction{
			Frame:        got.eviction.frame,
			Page:         got.eviction.page,
			Dirty:        got.eviction.dirty,
			WriteBacks:   got.eviction.writeBacks,
			SelectedPass: got.eviction.selectedPass,
		}
	}
	return result
}

func flushFromNaive(got naiveFlushResult) FlushResult {
	return FlushResult{Page: got.page, Frame: got.frame, WriteBacks: got.writeBacks}
}

func snapshotFromNaive(got naiveSnapshot) Snapshot {
	frames := make([]Frame, len(got.frames))
	for index, frame := range got.frames {
		frames[index] = Frame{
			Page:       frame.page,
			Occupied:   frame.occupied,
			Referenced: frame.referenced,
			Dirty:      frame.dirty,
		}
	}
	return Snapshot{Frames: frames, Pointer: got.pointer, WriteBacks: got.writeBacks}
}

func equalErrors(left, right []error) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !errors.Is(left[index], right[index]) {
			return false
		}
	}
	return true
}

func seededReplacer(t *testing.T, frames []Frame, pointer int, writeBacks int) *ClockReplacer {
	t.Helper()

	replacer, err := New(len(frames))
	if err != nil {
		t.Fatalf("New(%d) error: %v", len(frames), err)
	}

	replacer.frames = append([]Frame(nil), frames...)
	replacer.pointer = pointer
	replacer.writeBacks = writeBacks
	return replacer
}

func assertSnapshotsEqual(t *testing.T, got, want Snapshot) {
	t.Helper()

	if got.Pointer != want.Pointer || got.WriteBacks != want.WriteBacks || fmt.Sprint(got.Frames) != fmt.Sprint(want.Frames) {
		t.Fatalf("snapshot = %+v, want %+v", got, want)
	}
}
