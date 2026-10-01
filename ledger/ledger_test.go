package ledger

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

type modelFile struct {
	allocated int64
	delayed   int64
	dirtySeq  int64
	dirty     bool
	exists    bool
}

type modelLedger struct {
	total          int64
	systemReserved int64
	blocksPerIndex int64
	watermark      int64
	files          map[int64]modelFile
	writeCounter   int64
}

type modelState struct {
	free      int64
	reserved  int64
	available bool
	value     int64
}

func newModelLedger(total, system, entries, watermark int64) *modelLedger {
	return &modelLedger{
		total:          total,
		systemReserved: system,
		blocksPerIndex: entries,
		watermark:      watermark,
		files:          make(map[int64]modelFile),
	}
}

func (m *modelLedger) indexBlocks(blocks int64) int64 {
	if blocks == 0 {
		return 0
	}
	return (blocks + m.blocksPerIndex - 1) / m.blocksPerIndex
}

func (m *modelLedger) used() int64 {
	var total int64
	for _, file := range m.files {
		total += file.allocated + m.indexBlocks(file.allocated)
	}
	return total
}

func (m *modelLedger) free() int64 {
	return m.total - m.used()
}

func (m *modelLedger) reserved() int64 {
	var total int64
	for _, file := range m.files {
		total += file.delayed + m.indexBlocks(file.allocated+file.delayed) -
			m.indexBlocks(file.allocated)
	}
	return total
}

func (m *modelLedger) available(privileged bool) int64 {
	value := m.free() - m.reserved()
	if !privileged {
		value -= m.systemReserved
	}
	if value < 0 {
		return 0
	}
	return value
}

func (m *modelLedger) dirty() []int64 {
	fileIDs := make([]int64, 0)
	for fileID, file := range m.files {
		if file.dirty {
			fileIDs = append(fileIDs, fileID)
		}
	}
	for i := 0; i < len(fileIDs); i++ {
		for j := i + 1; j < len(fileIDs); j++ {
			if m.files[fileIDs[j]].dirtySeq < m.files[fileIDs[i]].dirtySeq {
				fileIDs[i], fileIDs[j] = fileIDs[j], fileIDs[i]
			}
		}
	}
	return fileIDs
}

func (m *modelLedger) totalDelayed() int64 {
	var total int64
	for _, file := range m.files {
		total += file.delayed
	}
	return total
}

func (m *modelLedger) flush(fileID int64) {
	file := m.files[fileID]
	file.allocated += file.delayed
	file.delayed = 0
	file.dirty = false
	file.dirtySeq = 0
	m.files[fileID] = file
}

func (m *modelLedger) flushFile(fileID int64) error {
	if fileID < 0 {
		return ErrInvalidArgument
	}
	file, exists := m.files[fileID]
	if !exists {
		return ErrFileNotFound
	}
	if file.delayed == 0 {
		return ErrNoDelayedBlocks
	}
	m.flush(fileID)
	return nil
}

func (m *modelLedger) write(fileID, blocks int64, privileged bool) ([]int64, error) {
	if fileID < 0 || blocks <= 0 {
		return nil, ErrInvalidArgument
	}

	before := m.files[fileID]
	allocated := before.allocated
	delayed := before.delayed
	currentSize := allocated + delayed
	nextSize := currentSize + blocks
	if nextSize < currentSize || nextSize > m.total {
		return nil, ErrOutOfSpace
	}

	extra := blocks + m.indexBlocks(nextSize) - m.indexBlocks(currentSize)
	if extra > m.available(privileged) {
		return nil, ErrOutOfSpace
	}

	m.writeCounter++
	before.exists = true
	before.delayed += blocks
	if delayed == 0 {
		before.dirty = true
		before.dirtySeq = m.writeCounter
	}
	m.files[fileID] = before

	flushed := make([]int64, 0)
	for m.totalDelayed() > m.watermark {
		oldestID := int64(-1)
		var oldestSeq int64
		for candidateID, candidate := range m.files {
			if candidate.dirty && (oldestID < 0 || candidate.dirtySeq < oldestSeq) {
				oldestID = candidateID
				oldestSeq = candidate.dirtySeq
			}
		}
		m.flush(oldestID)
		flushed = append(flushed, oldestID)
	}
	return flushed, nil
}

