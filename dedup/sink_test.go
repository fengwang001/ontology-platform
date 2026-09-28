package dedup

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func openSink(t *testing.T, maxParts int) *Sink {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.json"), maxParts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// batch builds a batch with one record per partition, offsets starting at
// base and strictly increasing per partition.
func batch(maxParts int, base int64) Batch {
	b := Batch{}
	for p := 0; p < maxParts; p++ {
		for i := 0; i < 3; i++ {
			b.Records = append(b.Records, Record{
				Partition: p,
				Offset:    base + int64(i),
				Key:       fmt.Sprintf("p%d", p),
				Value:     1,
			})
		}
	}
	return b
}

func TestRedeliveryIsIdempotent(t *testing.T) {
	s := openSink(t, 2)
	b := batch(2, 0)

	res, err := s.Apply(b)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if res.Applied != 6 || res.Duplicates != 0 {
		t.Fatalf("first apply = %+v, want 6 applied 0 duplicates", res)
	}

	res, err = s.Apply(b) // redelivered
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if res.Applied != 0 || res.Duplicates != 6 {
		t.Fatalf("redelivery = %+v, want 0 applied 6 duplicates", res)
	}

	snap := s.Snapshot()
	want := map[string]int64{"p0": 3, "p1": 3}
	if !reflect.DeepEqual(snap.Results, want) {
		t.Fatalf("results = %v, want %v", snap.Results, want)
	}
	if snap.Duplicates != 6 {
		t.Fatalf("duplicates = %d, want 6", snap.Duplicates)
	}
	if !reflect.DeepEqual(snap.Watermarks, []int64{2, 2}) {
		t.Fatalf("watermarks = %v, want [2 2]", snap.Watermarks)
	}
}

func TestRestartRebuildsFromPersistedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path, 2)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Apply(batch(2, 0)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Simulate crash + restart: rebuild only from persisted state.
	s2, err := Open(path, 2)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	snap := s2.Snapshot()
	if !reflect.DeepEqual(snap.Results, map[string]int64{"p0": 3, "p1": 3}) {
		t.Fatalf("recovered results = %v", snap.Results)
	}
	if !reflect.DeepEqual(snap.Watermarks, []int64{2, 2}) {
		t.Fatalf("recovered watermarks = %v", snap.Watermarks)
	}

	// A batch redelivered after restart is still deduplicated.
	res, err := s2.Apply(batch(2, 0))
	if err != nil {
		t.Fatalf("redelivery after restart: %v", err)
	}
	if res.Applied != 0 || res.Duplicates != 6 {
		t.Fatalf("redelivery after restart = %+v", res)
	}

	// New records above the watermark still apply after restart.
	res, err = s2.Apply(batch(2, 3))
	if err != nil {
		t.Fatalf("apply after restart: %v", err)
	}
	if res.Applied != 6 {
		t.Fatalf("apply after restart = %+v", res)
	}
	if got := s2.Snapshot().Results["p0"]; got != 6 {
		t.Fatalf("results[p0] = %d, want 6", got)
	}
}

func TestInBatchDuplicateOffsetRejected(t *testing.T) {
	s := openSink(t, 1)
	b := Batch{Records: []Record{
		{Partition: 0, Offset: 5, Key: "a", Value: 1},
		{Partition: 0, Offset: 5, Key: "a", Value: 1}, // duplicate offset in same batch
	}}
	if _, err := s.Apply(b); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("err = %v, want ErrOutOfOrder", err)
	}
	// Descending offsets within a batch are also rejected.
	b.Records[1].Offset = 4
	if _, err := s.Apply(b); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("err = %v, want ErrOutOfOrder", err)
	}
	snap := s.Snapshot()
	if len(snap.Results) != 0 || snap.Duplicates != 0 || snap.Watermarks[0] != -1 {
		t.Fatalf("rejected batch changed state: %+v", snap)
	}
}

func TestInvalidInputsRejected(t *testing.T) {
	s := openSink(t, 2)
	cases := []struct {
		name string
		rec  Record
		want error
	}{
		{"negative partition", Record{Partition: -1, Offset: 0, Key: "a"}, ErrNegativePartition},
		{"partition limit", Record{Partition: 2, Offset: 0, Key: "a"}, ErrPartitionLimit},
		{"negative offset", Record{Partition: 0, Offset: -1, Key: "a"}, ErrNegativeOffset},
		{"empty key", Record{Partition: 0, Offset: 0, Key: ""}, ErrEmptyKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Apply(Batch{Records: []Record{tc.rec}})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	snap := s.Snapshot()
	if len(snap.Results) != 0 || snap.Duplicates != 0 {
		t.Fatalf("rejected batches changed state: %+v", snap)
	}
	for p, wm := range snap.Watermarks {
		if wm != -1 {
			t.Fatalf("watermark[%d] = %d, want -1", p, wm)
		}
	}
}

func TestOpenRejectsInvalidPartitionCount(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "state.json"), 0)
	if !errors.Is(err, ErrInvalidPartitionCount) {
		t.Fatalf("err = %v, want ErrInvalidPartitionCount", err)
	}
}

func TestPartialOverlap(t *testing.T) {
	s := openSink(t, 1)
	if _, err := s.Apply(Batch{Records: []Record{
		{Partition: 0, Offset: 0, Key: "a", Value: 1},
		{Partition: 0, Offset: 1, Key: "a", Value: 1},
	}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Offsets 0,1 are duplicates; 2,3 are new.
	res, err := s.Apply(Batch{Records: []Record{
		{Partition: 0, Offset: 0, Key: "a", Value: 10},
		{Partition: 0, Offset: 1, Key: "a", Value: 10},
		{Partition: 0, Offset: 2, Key: "a", Value: 10},
		{Partition: 0, Offset: 3, Key: "a", Value: 10},
	}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Applied != 2 || res.Duplicates != 2 {
		t.Fatalf("res = %+v, want 2 applied 2 duplicates", res)
	}
	snap := s.Snapshot()
	if snap.Results["a"] != 22 || snap.Watermarks[0] != 3 || snap.Duplicates != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestConcurrentApplyEqualsSingleWrite(t *testing.T) {
	s := openSink(t, 4)
	b := batch(4, 0)
	const writers = 16

	var wg sync.WaitGroup
	errs := make([]error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			_, errs[w] = s.Apply(b)
		}(w)
	}
	wg.Wait()
	for w, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", w, err)
		}
	}

	snap := s.Snapshot()
	want := map[string]int64{"p0": 3, "p1": 3, "p2": 3, "p3": 3}
	if !reflect.DeepEqual(snap.Results, want) {
		t.Fatalf("results = %v, want %v (as if written once)", snap.Results, want)
	}
	// Every extra delivery beyond the first is fully duplicate.
	if want := int64((writers - 1) * len(b.Records)); snap.Duplicates != want {
		t.Fatalf("duplicates = %d, want %d", snap.Duplicates, want)
	}
}

func TestDeterministicReplay(t *testing.T) {
	run := func() Snapshot {
		s := openSink(t, 3)
		for base := int64(0); base < 9; base += 3 {
			if _, err := s.Apply(batch(3, base)); err != nil {
				t.Fatalf("apply: %v", err)
			}
		}
		if _, err := s.Apply(batch(3, 0)); err != nil { // redelivery
			t.Fatalf("redelivery: %v", err)
		}
		return s.Snapshot()
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic: %+v vs %+v", first, second)
	}
}
