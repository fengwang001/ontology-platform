package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestLateBoundaryAndOlderTimestampAccepted(t *testing.T) {
	b, err := NewBuffer(10, 4, 2, EmitEarly)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := b.Update([]byte("a"), 0, 1); err != nil {
		t.Fatal(err)
	}
	if out, err := b.Tick(13); err != nil || len(out) != 0 {
		t.Fatalf("Tick(13) = %v, %v", out, err)
	}
	result, err := b.Update([]byte("a"), 0, 1)
	if err != nil || result.Status != StatusAccepted || len(result.Emitted) != 0 {
		t.Fatalf("Update at ST=14, close-ST gap=1 = %+v, %v", result, err)
	}

	if out, err := b.Tick(14); err != nil || len(out) != 1 {
		t.Fatalf("closing Tick(14) = %v, %v", out, err)
	}
	result, err = b.Update([]byte("b"), 0, 2)
	if err != nil || result.Status != StatusDiscarded || b.Late() != 1 || b.StreamTime() != 14 {
		t.Fatalf("Update at close time = %+v, %v, late=%d ST=%d", result, err, b.Late(), b.StreamTime())
	}
}

func TestExampleEmitEarly(t *testing.T) {
	b, _ := NewBuffer(10, 5, 2, EmitEarly)

	result, err := b.Update([]byte("a"), 3, 1)
	if err != nil || len(result.Emitted) != 0 || b.StreamTime() != 3 {
		t.Fatalf("first update = %+v, %v", result, err)
	}
	result, err = b.Update([]byte("b"), 12, 2)
	if err != nil || len(result.Emitted) != 0 || b.StreamTime() != 12 {
		t.Fatalf("second update = %+v, %v", result, err)
	}

	result, err = b.Update([]byte("a"), 14, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []Emitted{{Key: []byte("a"), WS: 0, End: 10, Value: 1, LastTS: 3, Kind: Early}}
	if !emittedEqual(result.Emitted, want) || b.StreamTime() != 14 {
		t.Fatalf("early emit = %+v, ST=%d; want %+v", result.Emitted, b.StreamTime(), want)
	}

	result, err = b.Update([]byte("a"), 2, 9)
	if err != nil {
		t.Fatal(err)
	}
	want = []Emitted{{Key: []byte("a"), WS: 10, End: 20, Value: 3, LastTS: 14, Kind: Early}}
	if !emittedEqual(result.Emitted, want) {
		t.Fatalf("equal-end key ordering = %+v; want %+v", result.Emitted, want)
	}

	result, err = b.Update([]byte("c"), 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	want = []Emitted{
		{Key: []byte("a"), WS: 0, End: 10, Value: 9, LastTS: 2, Kind: Final},
		{Key: []byte("b"), WS: 10, End: 20, Value: 2, LastTS: 12, Kind: Final},
	}
	if !emittedEqual(result.Emitted, want) {
		t.Fatalf("final emissions = %+v; want %+v", result.Emitted, want)
	}
}

func TestExampleShutdown(t *testing.T) {
	b, _ := NewBuffer(10, 5, 1, Shutdown)

	if _, err := b.Update([]byte("a"), 3, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Update([]byte("b"), 12, 2); !errors.Is(err, ErrBufferFull) || b.StreamTime() != 3 {
		t.Fatalf("overflow error=%v, ST=%d", err, b.StreamTime())
	}

	out, err := b.Update([]byte("c"), 16, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []Emitted{{Key: []byte("a"), WS: 0, End: 10, Value: 1, LastTS: 3, Kind: Final}}
	if !emittedEqual(out.Emitted, want) || b.StreamTime() != 16 {
		t.Fatalf("self-advancing close = %+v, ST=%d; want %+v", out.Emitted, b.StreamTime(), want)
	}

	out, err = b.Update([]byte("c"), 17, 4)
	if err != nil || len(out.Emitted) != 0 {
		t.Fatalf("overwrite = %+v, %v", out, err)
	}

	out, err = b.Update([]byte("a"), 25, 5)
	if err != nil {
		t.Fatal(err)
	}
	want = []Emitted{{Key: []byte("c"), WS: 10, End: 20, Value: 4, LastTS: 17, Kind: Final}}
	if !emittedEqual(out.Emitted, want) {
		t.Fatalf("boundary final = %+v; want %+v", out.Emitted, want)
	}
}

func TestOverwriteDoesNotConsumeCapacityAndCanLowerLastTS(t *testing.T) {
	b, _ := NewBuffer(10, 5, 1, Shutdown)
	if _, err := b.Update([]byte("a"), 3, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Update([]byte("a"), 2, 9); err != nil {
		t.Fatalf("older overwrite: %v", err)
	}
	entries := b.Buffered()
	if len(entries) != 1 || entries[0].Value != 9 || entries[0].LastTS != 2 {
		t.Fatalf("buffered = %+v", entries)
	}

	out, err := b.Tick(15)
	if err != nil {
		t.Fatal(err)
	}
	want := []Emitted{{Key: []byte("a"), WS: 0, End: 10, Value: 9, LastTS: 2, Kind: Final}}
	if !emittedEqual(out, want) {
		t.Fatalf("tick = %+v; want %+v", out, want)
	}
}

func TestTickBoundariesRollbackAndNoOp(t *testing.T) {
	b, _ := NewBuffer(10, 5, 1, EmitEarly)
	if _, err := b.Update([]byte("a"), 3, 1); err != nil {
		t.Fatal(err)
	}

	if out, err := b.Tick(14); err != nil || len(out) != 0 || b.StreamTime() != 14 {
		t.Fatalf("Tick(close-1) = %v, %v, ST=%d", out, err, b.StreamTime())
	}
	if out, err := b.Tick(13); !errors.Is(err, ErrStreamTimeRollback) || len(out) != 0 {
		t.Fatalf("rollback = %v, %v", out, err)
	}
	if out, err := b.Tick(14); err != nil || len(out) != 0 {
		t.Fatalf("equal-time Tick = %v, %v", out, err)
	}
	out, err := b.Tick(15)
	if err != nil {
		t.Fatal(err)
	}
	want := []Emitted{{Key: []byte("a"), WS: 0, End: 10, Value: 1, LastTS: 3, Kind: Final}}
	if !emittedEqual(out, want) {
		t.Fatalf("Tick(close) = %+v; want %+v", out, want)
	}
}

func TestPeekIncrementIndependentOfBufferSize(t *testing.T) {
	delta := func(n int) int64 {
		b, _ := NewBuffer(10, 5, 10_001, Shutdown)
		for i := range n {
			result, err := b.Update([]byte(fmt.Sprintf("k%05d", i)), 0, 0)
			if err != nil || len(result.Emitted) != 0 {
				t.Fatalf("fill %d: %+v, %v", i, result, err)
			}
		}
		before := b.peeks
		result, err := b.Update([]byte("probe"), 1, 0)
		if err != nil || len(result.Emitted) != 0 {
			t.Fatalf("probe: %+v, %v", result, err)
		}
		return b.peeks - before
	}

	if got10, got10000 := delta(10), delta(10_000); got10 != 1 || got10 != got10000 {
		t.Fatalf("peek deltas = %d and %d", got10, got10000)
	}
}

func TestPeekIncrementBudget(t *testing.T) {
	b, _ := NewBuffer(10, 5, 2, EmitEarly)
	ops := []struct {
		key   string
		ts    int64
		value int64
	}{
		{"a", 3, 1},
		{"b", 12, 2},
		{"a", 14, 3},
		{"a", 2, 9},
		{"c", 30, 1},
	}

	for _, op := range ops {
		before := b.peeks
		result, err := b.Update([]byte(op.key), op.ts, op.value)
		if err != nil {
			t.Fatal(err)
		}
		delta := b.peeks - before
		if delta > int64(len(result.Emitted))+2 {
			t.Fatalf("Update(%v) peek delta=%d emitted=%d", op, delta, len(result.Emitted))
		}
	}

	for _, tick := range []int64{31, 34, 35} {
		before := b.peeks
		out, err := b.Tick(tick)
		if err != nil {
			t.Fatal(err)
		}
		delta := b.peeks - before
		if delta > int64(len(out))+2 {
			t.Fatalf("Tick(%d) peek delta=%d emitted=%d", tick, delta, len(out))
		}
	}
}

func TestFinalKindIsEmittedBeforeEarlyKind(t *testing.T) {
	closed := newTreeNode(&bufferEntry{key: []byte("a"), ws: 0, end: 10})
	early := newTreeNode(&bufferEntry{key: []byte("b"), ws: 10, end: 20})
	out := appendEntriesInOrder(closed, nil, Final)
	out = appendEntriesInOrder(early, out, Early)
	if len(out) != 2 || out[0].Kind != Final || out[1].Kind != Early {
		t.Fatalf("order = %+v", out)
	}
}

func TestFinalThenSameWindowCanReenterAndFinalAgain(t *testing.T) {
	b, _ := NewBuffer(10, 0, 1, Shutdown)

	out, err := b.Update([]byte("a"), 3, 1)
	if err != nil || len(out.Emitted) != 0 {
		t.Fatalf("first insert = %+v, %v", out, err)
	}
	out, err = b.Update([]byte("a"), 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []Emitted{{Key: []byte("a"), WS: 0, End: 10, Value: 1, LastTS: 3, Kind: Final}}
	if !emittedEqual(out.Emitted, want) {
		t.Fatalf("self-closed old entry = %+v; want %+v", out.Emitted, want)
	}
	if entries := b.Buffered(); len(entries) != 1 || entries[0].WS != 10 || entries[0].Value != 2 || entries[0].LastTS != 10 {
		t.Fatalf("reentered entry = %+v", entries)
	}

	out, err = b.Update([]byte("b"), 20, 3)
	if err != nil {
		t.Fatal(err)
	}
	want = []Emitted{{Key: []byte("a"), WS: 10, End: 20, Value: 2, LastTS: 10, Kind: Final}}
	if !emittedEqual(out.Emitted, want) {
		t.Fatalf("reentered final = %+v; want %+v", out.Emitted, want)
	}
}

func TestDeterministicReplay(t *testing.T) {
	run := func() ([]Emitted, []BufferedEntry, int64, int64) {
		b, _ := NewBuffer(7, 3, 2, EmitEarly)
		all := make([]Emitted, 0)
		operations := []struct {
			key   string
			ts    int64
			value int64
			tick  bool
		}{
			{key: "a", ts: 1, value: 1},
			{key: "b", ts: 9, value: 2},
			{key: "a", ts: 15, value: 3},
			{tick: true, ts: 25},
			{key: "c", ts: 22, value: 4},
			{key: "b", ts: 20, value: 5},
		}
		for _, operation := range operations {
			if operation.tick {
				out, err := b.Tick(operation.ts)
				if err != nil {
					t.Fatal(err)
				}
				all = append(all, out...)
			} else {
				out, err := b.Update([]byte(operation.key), operation.ts, operation.value)
				if err != nil {
					t.Fatal(err)
				}
				all = append(all, out.Emitted...)
			}
		}
		return all, b.Buffered(), b.StreamTime(), b.Late()
	}

	firstEmitted, firstBuffered, firstST, firstLate := run()
	secondEmitted, secondBuffered, secondST, secondLate := run()
	if !emittedEqual(firstEmitted, secondEmitted) || firstST != secondST || firstLate != secondLate {
		t.Fatalf("replay emitted/ST/late mismatch")
	}
	if len(firstBuffered) != len(secondBuffered) {
		t.Fatalf("replay buffer length mismatch")
	}
	for i := range firstBuffered {
		if !bytes.Equal(firstBuffered[i].Key, secondBuffered[i].Key) ||
			firstBuffered[i].WS != secondBuffered[i].WS ||
			firstBuffered[i].End != secondBuffered[i].End ||
			firstBuffered[i].Value != secondBuffered[i].Value ||
			firstBuffered[i].LastTS != secondBuffered[i].LastTS {
			t.Fatalf("replay buffer[%d] mismatch: %+v vs %+v", i, firstBuffered[i], secondBuffered[i])
		}
	}
}

func TestInvalidArguments(t *testing.T) {
	if _, err := NewBuffer(0, 0, 1, EmitEarly); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("width error = %v", err)
	}
	if _, err := NewBuffer(1, 1_000_000_001, 1, EmitEarly); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("grace error = %v", err)
	}
	if _, err := NewBuffer(1, 0, 0, EmitEarly); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("capacity error = %v", err)
	}
	if _, err := NewBuffer(1, 0, 1, FullPolicy(9)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("policy error = %v", err)
	}

	b, _ := NewBuffer(10, 5, 1, Shutdown)
	if _, err := b.Update(nil, 0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty key error = %v", err)
	}
	if _, err := b.Update([]byte("a"), -1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ts error = %v", err)
	}
	if _, err := b.Update([]byte("a"), 0, 1_000_000_000_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("value error = %v", err)
	}
	if _, err := b.Tick(1_000_000_000_000_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("tick error = %v", err)
	}
}

func TestConcurrentUpdatesAreSerializable(t *testing.T) {
	b, _ := NewBuffer(7, 2, 12, EmitEarly)
	var wait sync.WaitGroup
	var mu sync.Mutex
	allEmitted := make([]Emitted, 0, 100)

	for i := range 100 {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			result, err := b.Update([]byte(fmt.Sprintf("key-%02d", i)), int64(i), int64(i))
			if err != nil {
				t.Errorf("update %d: %v", i, err)
				return
			}
			mu.Lock()
			allEmitted = append(allEmitted, result.Emitted...)
			mu.Unlock()
		}(i)
	}
	wait.Wait()

	if got := len(b.Buffered()); got > 12 {
		t.Fatalf("buffered size = %d", got)
	}
	if got := b.StreamTime(); got != 99 {
		t.Fatalf("ST = %d", got)
	}
	if got := len(allEmitted) + len(b.Buffered()) + int(b.Late()); got != 100 {
		t.Fatalf("accounting emitted=%d buffered=%d late=%d", len(allEmitted), len(b.Buffered()), b.Late())
	}
}

func emittedEqual(got, want []Emitted) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !bytes.Equal(got[i].Key, want[i].Key) ||
			got[i].WS != want[i].WS || got[i].End != want[i].End ||
			got[i].Value != want[i].Value || got[i].LastTS != want[i].LastTS ||
			got[i].Kind != want[i].Kind {
			return false
		}
	}
	return true
}