func (m *modelLedger) truncate(fileID, blocks int64) error {
	if fileID < 0 || blocks <= 0 {
		return ErrInvalidArgument
	}
	file, exists := m.files[fileID]
	if !exists {
		return ErrFileNotFound
	}
	if blocks > file.allocated+file.delayed {
		return ErrFileTooLarge
	}

	fromDelayed := min(blocks, file.delayed)
	file.delayed -= fromDelayed
	fromAllocated := min(blocks-fromDelayed, file.allocated)
	file.allocated -= fromAllocated
	if file.delayed == 0 {
		file.dirty = false
		file.dirtySeq = 0
	}
	m.files[fileID] = file
	return nil
}

func (m *modelLedger) unlink(fileID int64) error {
	if fileID < 0 {
		return ErrInvalidArgument
	}
	if _, exists := m.files[fileID]; !exists {
		return ErrFileNotFound
	}
	delete(m.files, fileID)
	return nil
}

func assertQueries(t *testing.T, ledger *Ledger, model *modelLedger) {
	t.Helper()
	if got := ledger.Free(); got != model.free() {
		t.Fatalf("Free() = %d, want %d", got, model.free())
	}
	if got := ledger.Reserved(); got != model.reserved() {
		t.Fatalf("Reserved() = %d, want %d", got, model.reserved())
	}
	if got := ledger.Available(false); got != model.available(false) {
		t.Fatalf("Available(false) = %d, want %d", got, model.available(false))
	}
	if got := ledger.Available(true); got != model.available(true) {
		t.Fatalf("Available(true) = %d, want %d", got, model.available(true))
	}
	if got, want := ledger.Dirty(), model.dirty(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Dirty() = %v, want %v", got, want)
	}
}

func TestSpecExample(t *testing.T) {
	ledger, err := NewLedger(100, 0, 4, 5)
	if err != nil {
		t.Fatal(err)
	}
	model := newModelLedger(100, 0, 4, 5)

	got, err := ledger.Write(1, 3, false)
	want, wantErr := model.write(1, 3, false)
	if err != wantErr || !reflect.DeepEqual(got, want) {
		t.Fatalf("Write(1, 3) = %v, %v; want %v, %v", got, err, want, wantErr)
	}

	got, err = ledger.Write(2, 4, false)
	want, wantErr = model.write(2, 4, false)
	if err != wantErr || !reflect.DeepEqual(got, want) {
		t.Fatalf("Write(2, 4) = %v, %v; want %v, %v", got, err, want, wantErr)
	}
	assertQueries(t, ledger, model)

	got, err = ledger.Write(1, 2, false)
	want, wantErr = model.write(1, 2, false)
	if err != wantErr || !reflect.DeepEqual(got, want) {
		t.Fatalf("Write(1, 2) = %v, %v; want %v, %v", got, err, want, wantErr)
	}
	assertQueries(t, ledger, model)
}

func TestErrorIdentity(t *testing.T) {
	for target, err := range map[string]error{
		"invalid": ErrInvalidArgument,
		"space":   ErrOutOfSpace,
	} {
		if !errors.Is(err, err) {
			t.Fatalf("%s error does not match itself", target)
		}
	}
}

