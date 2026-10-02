package ontology

import (
	"sync"
	"testing"
)

func TestFrameWorkIndependentOfWidth(t *testing.T) {
	measure := func(width int64) int64 {
		a, err := NewRangeAggregator(width, 0, 20000)
		if err != nil {
			t.Fatal(err)
		}
		for ts := int64(1); ts <= 20000; ts++ {
			_, status, err := a.Insert(Row{Key: []byte("k"), TS: ts, Val: 1})
			if err != nil || status != "buffered" {
				t.Fatalf("ts=%d status=%q err=%v", ts, status, err)
			}
		}
		outputs, err := a.Advance(20000)
		if err != nil || len(outputs) != 20000 {
			t.Fatalf("advance outputs=%d err=%v", len(outputs), err)
		}
		if a.frameWork > 4*a.released {
			t.Fatalf("frameWork=%d released=%d", a.frameWork, a.released)
		}
		return a.frameWork
	}

	narrow := measure(10)
	wide := measure(1_000_000_000)
	if narrow > 80000 || wide > 80000 {
		t.Fatalf("narrow=%d wide=%d", narrow, wide)
	}
	if narrow == 0 || wide == 0 {
		t.Fatalf("expected nonzero counters narrow=%d wide=%d", narrow, wide)
	}
}

func TestLateWorkIndependentOfOutsideBuckets(t *testing.T) {
	measure := func(outsideBuckets int) int64 {
		const width = int64(100000)
		const historyWM = int64(100000)
		const targetWM = int64(300000)
		a, err := NewRangeAggregator(width, targetWM, 1_000_000)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Advance(historyWM); err != nil {
			t.Fatal(err)
		}
		start := int64(1)
		for i := 0; i < outsideBuckets; i++ {
			ts := start + int64(i)
			out, status, err := a.Insert(Row{Key: []byte("k"), TS: ts, Val: 1})
			if err != nil || status != "supplement" {
				t.Fatalf("history ts=%d status=%q err=%v", ts, status, err)
			}
			if out.Cnt < 1 {
				t.Fatalf("history ts=%d frame=%+v", ts, out)
			}
		}
		if _, err := a.Advance(targetWM); err != nil {
			t.Fatal(err)
		}
		a.lateWork = 0
		before := a.lateWork
		out, status, err := a.Insert(Row{Key: []byte("k"), TS: targetWM, Val: 2})
		if err != nil || status != "supplement" {
			t.Fatalf("target status=%q err=%v", status, err)
		}
		if out.Cnt != 1 || out.Sum != 2 || out.Max != 2 {
			t.Fatalf("target frame=%+v", out)
		}
		delta := a.lateWork - before
		if delta > 3 {
			t.Fatalf("outsideBuckets=%d delta=%d", outsideBuckets, delta)
		}
		return a.lateWork
	}

	with10 := measure(10)
	with10000 := measure(10000)
	if with10000-with10 > 6 {
		t.Fatalf("lateWork 10=%d 10000=%d", with10, with10000)
	}
}

func TestConcurrentOperationsSerializeAndPreserveInvariants(t *testing.T) {
	a, err := NewRangeAggregator(2, 2, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if (id+j)%5 == 0 {
					_, _ = a.Advance(int64((id + j) % 200))
				} else {
					_, _, _ = a.Insert(Row{Key: []byte{byte('a' + id%4)}, TS: int64((id + j) % 200), Val: int64(id)})
				}
				_ = a.Retained()
			}
		}(i)
	}
	wg.Wait()
	if int64(a.buffer.Len()) > a.capacity {
		t.Fatalf("buffer=%d capacity=%d", a.buffer.Len(), a.capacity)
	}
	if a.Retained() < 0 {
		t.Fatalf("negative retained %d", a.Retained())
	}
}
