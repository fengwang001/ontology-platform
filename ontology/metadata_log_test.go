package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustNewLog(t *testing.T, capacity int) *MetadataLog {
	t.Helper()
	log, err := NewMetadataLog(capacity)
	if err != nil {
		t.Fatalf("NewMetadataLog(%d): %v", capacity, err)
	}
	return log
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func requireBlock(t *testing.T, image map[int]Block, block, ver int, payload string) {
	t.Helper()
	value, ok := image[block]
	if !ok {
		t.Fatalf("block %d missing from image", block)
	}
	if value.Ver != ver || !bytes.Equal(value.Payload, []byte(payload)) {
		t.Fatalf("block %d = (ver=%d, payload=%q), want (ver=%d, payload=%q)",
			block, value.Ver, value.Payload, ver, payload)
	}
}

func requireCounts(t *testing.T, got RecoveryResult, applied, skipped, stale, checkpointed, ignored int) {
	t.Helper()
	if got.Applied != applied || got.Skipped != skipped || got.Stale != stale ||
		got.Checkpointed != checkpointed || got.Ignored != ignored {
		t.Fatalf("counts = applied=%d skipped=%d stale=%d checkpointed=%d ignored=%d, want applied=%d skipped=%d stale=%d checkpointed=%d ignored=%d",
			got.Applied, got.Skipped, got.Stale, got.Checkpointed, got.Ignored,
			applied, skipped, stale, checkpointed, ignored)
	}
}

func TestWriteThenRevokeWithinTransaction(t *testing.T) {
	log := mustNewLog(t, 4)
	must(t, log.Write(1, []byte("a")))
	must(t, log.Revoke(1))
	must(t, log.Commit())

	got, err := log.Recover(nil, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, got, 0, 1, 0, 0, 0)
	if _, exists := got.Image[1]; exists {
		t.Fatalf("revoked write must not create block: %#v", got.Image)
	}
}

func TestRevokeThenWriteWithinTransaction(t *testing.T) {
	log := mustNewLog(t, 4)
	must(t, log.Write(1, []byte("old")))
	must(t, log.Commit())
	must(t, log.Revoke(1))
	must(t, log.Write(1, []byte("new")))
	must(t, log.Commit())

	got, err := log.Recover(nil, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, got, 2, 0, 0, 0, 0)
	requireBlock(t, got.Image, 1, 2, "new")
}

func TestRevokeChainAndCheckpointBoundary(t *testing.T) {
	log := mustNewLog(t, 6)
	must(t, log.Write(1, []byte("t1")))
	must(t, log.Commit())
	must(t, log.Write(1, []byte("t2")))
	must(t, log.Commit())
	must(t, log.Write(1, []byte("t3")))
	must(t, log.Revoke(1))
	must(t, log.Commit())
	must(t, log.Write(1, []byte("t4")))
	must(t, log.Commit())

	got, err := log.Recover(nil, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, got, 1, 3, 0, 0, 0)
	requireBlock(t, got.Image, 1, 4, "t4")

	must(t, log.Checkpoint(3))
	got, err = log.Recover(nil, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, got, 1, 0, 0, 3, 0)
	requireBlock(t, got.Image, 1, 4, "t4")
}

func TestRevocationAndDiskVersionBoundaries(t *testing.T) {
	log := mustNewLog(t, 4)
	must(t, log.Write(5, []byte("revoked")))
	must(t, log.Commit())
	must(t, log.Revoke(1))
	must(t, log.Commit())
	must(t, log.Write(1, []byte("equal")))
	must(t, log.Commit())

	disk := map[int]Block{
		2: {Ver: 2, Payload: []byte("stale")},
		3: {Ver: 2, Payload: []byte("older")},
		4: {Ver: 4, Payload: []byte("disk-stale")},
	}
	must(t, log.Revoke(2))
	must(t, log.Revoke(5))
	must(t, log.Revoke(1))
	must(t, log.Write(4, []byte("stale-equal")))
	must(t, log.Commit())
	must(t, log.Write(2, []byte("after-revoke")))
	must(t, log.Write(3, []byte("apply")))
	must(t, log.Commit())

	got, err := log.Recover(disk, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, got, 2, 2, 1, 0, 0)
	requireBlock(t, got.Image, 2, 5, "after-revoke")
	requireBlock(t, got.Image, 3, 5, "apply")
	requireBlock(t, got.Image, 4, 4, "disk-stale")
}

func TestTwoWritesSameBlockSameTransactionBothApply(t *testing.T) {
	log := mustNewLog(t, 4)
	must(t, log.Write(1, []byte("first")))
	must(t, log.Write(1, []byte("second")))
	must(t, log.Commit())

	got, err := log.Recover(nil, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, got, 2, 0, 0, 0, 0)
	requireBlock(t, got.Image, 1, 1, "second")
}

func TestCheckpointPrecedesRevocation(t *testing.T) {
	log := mustNewLog(t, 4)
	must(t, log.Write(1, []byte("a")))
	must(t, log.Commit())
	must(t, log.Revoke(1))
	must(t, log.Checkpoint(1))
	must(t, log.Write(2, []byte("b")))
	must(t, log.Commit())

	got, err := log.Recover(nil, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, got, 1, 0, 0, 1, 0)
	requireBlock(t, got.Image, 2, 2, "b")
}

func TestEmptyPayloadIsWrittenAndDistinctFromAbsentBlock(t *testing.T) {
	log := mustNewLog(t, 2)
	must(t, log.Write(1, nil))
	must(t, log.Write(2, []byte{}))
	must(t, log.Commit())

	got, err := log.Recover(nil, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range []int{1, 2} {
		value, exists := got.Image[block]
		if !exists {
			t.Fatalf("block %d should exist", block)
		}
		if value.Ver != 1 || value.Payload == nil || len(value.Payload) != 0 {
			t.Fatalf("block %d = %#v, want version 1 non-nil empty payload", block, value)
		}
	}
	if _, exists := got.Image[3]; exists {
		t.Fatal("block 3 was never written")
	}
}

func TestSpecExample(t *testing.T) {
	log := mustNewLog(t, 4)
	must(t, log.Write(5, []byte("a")))
	must(t, log.Commit())
	must(t, log.Revoke(5))
	must(t, log.Commit())
	must(t, log.Write(5, []byte("c")))
	must(t, log.Write(7, []byte("d")))
	must(t, log.Commit())

	disk := map[int]Block{7: {Ver: 3, Payload: []byte("old")}}
	got, err := log.Recover(disk, 7)
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, got, 1, 1, 1, 0, 0)
	requireBlock(t, got.Image, 5, 3, "c")
	requireBlock(t, got.Image, 7, 3, "old")

	must(t, log.Checkpoint(1))
	got, err = log.Recover(disk, 8)
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, got, 1, 0, 1, 1, 0)
	requireBlock(t, got.Image, 5, 3, "c")
}