func TestWriteExactLimitAndOverByOne(t *testing.T) {
	t.Run("non-privileged exact and over by one", func(t *testing.T) {
		exact, _ := NewLedger(6, 1, 4, 100)
		seedExact(t, exact, 3)
		if _, err := exact.Write(1, 1, false); err != nil {
			t.Fatalf("exact write: %v", err)
		}

		over, _ := NewLedger(6, 1, 4, 100)
		seedExact(t, over, 3)
		_, err := over.Write(1, 2, false)
		if !errors.Is(err, ErrOutOfSpace) {
			t.Fatalf("over-by-one write error = %v, want ErrOutOfSpace", err)
		}
	})

	t.Run("privileged exact and non-privileged over by one", func(t *testing.T) {
		exact, _ := NewLedger(7, 1, 4, 100)
		seedExact(t, exact, 3)
		if _, err := exact.Write(1, 2, true); err != nil {
			t.Fatalf("exact privileged write: %v", err)
		}

		over, _ := NewLedger(7, 1, 4, 100)
		seedExact(t, over, 3)
		_, err := over.Write(1, 2, false)
		if !errors.Is(err, ErrOutOfSpace) {
			t.Fatalf("non-privileged write error = %v, want ErrOutOfSpace", err)
		}
	})
}

func seedExact(t *testing.T, ledger *Ledger, blocks int64) {
	t.Helper()
	if _, err := ledger.Write(1, blocks, true); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Flush(1); err != nil {
		t.Fatal(err)
	}
}

func TestIndexBoundaryReservation(t *testing.T) {
	crossing, _ := NewLedger(100, 0, 4, 100)
	if _, err := crossing.Write(1, 4, false); err != nil {
		t.Fatal(err)
	}
	if got := crossing.Reserved(); got != 5 {
		t.Fatalf("four-block reservation = %d, want 5", got)
	}
	if _, err := crossing.Write(1, 1, false); err != nil {
		t.Fatal(err)
	}
	if got := crossing.Reserved(); got != 7 {
		t.Fatalf("crossing reservation = %d, want 7", got)
	}

	within, _ := NewLedger(100, 0, 4, 100)
	if _, err := within.Write(1, 3, false); err != nil {
		t.Fatal(err)
	}
	if _, err := within.Write(1, 1, false); err != nil {
		t.Fatal(err)
	}
	if got := within.Reserved(); got != 5 {
		t.Fatalf("within-boundary reservation = %d, want 5", got)
	}
}

func TestFlushPreservesAvailability(t *testing.T) {
	ledger, _ := NewLedger(10, 0, 4, 100)
	model := newModelLedger(10, 0, 4, 100)

	if _, err := ledger.Write(1, 3, false); err != nil {
		t.Fatal(err)
	}
	if _, err := model.write(1, 3, false); err != nil {
		t.Fatal(err)
	}

	beforeFree := ledger.Free()
	beforeReserved := ledger.Reserved()
	beforeAvailable := ledger.Available(false)
	if err := ledger.Flush(1); err != nil {
		t.Fatal(err)
	}
	model.flush(1)

	if ledger.Free() != beforeFree-beforeReserved {
		t.Fatalf("free after flush = %d, want %d", ledger.Free(), beforeFree-beforeReserved)
	}
	if ledger.Reserved() != 0 {
		t.Fatalf("reserved after flush = %d, want 0", ledger.Reserved())
	}
	if ledger.Available(false) != beforeAvailable {
		t.Fatalf("available after flush = %d, want %d", ledger.Available(false), beforeAvailable)
	}
	assertQueries(t, ledger, model)
}

