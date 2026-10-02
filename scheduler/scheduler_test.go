package scheduler

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
)

type opLog struct {
	name string
	args string
	got  string
	want string
	ok   bool
}

func logf(b *strings.Builder, entry opLog) {
	fmt.Fprintf(b, "%s(%s) => %s; want %s; %v\n", entry.name, entry.args, entry.got, entry.want, entry.ok)
}

func TestSchedulerExample(t *testing.T) {
	s := NewScheduler()
	var logs strings.Builder

	add, err := s.Add(10, 20)
	logf(&logs, opLog{"Add", "10,20", fmt.Sprintf("%v,%v", add, err), "id=1,nil", err == nil && add.ID == 1})
	if err != nil || add.ID != 1 {
		t.Fatalf("\n%s", logs.String())
	}
	if w := s.Watermark(); w != 10 {
		t.Fatalf("W=%d, want 10\n%s", w, logs.String())
	}

	pr, err := s.Process(1, 3)
	if err != nil || pr != (ProcessResult{3, 10, 13, 1}) {
		t.Fatalf("process=%+v err=%v", pr, err)
	}
	if w := s.Watermark(); w != 10 {
		t.Fatalf("W=%d, want 10", w)
	}

	add, err = s.Add(11, 15)
	if err != nil || add.ID != 2 {
		t.Fatalf("add=%+v err=%v", add, err)
	}
	if err := s.Ack(1); err != nil {
		t.Fatal(err)
	}
	if w := s.Watermark(); w != 11 {
		t.Fatalf("W=%d, want 11", w)
	}

	pr, err = s.Process(2, 4)
	if err != nil || pr != (ProcessResult{4, 11, 15, 2}) {
		t.Fatalf("process=%+v err=%v", pr, err)
	}
	sp, err := s.Split(1, 1, 2)
	if err != nil || sp.ID != 3 {
		t.Fatalf("split=%+v err=%v", sp, err)
	}
	pr, err = s.Process(1, 10)
	if err != nil || pr != (ProcessResult{4, 13, 17, 3}) {
		t.Fatalf("process=%+v err=%v", pr, err)
	}
	if err := s.Ack(3); err != nil {
		t.Fatal(err)
	}
	_, err = s.Split(2, 1, 2)
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("split exhausted err=%v", err)
	}
	if err := s.Ack(2); err != nil {
		t.Fatal(err)
	}
	if w := s.Watermark(); w != 17 {
		t.Fatalf("W=%d, want 17", w)
	}
	_, err = s.Add(15, 16)
	if !errors.Is(err, ErrBelowWatermark) {
		t.Fatalf("low add err=%v", err)
	}
	add, err = s.Add(17, 18)
	if err != nil || add.ID != 4 {
		t.Fatalf("add=%+v err=%v", add, err)
	}
	pr, err = s.Process(3, 3)
	if err != nil || pr != (ProcessResult{3, 17, 20, 4}) {
		t.Fatalf("process=%+v err=%v", pr, err)
	}
	if err := s.Ack(4); err != nil {
		t.Fatal(err)
	}
	pr, err = s.Process(4, 1)
	if err != nil || pr != (ProcessResult{1, 17, 18, 5}) {
		t.Fatalf("process=%+v err=%v", pr, err)
	}
	if err := s.Ack(5); err != nil {
		t.Fatal(err)
	}
	if w := s.Watermark(); w != 17 {
		t.Fatalf("W=%d, want 17 after all done", w)
	}
	add, err = s.Add(17, 25)
	if err != nil || add.ID != 5 {
		t.Fatalf("final add=%+v err=%v", add, err)
	}
}

