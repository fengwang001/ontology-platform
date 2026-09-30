package enhancedclock

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

type naiveFrame struct {
	page       int
	referenced bool
	dirty      bool
	occupied   bool
}

type naiveReplacer struct {
	frames     []naiveFrame
	pointer    int
	writeBacks int
}

type naiveAccessResult struct {
	err         error
	pageFault   bool
	evicted     bool
	evictedPage int
	writeBack   bool
	frame       int
	oldPointer  int
	newPointer  int
	victimPass  string
	scanned     []int
	cleared     []int
}

type operation struct {
	kind  string
	page  int
	write bool
}

func newNaive(frameCount int) *naiveReplacer {
	if frameCount < 1 {
		return nil
	}

	replacer := &naiveReplacer{
		frames:  make([]naiveFrame, frameCount),
		pointer: 0,
	}
	for index := range replacer.frames {
		replacer.frames[index].page = -1
	}
	return replacer
}

func (n *naiveReplacer) access(page int, write bool) naiveAccessResult {
	if page < 0 {
		return naiveAccessResult{err: ErrNegativePage}
	}

	result := naiveAccessResult{oldPointer: n.pointer, newPointer: n.pointer}
	for index, frame := range n.frames {
		if frame.occupied && frame.page == page {
			n.frames[index].referenced = true
			if write {
				n.frames[index].dirty = true
			}
			result.frame = index
			return result
		}
	}

	result.pageFault = true
	for index, frame := range n.frames {
		if !frame.occupied {
			n.frames[index] = naiveFrame{
				page:       page,
				referenced: true,
				dirty:      write,
				occupied:   true,
			}
			result.frame = index
			return result
		}
	}

	for {
		for offset := 0; offset < len(n.frames); offset++ {
			index := (n.pointer + offset) % len(n.frames)
			result.scanned = append(result.scanned, index)
			frame := n.frames[index]
			if !frame.referenced && !frame.dirty {
				return n.replace(index, "A", result, page, write)
			}
		}

		for offset := 0; offset < len(n.frames); offset++ {
			index := (n.pointer + offset) % len(n.frames)
			result.scanned = append(result.scanned, index)
			frame := n.frames[index]
			if frame.referenced {
				n.frames[index].referenced = false
				result.cleared = append(result.cleared, index)
			} else if frame.dirty {
				return n.replace(index, "B", result, page, write)
			}
		}
	}
}

func (n *naiveReplacer) replace(index int, pass string, result naiveAccessResult, page int, write bool) naiveAccessResult {
	old := n.frames[index]
	result.evicted = true
	result.evictedPage = old.page
	result.writeBack = old.dirty
	result.frame = index
	result.victimPass = pass
	if old.dirty {
		n.writeBacks++
	}
	n.frames[index] = naiveFrame{
		page:       page,
		referenced: true,
		dirty:      write,
		occupied:   true,
	}
	n.pointer = (index + 1) % len(n.frames)
	result.newPointer = n.pointer
	return result
}

func (n *naiveReplacer) flush(page int) error {
	if page < 0 {
		return ErrNegativePage
	}

	for index, frame := range n.frames {
		if frame.occupied && frame.page == page {
			if !frame.dirty {
				return ErrPageNotDirty
			}
			n.writeBacks++
			n.frames[index].dirty = false
			return nil
		}
	}

	return ErrPageNotResident
}