func TestTruncateOrderAndIndexRelease(t *testing.T) {
	ledger, _ := NewLedger(100, 0, 4, 100)
	model := newModelLedger(100, 0, 4, 100)

	for _, op := range []func() error{
		func() error { _, err := ledger.Write(1, 10, false); return err },
		func() error { return ledger.Flush(1) },
		func() error { _, err := ledger.Write(1, 3, false); return err },
	} {
		if err := op(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := model.write(1, 10, false); err != nil {
		t.Fatal(err)
	}
	model.flush(1)
	if _, err := model.write(1, 3, false); err != nil {
		t.Fatal(err)
	}

	if err := ledger.Truncate(1, 5); err != nil {
		t.Fatal(err)
	}
	if err := model.truncate(1, 5); err != nil {
		t.Fatal(err)
	}
	assertQueries(t, ledger, model)

	if err := ledger.Truncate(1, 8); err != nil {
		t.Fatal(err)
	}
	if err := model.truncate(1, 8); err != nil {
		t.Fatal(err)
	}
	assertQueries(t, ledger, model)
	if err := ledger.Truncate(1, 1); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("truncate empty file error = %v, want ErrFileTooLarge", err)
	}
	if err := ledger.Flush(1); !errors.Is(err, ErrNoDelayedBlocks) {
		t.Fatalf("flush empty file error = %v, want ErrNoDelayedBlocks", err)
	}
}

func TestWatermarkAndDirtyOrder(t *testing.T) {
	t.Run("equal does not flush and over by one flushes oldest", func(t *testing.T) {
		ledger, _ := NewLedger(100, 0, 4, 5)
		if _, err := ledger.Write(1, 3, false); err != nil {
			t.Fatal(err)
		}
		if got, err := ledger.Write(2, 2, false); err != nil || !reflect.DeepEqual(got, []int64{}) {
			t.Fatalf("equal watermark = %v, %v; want [], nil", got, err)
		}
		if got := ledger.Dirty(); !reflect.DeepEqual(got, []int64{1, 2}) {
			t.Fatalf("dirty order = %v, want [1 2]", got)
		}
		got, err := ledger.Write(3, 1, false)
		if err != nil || !reflect.DeepEqual(got, []int64{1}) {
			t.Fatalf("over watermark = %v, %v; want [1], nil", got, err)
		}
		if got := ledger.Dirty(); !reflect.DeepEqual(got, []int64{2, 3}) {
			t.Fatalf("dirty order = %v, want [2 3]", got)
		}
	})

	t.Run("append preserves sequence and flush assigns new sequence", func(t *testing.T) {
		ledger, _ := NewLedger(100, 0, 4, 100)
		for _, fileID := range []int64{1, 2} {
			if _, err := ledger.Write(fileID, 1, false); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := ledger.Write(1, 1, false); err != nil {
			t.Fatal(err)
		}
		if got := ledger.Dirty(); !reflect.DeepEqual(got, []int64{1, 2}) {
			t.Fatalf("dirty after append = %v, want [1 2]", got)
		}
		if err := ledger.Flush(1); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Write(1, 1, false); err != nil {
			t.Fatal(err)
		}
		if got := ledger.Dirty(); !reflect.DeepEqual(got, []int64{2, 1}) {
			t.Fatalf("dirty after re-write = %v, want [2 1]", got)
		}
	})

	t.Run("automatic flush can include current file", func(t *testing.T) {
		ledger, _ := NewLedger(100, 0, 4, 2)
		if _, err := ledger.Write(2, 2, false); err != nil {
			t.Fatal(err)
		}
		got, err := ledger.Write(1, 3, false)
		if err != nil || !reflect.DeepEqual(got, []int64{2, 1}) {
			t.Fatalf("self flush = %v, %v; want [2 1], nil", got, err)
		}
	})

	t.Run("zero watermark flushes every write", func(t *testing.T) {
		ledger, _ := NewLedger(100, 0, 4, 0)
		got, err := ledger.Write(1, 3, false)
		if err != nil || !reflect.DeepEqual(got, []int64{1}) {
			t.Fatalf("zero watermark = %v, %v; want [1], nil", got, err)
		}
		if got := ledger.Dirty(); len(got) != 0 {
			t.Fatalf("dirty after zero-watermark write = %v, want empty", got)
		}
	})

	t.Run("partial delayed truncation preserves sequence", func(t *testing.T) {
		ledger, _ := NewLedger(100, 0, 4, 100)
		if _, err := ledger.Write(1, 3, false); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Write(2, 1, false); err != nil {
			t.Fatal(err)
		}
		if err := ledger.Truncate(1, 1); err != nil {
			t.Fatal(err)
		}
		if got := ledger.Dirty(); !reflect.DeepEqual(got, []int64{1, 2}) {
			t.Fatalf("dirty after partial truncate = %v, want [1 2]", got)
		}
	})
}

func TestRejectedWriteAndRecreateAfterUnlink(t *testing.T) {
	ledger, _ := NewLedger(3, 0, 4, 100)
	if _, err := ledger.Write(1, 3, false); !errors.Is(err, ErrOutOfSpace) {
		t.Fatalf("rejected write = %v, want ErrOutOfSpace", err)
	}
	if err := ledger.Unlink(1); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("unlink rejected file = %v, want ErrFileNotFound", err)
	}
	if got, err := ledger.Write(2, 1, false); err != nil || len(got) != 0 {
		t.Fatalf("write after rejection = %v, %v; want empty sequence", got, err)
	}
	if got := ledger.Dirty(); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("counter advanced or rejected file leaked: dirty = %v, want [2]", got)
	}

	if err := ledger.Unlink(2); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Unlink(2); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("second unlink = %v, want ErrFileNotFound", err)
	}
	if got, err := ledger.Write(2, 1, false); err != nil || len(got) != 0 {
		t.Fatalf("recreate write = %v, %v; want empty sequence", got, err)
	}
	if got := ledger.Dirty(); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("dirty after recreate = %v, want [2]", got)
	}
}