func TestSplitRoundingAndBoundaries(t *testing.T) {
	cases := []struct {
		from, to, processed, num, den Offset
		keep                          Offset
	}{
		{0, 10, 0, 1, 3, 4},
		{0, 10, 0, 2, 3, 7},
		{0, 10, 0, 3, 4, 8},
		{0, 10, 0, 1, 2, 5},
		{0, 10, 0, 4, 7, 6},
	}
	for _, tc := range cases {
		s := NewScheduler()
		add, err := s.Add(tc.from, tc.to)
		if err != nil {
			t.Fatal(err)
		}
		if tc.processed > 0 {
			if _, err := s.Process(add.ID, tc.processed); err != nil {
				t.Fatal(err)
			}
		}
		base := tc.from + tc.processed
		childID, err := s.Split(add.ID, tc.num, tc.den)
		if err != nil {
			t.Fatalf("split %+v: %v", tc, err)
		}
		p, _ := s.Progress(add.ID)
		child, _ := s.Progress(childID.ID)
		if p.To != base+tc.keep || child.From != base+tc.keep || child.To != tc.to {
			t.Fatalf("rounding %+v: parent=%+v child=%+v", tc, p, child)
		}
	}

	s := NewScheduler()
	a, _ := s.Add(17, 20)
	if _, err := s.Process(a.ID, 1); err != nil {
		t.Fatal(err)
	}
	child, err := s.Split(a.ID, 0, 1)
	if err != nil || child.ID != 2 {
		t.Fatalf("num=0 split: %+v %v", child, err)
	}
	if p, _ := s.Progress(a.ID); p.To != 19 {
		t.Fatalf("num=0 parent=%+v", p)
	}
	if _, err := s.Process(a.ID, 2); err != nil {
		t.Fatal(err)
	}
	_, err = s.Split(child.ID, 0, 1)
	if !errors.Is(err, ErrCannotSplit) {
		t.Fatalf("rem=1 err=%v", err)
	}
	if p, _ := s.Progress(child.ID); p.From != 19 || p.To != 20 || p.Next != 19 {
		t.Fatalf("cannot split changed child: %+v", p)
	}
	if _, err := s.Process(child.ID, 1); err != nil {
		t.Fatal(err)
	}
}

func TestSplitUsesNextAndPreservesBatches(t *testing.T) {
	s := NewScheduler()
	a, _ := s.Add(0, 10)
	first, err := s.Process(a.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Split(a.ID, 1, 2)
	if err != nil || child.ID != 2 {
		t.Fatalf("split %+v %v", child, err)
	}
	parent, _ := s.Progress(a.ID)
	if parent.From != 0 || parent.To != 7 || parent.Next != 3 {
		t.Fatalf("split based on from not next: %+v", parent)
	}
	if err := s.Ack(first.BatchID); err != nil {
		t.Fatal(err)
	}
	p2, err := s.Process(a.ID, 10)
	if err != nil || p2 != (ProcessResult{4, 3, 7, 2}) {
		t.Fatalf("remaining parent=%+v err=%v", p2, err)
	}
	p3, err := s.Process(child.ID, 10)
	if err != nil || p3 != (ProcessResult{3, 7, 10, 3}) {
		t.Fatalf("child=%+v err=%v", p3, err)
	}
}

func TestLargeProductUsesExactCeiling(t *testing.T) {
	s := NewScheduler()
	from := int64(0)
	to := MaxOffset
	a, err := s.Add(from, to)
	if err != nil {
		t.Fatal(err)
	}
	num, den := int64(999999), int64(1000000)
	child, err := s.Split(a.ID, num, den)
	if err != nil {
		t.Fatal(err)
	}
	expected := new(big.Int).Mul(big.NewInt(to), big.NewInt(num))
	quot, rem := new(big.Int).QuoRem(expected, big.NewInt(den), new(big.Int))
	if rem.Sign() != 0 {
		quot.Add(quot, big.NewInt(1))
	}
	p, _ := s.Progress(a.ID)
	if p.To != quot.Int64() {
		t.Fatalf("got %d want %s rem=%s", p.To, quot.String(), rem.String())
	}
	cp, _ := s.Progress(child.ID)
	if cp.From != p.To || cp.To != to {
		t.Fatalf("child=%+v", cp)
	}
}

func TestOutOfOrderAckAndHold(t *testing.T) {
	s := NewScheduler()
	a, _ := s.Add(0, 10)
	b1, _ := s.Process(a.ID, 2)
	b2, _ := s.Process(a.ID, 2)
	b3, _ := s.Process(a.ID, 2)
	if p, _ := s.Progress(a.ID); p.Next != 6 {
		t.Fatalf("next=%d", p.Next)
	}
	b4, _ := s.Process(a.ID, 4)
	_, err := s.Process(a.ID, 1)
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("process while exhausted but pending: %v", err)
	}
	_, err = s.Split(a.ID, 1, 2)
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("split while exhausted but pending: %v", err)
	}
	if err := s.Ack(b2.BatchID); err != nil {
		t.Fatal(err)
	}
	if w := s.Watermark(); w != 0 {
		t.Fatalf("later ack moved W=%d", w)
	}
	if err := s.Ack(b3.BatchID); err != nil {
		t.Fatal(err)
	}
	if w := s.Watermark(); w != 0 {
		t.Fatalf("earliest still pending W=%d", w)
	}
	if err := s.Ack(b1.BatchID); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(b4.BatchID); err != nil {
		t.Fatal(err)
	}
	if w := s.Watermark(); w != 6 {
		t.Fatalf("after all acks W=%d want frozen 6", w)
	}
	_, err = s.Process(a.ID, 10)
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("process pending-exhausted err=%v", err)
	}
	_, err = s.Split(a.ID, 1, 2)
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("split pending-exhausted err=%v", err)
	}
}