func TestReplacementRules(t *testing.T) {
	t.Run("pass A prefers referenced zero dirty zero", func(t *testing.T) {
		replacer, err := New(4)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		fillReplacer(t, replacer, 0, false)
		setFrame(replacer, 0, 10, true, false)
		setFrame(replacer, 1, 11, true, true)
		setFrame(replacer, 2, 12, false, true)
		setFrame(replacer, 3, 13, false, false)

		result, err := replacer.Access(99, false)
		if err != nil {
			t.Fatalf("Access() error = %v", err)
		}

		if !result.PageFault || !result.Evicted || result.EvictedPage != 13 || result.Frame != 3 {
			t.Fatalf("unexpected victim result: %+v", result)
		}
		if result.WriteBack || result.VictimPass != "A" || result.NewPointer != 0 {
			t.Fatalf("pass A should evict cleanly and wrap pointer: %+v", result)
		}
		if result.ScannedFrames[len(result.ScannedFrames)-1] != 3 || len(result.ClearedFrames) != 0 {
			t.Fatalf("pass A must not clear bits: %+v", result)
		}
	})

	t.Run("pass B selects referenced zero dirty one", func(t *testing.T) {
		replacer, _ := New(3)
		fillReplacer(t, replacer, 0, false)
		setFrame(replacer, 0, 10, true, false)
		setFrame(replacer, 1, 11, true, true)
		setFrame(replacer, 2, 12, false, true)

		result, err := replacer.Access(99, false)
		if err != nil {
			t.Fatalf("Access() error = %v", err)
		}

		if result.VictimPass != "B" || result.Frame != 2 || result.EvictedPage != 12 || !result.WriteBack {
			t.Fatalf("unexpected pass B victim: %+v", result)
		}
		if len(result.ScannedFrames) != 6 || result.NewPointer != 0 || replacer.WriteBacks() != 1 {
			t.Fatalf("pass B should scan A then B and count one writeback: %+v", result)
		}
		if !equalInts(result.ClearedFrames, []int{0, 1}) {
			t.Fatalf("B should clear referenced frames before victim, got %v", result.ClearedFrames)
		}
	})

	t.Run("cleared bits are visible on next pass A", func(t *testing.T) {
		replacer, _ := New(3)
		fillReplacer(t, replacer, 0, false)
		setFrame(replacer, 0, 10, true, false)
		setFrame(replacer, 1, 11, true, true)
		setFrame(replacer, 2, 12, false, true)

		first, err := replacer.Access(20, false)
		if err != nil {
			t.Fatalf("first Access() error = %v", err)
		}
		if first.Frame != 2 || first.VictimPass != "B" {
			t.Fatalf("unexpected first victim: %+v", first)
		}

		second, err := replacer.Access(30, false)
		if err != nil {
			t.Fatalf("second Access() error = %v", err)
		}
		if second.VictimPass != "A" || second.Frame != 0 || second.WriteBack {
			t.Fatalf("frame 0 should be available to next pass A: %+v", second)
		}
		if second.NewPointer != 1 {
			t.Fatalf("pointer should advance from frame 0: %+v", second)
		}
	})

	t.Run("all referenced needs A then B then another A", func(t *testing.T) {
		replacer, _ := New(3)
		fillReplacer(t, replacer, 10, false)

		result, err := replacer.Access(99, false)
		if err != nil {
			t.Fatalf("Access() error = %v", err)
		}

		if result.VictimPass != "A" || result.Frame != 0 || result.EvictedPage != 10 {
			t.Fatalf("second-round A should select frame 0: %+v", result)
		}
		if len(result.ScannedFrames) != 7 || !equalInts(result.ClearedFrames, []int{0, 1, 2}) {
			t.Fatalf("expected two full rounds and three clears, got scans=%v clears=%v", result.ScannedFrames, result.ClearedFrames)
		}
		if result.WriteBack || result.NewPointer != 1 || replacer.WriteBacks() != 0 {
			t.Fatalf("all-clean pages must not write back: %+v", result)
		}
	})

	t.Run("empty frame uses lowest index without moving pointer", func(t *testing.T) {
		replacer, _ := New(4)
		fillReplacer(t, replacer, 0, false)
		setPointer(replacer, 2)
		setFrame(replacer, 3, 3, true, false)
		clearFrame(replacer, 1)

		result, err := replacer.Access(99, true)
		if err != nil {
			t.Fatalf("Access() error = %v", err)
		}
		if !result.PageFault || result.Evicted || result.Frame != 1 || result.OldPointer != 2 || result.NewPointer != 2 {
			t.Fatalf("lowest empty frame must be used without pointer change: %+v", result)
		}
		if state := replacer.Snapshot(); state.Frames[1].Page != 99 || !state.Frames[1].Referenced || !state.Frames[1].Dirty || state.Pointer != 2 {
			t.Fatalf("new page must be referenced and dirty with unchanged pointer: %+v", state)
		}
	})

	t.Run("flush clears dirty but leaves referenced", func(t *testing.T) {
		replacer, _ := New(2)
		fillReplacer(t, replacer, 0, true)
		setPointer(replacer, 1)

		if err := replacer.Flush(1); err != nil {
			t.Fatalf("Flush() error = %v", err)
		}

		state := replacer.Snapshot()
		if !state.Frames[1].Occupied || !state.Frames[1].Referenced || state.Frames[1].Dirty {
			t.Fatalf("flush must clear only dirty bit: %+v", state.Frames[1])
		}
		if state.WriteBacks != 1 || state.Pointer != 1 {
			t.Fatalf("flush must count writeback and leave pointer: %+v", state)
		}
	})
}

func setPointer(replacer *Replacer, pointer int) {
	replacer.mu.Lock()
	defer replacer.mu.Unlock()
	replacer.pointer = pointer
}

func clearFrame(replacer *Replacer, index int) {
	replacer.mu.Lock()
	defer replacer.mu.Unlock()
	replacer.frames[index] = Frame{Page: -1}
}