func TestConstructorValidation(t *testing.T) {
	cases := []struct {
		name           string
		total          int64
		systemReserved int64
		entries        int64
		watermark      int64
	}{
		{"zero total", 0, 0, 1, 0},
		{"negative reserved", 1, -1, 1, 0},
		{"reserved above total", 1, 2, 1, 0},
		{"zero entries", 1, 0, 0, 0},
		{"negative watermark", 1, 0, 1, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewLedger(tc.total, tc.systemReserved, tc.entries, tc.watermark)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("NewLedger error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

type randomOperation struct {
	kind       string
	fileID     int64
	blocks     int64
	privileged bool
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	const sequenceCount = 2000
	const operationsPerSequence = 80
	rng := rand.New(rand.NewSource(1101))

	for sequence := 0; sequence < sequenceCount; sequence++ {
		total := int64(1 + rng.Intn(60))
		systemReserved := int64(rng.Intn(int(total + 1)))
		entries := int64(1 + rng.Intn(8))
		watermark := int64(rng.Intn(16))

		ledger, err := NewLedger(total, systemReserved, entries, watermark)
		if err != nil {
			t.Fatalf("sequence %d constructor: %v", sequence, err)
		}
		model := newModelLedger(total, systemReserved, entries, watermark)
		operations := make([]randomOperation, operationsPerSequence)

		for step := 0; step < operationsPerSequence; step++ {
			op := randomOperation{
				kind:       []string{"write", "flush", "truncate", "unlink"}[rng.Intn(4)],
				fileID:     int64(rng.Intn(7)),
				blocks:     int64(1 + rng.Intn(14)),
				privileged: rng.Intn(2) == 1,
			}
			if rng.Intn(10) == 0 {
				op.fileID = -int64(rng.Intn(3))
			}
			if rng.Intn(10) == 0 {
				op.blocks = 0
			}
			operations[step] = op

			var gotFlushed []int64
			var gotErr error
			switch op.kind {
			case "write":
				gotFlushed, gotErr = ledger.Write(op.fileID, op.blocks, op.privileged)
			case "flush":
				gotErr = ledger.Flush(op.fileID)
			case "truncate":
				gotErr = ledger.Truncate(op.fileID, op.blocks)
			case "unlink":
				gotErr = ledger.Unlink(op.fileID)
			}

			var wantFlushed []int64
			var wantErr error
			switch op.kind {
			case "write":
				wantFlushed, wantErr = model.write(op.fileID, op.blocks, op.privileged)
			case "flush":
				wantErr = model.flushFile(op.fileID)
			case "truncate":
				wantErr = model.truncate(op.fileID, op.blocks)
			case "unlink":
				wantErr = model.unlink(op.fileID)
			}

			basis := fmt.Sprintf(
				"F=%d R=%d Avail(non-priv)=%d Avail(priv)=%d dirty=%v",
				model.free(), model.reserved(), model.available(false),
				model.available(true), model.dirty(),
			)
			t.Logf(
				"sequence=%d step=%d input=%+v output=(flushed=%v error=%v) want=(flushed=%v error=%v) basis=%s",
				sequence, step, op, gotFlushed, gotErr, wantFlushed, wantErr, basis,
			)

			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("sequence %d step %d error mismatch: got %v, want %v", sequence, step, gotErr, wantErr)
			}
			if !reflect.DeepEqual(gotFlushed, wantFlushed) {
				t.Fatalf("sequence %d step %d flushed mismatch: got %v, want %v", sequence, step, gotFlushed, wantFlushed)
			}
			assertModelState(t, ledger, model, sequence, step)
			assertQueries(t, ledger, model)
		}
	}
}

func assertModelState(t *testing.T, ledger *Ledger, model *modelLedger, sequence, step int) {
	t.Helper()
	ledger.mu.Lock()
	defer ledger.mu.Unlock()

	if len(ledger.files) != len(model.files) {
		t.Fatalf("sequence %d step %d file count = %d, want %d", sequence, step, len(ledger.files), len(model.files))
	}
	for fileID, want := range model.files {
		got, exists := ledger.files[fileID]
		if !exists {
			t.Fatalf("sequence %d step %d file %d missing", sequence, step, fileID)
		}
		if got.allocated != want.allocated || got.delayed != want.delayed {
			t.Fatalf(
				"sequence %d step %d file %d state = (A=%d D=%d), want (A=%d D=%d)",
				sequence, step, fileID, got.allocated, got.delayed, want.allocated, want.delayed,
			)
		}
		if (got.dirty != nil) != want.dirty {
			t.Fatalf("sequence %d step %d file %d dirty flag mismatch", sequence, step, fileID)
		}
		if got.dirty != nil && got.dirtySeq != want.dirtySeq {
			t.Fatalf("sequence %d step %d file %d dirty seq = %d, want %d", sequence, step, fileID, got.dirtySeq, want.dirtySeq)
		}
	}
	if ledger.reserved < 0 || ledger.reserved > ledger.freeLocked() {
		t.Fatalf("sequence %d step %d invariant broken: F=%d R=%d", sequence, step, ledger.freeLocked(), ledger.reserved)
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	ledger, err := NewLedger(200, 5, 4, 8)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	var readers sync.WaitGroup
	stop := make(chan struct{})
	for worker := 0; worker < 4; worker++ {
		wait.Add(1)
		go func(workerID int64) {
			defer wait.Done()
			for i := int64(0); i < 200; i++ {
				fileID := (workerID*3 + i%7) % 12
				if _, err := ledger.Write(fileID, 1+(i%3), i%2 == 0); err == nil {
					_ = ledger.Flush(fileID)
					_ = ledger.Truncate(fileID, 1)
				}
			}
		}(int64(worker))
	}

	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					free := ledger.Free()
					reserved := ledger.Reserved()
					if free < 0 || reserved < 0 || reserved > free {
						t.Errorf("concurrent invariant broken: free=%d reserved=%d", free, reserved)
						return
					}
					_ = ledger.Available(false)
					_ = ledger.Available(true)
					_ = ledger.Dirty()
				}
			}
		}()
	}

	wait.Wait()
	close(stop)
	readers.Wait()

	free := ledger.Free()
	reserved := ledger.Reserved()
	if free < 0 || reserved < 0 || reserved > free {
		t.Fatalf("final invariant broken: free=%d reserved=%d", free, reserved)
	}
}