func TestSameOperationSequenceReplaysIdentically(t *testing.T) {
	build := func() *MetadataLog {
		log := mustNewLog(t, 3)
		must(t, log.Write(1, []byte("first")))
		must(t, log.Write(1, nil))
		must(t, log.Revoke(2))
		must(t, log.Commit())
		must(t, log.Checkpoint(0))
		must(t, log.Revoke(1))
		must(t, log.Checkpoint(1))
		must(t, log.Write(1, []byte("later")))
		must(t, log.Commit())
		return log
	}

	first := build()
	second := build()
	if first.Records() != second.Records() {
		t.Fatalf("record counts differ: %d vs %d", first.Records(), second.Records())
	}
	disk := map[int]Block{3: {Ver: 2, Payload: []byte("disk")}}
	for n := 0; n <= first.Records(); n++ {
		left, err := first.Recover(disk, n)
		if err != nil {
			t.Fatalf("first n=%d: %v", n, err)
		}
		right, err := second.Recover(disk, n)
		if err != nil {
			t.Fatalf("second n=%d: %v", n, err)
		}
		if !reflect.DeepEqual(left, right) {
			t.Fatalf("n=%d recovery differs: %#v vs %#v", n, left, right)
		}
	}
}

func mustRecoverAt(t *testing.T, log *MetadataLog, disk map[int]Block, n int) RecoveryResult {
	t.Helper()
	got, err := log.Recover(disk, n)
	if err != nil {
		t.Fatalf("Recover(n=%d): %v", n, err)
	}
	return got
}

func TestEveryPrefixAndUncommittedRevoke(t *testing.T) {
	log := mustNewLog(t, 3)
	got := mustRecoverAt(t, log, nil, 0)
	requireCounts(t, got, 0, 0, 0, 0, 0)

	must(t, log.Write(1, []byte("x")))
	got = mustRecoverAt(t, log, nil, 1)
	requireCounts(t, got, 0, 0, 0, 0, 1)
	must(t, log.Commit())
	got = mustRecoverAt(t, log, nil, 2)
	requireCounts(t, got, 1, 0, 0, 0, 0)
	must(t, log.Revoke(1))
	got = mustRecoverAt(t, log, nil, 3)
	requireCounts(t, got, 1, 0, 0, 0, 1)
	must(t, log.Write(2, []byte("y")))
	got = mustRecoverAt(t, log, nil, 4)
	requireCounts(t, got, 1, 0, 0, 0, 2)
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	log := mustNewLog(t, 1)
	before := log.Records()
	if err := log.Write(-1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative block error = %v", err)
	}
	if err := log.Write(0, make([]byte, 1025)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("oversize payload error = %v", err)
	}
	if err := log.Commit(); !errors.Is(err, ErrEmptyTransaction) {
		t.Fatalf("empty commit error = %v", err)
	}
	if log.Records() != before {
		t.Fatal("invalid operation changed the log")
	}

	must(t, log.Write(0, []byte("full")))
	if err := log.Write(1, nil); !errors.Is(err, ErrTransactionFull) {
		t.Fatalf("full write error = %v", err)
	}
	if err := log.Revoke(1); !errors.Is(err, ErrTransactionFull) {
		t.Fatalf("full revoke error = %v", err)
	}
	if err := log.Checkpoint(1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("future checkpoint error = %v", err)
	}
	if log.Records() != before+1 {
		t.Fatal("rejected full-transaction operation changed the log")
	}

	must(t, log.Commit())
	must(t, log.Checkpoint(1))
	if err := log.Checkpoint(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("backward checkpoint error = %v", err)
	}
	if _, err := log.Recover(nil, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative prefix error = %v", err)
	}
	if _, err := log.Recover(nil, log.Records()+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("oversize prefix error = %v", err)
	}
	if log.Records() != before+3 {
		t.Fatal("rejected operation changed the log")
	}
}