func equalInts(got []int, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func fillReplacer(t *testing.T, replacer *Replacer, start int, dirtyOdd bool) {
	t.Helper()
	for page := start; page < start+len(replacer.frames); page++ {
		if _, err := replacer.Access(page, dirtyOdd && page%2 == 1); err != nil {
			t.Fatalf("fill Access(%d) error = %v", page, err)
		}
	}
}

func setFrame(replacer *Replacer, index int, page int, referenced bool, dirty bool) {
	replacer.mu.Lock()
	defer replacer.mu.Unlock()
	replacer.frames[index] = Frame{
		Page:       page,
		Referenced: referenced,
		Dirty:      dirty,
		Occupied:   true,
	}
}

func TestRejectedOperations(t *testing.T) {
	_, err := New(0)
	if !errorsIs(err, ErrInvalidFrameCount) {
		t.Fatalf("New(0) error = %v, want %v", err, ErrInvalidFrameCount)
	}

	replacer, err := New(2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fillReplacer(t, replacer, 0, true)
	setPointer(replacer, 1)
	before := replacer.Snapshot()

	if _, err := replacer.Access(-1, true); !errorsIs(err, ErrNegativePage) {
		t.Fatalf("Access(-1) error = %v, want %v", err, ErrNegativePage)
	}
	if err := replacer.Flush(-1); !errorsIs(err, ErrNegativePage) {
		t.Fatalf("Flush(-1) error = %v, want %v", err, ErrNegativePage)
	}
	if err := replacer.Flush(40); !errorsIs(err, ErrPageNotResident) {
		t.Fatalf("Flush(40) error = %v, want %v", err, ErrPageNotResident)
	}
	if err := replacer.Flush(0); !errorsIs(err, ErrPageNotDirty) {
		t.Fatalf("Flush(0) error = %v, want %v", err, ErrPageNotDirty)
	}

	after := replacer.Snapshot()
	if !statesEqual(before, after) {
		t.Fatalf("rejected operations changed state\nbefore: %+v\nafter:  %+v", before, after)
	}
}

func errorsIs(got error, want error) bool {
	return got == want
}

func statesEqual(left State, right State) bool {
	if left.Pointer != right.Pointer || left.WriteBacks != right.WriteBacks || len(left.Frames) != len(right.Frames) {
		return false
	}
	for index := range left.Frames {
		if left.Frames[index] != right.Frames[index] {
			return false
		}
	}
	return true
}

func TestNaiveModel(t *testing.T) {
	operations := []operation{
		{kind: "access", page: 0, write: false},
		{kind: "access", page: 1, write: true},
		{kind: "access", page: 2, write: false},
		{kind: "access", page: 1, write: false},
		{kind: "flush", page: 1},
		{kind: "access", page: 3, write: true},
		{kind: "access", page: 4, write: true},
		{kind: "access", page: 0, write: true},
		{kind: "access", page: 5, write: false},
		{kind: "access", page: 2, write: true},
		{kind: "access", page: 6, write: false},
		{kind: "flush", page: 4},
		{kind: "access", page: 7, write: false},
	}

	actual, err := New(3)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	model := newNaive(3)

	for step, operation := range operations {
		t.Run(fmt.Sprintf("step_%02d_%s_page_%d", step+1, operation.kind, operation.page), func(t *testing.T) {
			var actualResult AccessResult
			var actualErr error
			var modelResult naiveAccessResult
			var modelErr error

			switch operation.kind {
			case "access":
				actualResult, actualErr = actual.Access(operation.page, operation.write)
				modelResult = model.access(operation.page, operation.write)
				modelErr = modelResult.err
			case "flush":
				actualErr = actual.Flush(operation.page)
				modelErr = model.flush(operation.page)
			}

			t.Logf("input=%+v actualError=%v modelError=%v actualResult=%+v modelResult=%+v decision=compare_error_result_frames_pointer_writebacks",
				operation, actualErr, modelErr, actualResult, modelResult)

			if !sameError(actualErr, modelErr) {
				t.Fatalf("error mismatch: actual=%v model=%v", actualErr, modelErr)
			}
			if actualErr == nil && operation.kind == "access" && !accessResultsEqual(actualResult, modelResult) {
				t.Fatalf("access result mismatch:\nactual=%+v\nmodel=%+v", actualResult, modelResult)
			}

			actualState := actual.Snapshot()
			if !statesMatchModel(actualState, model) {
				t.Fatalf("state mismatch:\nactual=%+v\nmodel=%+v", actualState, model.frames)
			}
			t.Logf("judgment=serializable state matches frames=%v pointer=%d writeBacks=%d",
				actualState.Frames, actualState.Pointer, actualState.WriteBacks)
		})
	}
}

func sameError(actual error, expected error) bool {
	return actual == expected
}

func accessResultsEqual(actual AccessResult, expected naiveAccessResult) bool {
	return actual.PageFault == expected.pageFault &&
		actual.Evicted == expected.evicted &&
		actual.EvictedPage == expected.evictedPage &&
		actual.WriteBack == expected.writeBack &&
		actual.Frame == expected.frame &&
		actual.OldPointer == expected.oldPointer &&
		actual.NewPointer == expected.newPointer &&
		actual.VictimPass == expected.victimPass &&
		equalInts(actual.ScannedFrames, expected.scanned) &&
		equalInts(actual.ClearedFrames, expected.cleared)
}

func statesMatchModel(actual State, model *naiveReplacer) bool {
	if actual.Pointer != model.pointer || actual.WriteBacks != model.writeBacks || len(actual.Frames) != len(model.frames) {
		return false
	}
	for index := range actual.Frames {
		left := actual.Frames[index]
		right := model.frames[index]
		if left.Page != right.page || left.Referenced != right.referenced || left.Dirty != right.dirty || left.Occupied != right.occupied {
			return false
		}
	}
	return true
}

func TestConcurrentAccessFlushQuery(t *testing.T) {
	replacer, err := New(4)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fillReplacer(t, replacer, 0, true)

	var wait sync.WaitGroup
	var dirtyEvictions int64
	var successfulFlushes int64

	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for iteration := 0; iteration < 80; iteration++ {
				page := (worker*7 + iteration*3) % 16
				write := iteration%2 == 0

				if worker%3 == 0 && iteration%5 == 0 {
					if err := replacer.Flush(page); err == nil {
						atomic.AddInt64(&successfulFlushes, 1)
					}
				} else {
					result, err := replacer.Access(page, write)
					if err != nil {
						t.Errorf("Access(%d, %t) error = %v", page, write, err)
						return
					}
					if result.WriteBack {
						atomic.AddInt64(&dirtyEvictions, 1)
					}
				}

				state := replacer.Snapshot()
				if !invariantHolds(state) {
					t.Errorf("invariant violated: %+v", state)
					return
				}
			}
		}(worker)
	}

	wait.Wait()
	state := replacer.Snapshot()
	expectedWriteBacks := atomic.LoadInt64(&dirtyEvictions) + atomic.LoadInt64(&successfulFlushes)
	t.Logf("input=8_workers_x_80_mixed_operations output=writeBacks=%d dirtyEvictions=%d successfulFlushes=%d judgment=counter_parts_and_residency_invariants_match",
		state.WriteBacks, dirtyEvictions, successfulFlushes)
	if int64(state.WriteBacks) != expectedWriteBacks {
		t.Fatalf("writebacks = %d, want %d", state.WriteBacks, expectedWriteBacks)
	}
	if !invariantHolds(state) {
		t.Fatalf("final invariant violated: %+v", state)
	}
}