func TestRejectionsAndStatePreservation(t *testing.T) {
	s := NewScheduler()
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"add invalid before watermark", func() error { _, err := s.Add(-1, 10); return err }, ErrInvalidArgument},
		{"add reversed", func() error { _, err := s.Add(10, 10); return err }, ErrInvalidArgument},
		{"add over max", func() error { _, err := s.Add(0, MaxOffset+1); return err }, ErrInvalidArgument},
		{"process invalid n", func() error { _, err := s.Process(1, 0); return err }, ErrInvalidArgument},
		{"process missing", func() error { _, err := s.Process(99, 1); return err }, ErrIntervalNotFound},
		{"split invalid den", func() error { _, err := s.Split(1, 0, 0); return err }, ErrInvalidArgument},
		{"split num over den", func() error { _, err := s.Split(1, 2, 1); return err }, ErrInvalidArgument},
		{"split missing", func() error { _, err := s.Split(99, 1, 2); return err }, ErrIntervalNotFound},
		{"ack invalid", func() error { return s.Ack(0) }, ErrInvalidArgument},
		{"ack missing", func() error { return s.Ack(99) }, ErrBatchNotFound},
		{"progress missing", func() error { _, err := s.Progress(99); return err }, ErrIntervalNotFound},
	}
	for _, tc := range cases {
		if err := tc.run(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	a, _ := s.Add(10, 20)
	_, err := s.Add(9, 10)
	if !errors.Is(err, ErrBelowWatermark) {
		t.Fatalf("below W err=%v", err)
	}
	b, err := s.Process(a.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(b.BatchID); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(b.BatchID); !errors.Is(err, ErrAlreadyAcknowledged) {
		t.Fatalf("repeat ack err=%v", err)
	}
	p, _ := s.Progress(a.ID)
	if p.Next != 12 || p.PendingBatches != 0 {
		t.Fatalf("rejection changed state: %+v", p)
	}
}

func TestWatermarkDoesNotRegressOrJumpToInfinity(t *testing.T) {
	s := NewScheduler()
	a, _ := s.Add(10, 20)
	b, _ := s.Add(30, 40)
	if _, err := s.Process(a.ID, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Process(b.ID, 10); err != nil {
		t.Fatal(err)
	}
	c, err := s.Add(12, 18)
	if err != nil || c.ID != 3 {
		t.Fatalf("low-hold add=%+v err=%v", c, err)
	}
	if w := s.Watermark(); w != 10 {
		t.Fatalf("W regressed/new min: %d", w)
	}
	if err := s.Ack(2); err != nil {
		t.Fatal(err)
	}
	if w := s.Watermark(); w != 10 {
		t.Fatalf("W=%d", w)
	}
	if _, err := s.Process(c.ID, 6); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(3); err != nil {
		t.Fatal(err)
	}
	if w := s.Watermark(); w != 10 {
		t.Fatalf("after low hold W=%d", w)
	}
	if err := s.Ack(1); err != nil {
		t.Fatal(err)
	}
	if w := s.Watermark(); w != 10 {
		t.Fatalf("all done W=%d, want frozen 10", w)
	}
	if _, err := s.Add(9, 10); !errors.Is(err, ErrBelowWatermark) {
		t.Fatalf("post completion add err=%v", err)
	}
	if _, err := s.Add(10, 11); err != nil {
		t.Fatalf("equal frozen W add: %v", err)
	}
}

func TestHoldProbeScalesAreConstant(t *testing.T) {
	measure := func(t *testing.T, intervalCount, pendingCount int64) (int64, int64) {
		t.Helper()
		s := NewScheduler()
		var target ID
		for i := int64(0); i < intervalCount; i++ {
			a, err := s.Add(100+i*100, 100+i*100+pendingCount+2)
			if err != nil {
				t.Fatal(err)
			}
			for j := int64(0); j < pendingCount; j++ {
				if _, err := s.Process(a.ID, 1); err != nil {
					t.Fatal(err)
				}
			}
			if i == 0 {
				target = a.ID
			}
		}
		processedBefore := s.HoldProbes()
		last, err := s.Process(target, 1)
		if err != nil {
			t.Fatal(err)
		}
		processProbes := s.HoldProbes() - processedBefore
		for j := int64(0); j < pendingCount; j++ {
			batchID := last.BatchID - int64(pendingCount) + j
			if err := s.Ack(batchID); err != nil {
				t.Fatal(err)
			}
		}
		ackBefore := s.HoldProbes()
		if err := s.Ack(last.BatchID); err != nil {
			t.Fatal(err)
		}
		ackProbes := s.HoldProbes() - ackBefore
		return processProbes, ackProbes
	}
	p10, a10 := measure(t, 10, 10)
	p10000, a10000 := measure(t, 10000, 10)
	if p10 != p10000 || a10 != a10000 {
		t.Fatalf("interval scale process=%d/%d ack=%d/%d", p10, p10000, a10, a10000)
	}
	p10Batches, a10Batches := measure(t, 10, 10)
	p10000Batches, a10000Batches := measure(t, 10, 10000)
	if p10Batches != p10000Batches || a10Batches != a10000Batches {
		t.Fatalf("batch scale process=%d/%d ack=%d/%d", p10Batches, p10000Batches, a10Batches, a10000Batches)
	}
	if p10 > 4 || a10 > 4 {
		t.Fatalf("probe deltas without discards process=%d ack=%d", p10, a10)
	}
}

func TestConcurrentOperations(t *testing.T) {
	s := NewScheduler()
	const workers = 64
	var wg sync.WaitGroup
	ids := make(chan ID, workers)
	for i := 0; i < workers; i++ {
		base := int64(1_000_000_000 + i*100)
		add, err := s.Add(base, base+20)
		if err != nil {
			t.Fatal(err)
		}
		ids <- add.ID
	}
	close(ids)
	for id := range ids {
		wg.Add(1)
		go func(id ID) {
			defer wg.Done()
			batches := make([]ID, 0, 10)
			for range 10 {
				result, err := s.Process(id, 2)
				if err != nil {
					t.Error(err)
					return
				}
				batches = append(batches, result.BatchID)
			}
			for _, batchID := range batches {
				if err := s.Ack(batchID); err != nil {
					t.Error(err)
					return
				}
			}
		}(id)
	}
	wg.Wait()
	for id := ID(1); id <= workers; id++ {
		p, err := s.Progress(id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Remaining != 0 || p.PendingBatches != 0 {
			t.Fatalf("interval %d=%+v", id, p)
		}
	}
}