func TestNewMetadataLogRejectsNonPositiveCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1, -1024} {
		log, err := NewMetadataLog(capacity)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("NewMetadataLog(%d) error = %v, want ErrInvalidArgument", capacity, err)
		}
		if log != nil {
			t.Fatalf("NewMetadataLog(%d) log = %v, want nil", capacity, log)
		}
	}
}

func TestCheckpointExactlyOneAndOneGreater(t *testing.T) {
	log := mustNewLog(t, 4)
	must(t, log.Write(1, []byte("t1")))
	must(t, log.Commit())
	must(t, log.Write(2, []byte("t2")))
	must(t, log.Commit())
	must(t, log.Checkpoint(1))

	got, err := log.Recover(nil, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, got, 1, 0, 0, 1, 0)
	requireBlock(t, got.Image, 2, 2, "t2")
}

func TestPayloadCopiesAndRecoverDoesNotMutate(t *testing.T) {
	log := mustNewLog(t, 2)
	payload := []byte("original")
	must(t, log.Write(1, payload))
	payload[0] = 'X'
	must(t, log.Commit())

	disk := map[int]Block{2: {Ver: 1, Payload: []byte("disk")}}
	got, err := log.Recover(disk, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	requireBlock(t, got.Image, 1, 1, "original")
	got.Image[2].Payload[0] = 'Z'

	again, err := log.Recover(disk, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	if string(again.Image[2].Payload) != "disk" {
		t.Fatalf("recovery reused an image payload: %q", again.Image[2].Payload)
	}
	disk[2].Payload[0] = 'D'
	again, err = log.Recover(disk, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	if string(again.Image[2].Payload) != "Disk" {
		t.Fatalf("disk mutation was not visible: %q", again.Image[2].Payload)
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	log := mustNewLog(t, 100)
	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < 200; i++ {
			if err := log.Write(i%8, []byte(fmt.Sprintf("%d", i))); err != nil {
				t.Errorf("write: %v", err)
				return
			}
			if err := log.Commit(); err != nil {
				t.Errorf("commit: %v", err)
				return
			}
		}
	}()

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				n := log.Records()
				if _, err := log.Recover(nil, n); err != nil {
					t.Errorf("worker %d recover n=%d: %v", worker, n, err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	wantRecords := 400
	if log.Records() != wantRecords {
		t.Fatalf("records = %d, want %d", log.Records(), wantRecords)
	}
	got, err := log.Recover(nil, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	committedWrites := 200
	if got.Applied+got.Skipped+got.Stale+got.Checkpointed != committedWrites {
		t.Fatalf("classified writes = %d, want %d", got.Applied+got.Skipped+got.Stale+got.Checkpointed, committedWrites)
	}
}

func TestConcurrentMixedOperationsAreValid(t *testing.T) {
	log := mustNewLog(t, 10)
	var wg sync.WaitGroup
	committed := make(chan int, 50)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if err := log.Write(i%4, []byte(fmt.Sprintf("%d", i))); err != nil {
				t.Errorf("write: %v", err)
				return
			}
			if err := log.Commit(); err != nil {
				t.Errorf("commit: %v", err)
				return
			}
			committed <- i + 1
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		lastUpto := 0
		for i := 0; i < 50; i++ {
			tid := <-committed
			upto := lastUpto
			if tid%2 == 0 {
				upto = tid
			}
			if err := log.Checkpoint(upto); err != nil {
				t.Errorf("checkpoint %d after %d: %v", upto, lastUpto, err)
				return
			}
			lastUpto = upto
		}
	}()

	wg.Wait()
	got, err := log.Recover(nil, log.Records())
	if err != nil {
		t.Fatal(err)
	}
	if got.Applied+got.Skipped+got.Stale+got.Checkpointed != 50 {
		t.Fatalf("classified writes = %d, want 50", got.Applied+got.Skipped+got.Stale+got.Checkpointed)
	}
}