func invariantHolds(state State) bool {
	seen := make(map[int]bool)
	count := 0
	for _, frame := range state.Frames {
		if !frame.Occupied {
			continue
		}
		count++
		if frame.Page < 0 || seen[frame.Page] {
			return false
		}
		seen[frame.Page] = true
	}
	return count <= len(state.Frames)
}

func TestDeterministicReplay(t *testing.T) {
	first, _ := New(3)
	second, _ := New(3)

	for page := 0; page < 40; page++ {
		write := page%3 != 0
		firstResult, firstErr := first.Access(page, write)
		secondResult, secondErr := second.Access(page, write)
		if firstErr != secondErr {
			t.Fatalf("page %d errors differ: %v vs %v", page, firstErr, secondErr)
		}
		t.Logf("input=Access(page=%d,write=%t) output=%+v judgment=replay_result_must_be_identical", page, write, firstResult)
		if !accessResultsEqual(firstResult, naiveAccessResult{
			pageFault:   secondResult.PageFault,
			evicted:     secondResult.Evicted,
			evictedPage: secondResult.EvictedPage,
			writeBack:   secondResult.WriteBack,
			frame:       secondResult.Frame,
			oldPointer:  secondResult.OldPointer,
			newPointer:  secondResult.NewPointer,
			victimPass:  secondResult.VictimPass,
			scanned:     secondResult.ScannedFrames,
			cleared:     secondResult.ClearedFrames,
		}) {
			t.Fatalf("page %d replay result differs:\nfirst=%+v\nsecond=%+v", page, firstResult, secondResult)
		}
		if !statesEqual(first.Snapshot(), second.Snapshot()) {
			t.Fatalf("page %d replay state differs:\nfirst=%+v\nsecond=%+v", page, first.Snapshot(), second.Snapshot())
		}
	}
}
