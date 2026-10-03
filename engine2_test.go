package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func fillEngine(t *testing.T, e *Engine, n int) {
	t.Helper()
	const chunk = 500
	for start := 0; start < n; start += chunk {
		end := start + chunk
		if end > n {
			end = n
		}
		b := make([]Event, 0, end-start)
		for i := start; i < end; i++ {
			b = append(b, mkev(int64(i), "a", "x", "z"))
		}
		if err := e.Append(b); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExaminedDoesNotScanOutsideRange(t *testing.T) {
	e1 := newTestEngine(1000000, 2000000)
	fillEngine(t, e1, 100)
	e1.rangeEvents(40, 60)
	small := e1.examinedCount()

	e2 := newTestEngine(1000000, 2000000)
	fillEngine(t, e2, 10000)
	e2.rangeEvents(40, 60)
	big := e2.examinedCount()

	if small != 20 || big != 20 || small != big {
		t.Fatalf("examined small=%d big=%d (both must equal in-range 20)", small, big)
	}
	t.Logf("examined equal for 100 vs 10000 stored: %d", small)
}

func TestDeterministicAcrossBatchSplits(t *testing.T) {
	build := func(splits ...[]Event) *Engine {
		e := newTestEngine(100000, 1000000)
		for _, b := range splits {
			if err := e.Append(b); err != nil {
				t.Fatal(err)
			}
		}
		return e
	}
	evs := []Event{
		mkev(1, "a", "x", "z"), mkev(2, "b", "y", "z"),
		mkev(2, "a", "y", "z"), mkev(3, "a", "x", "z"),
	}
	eA := build(evs)
	eB := build(evs[:1], evs[1:3], evs[3:])
	tA, _ := eA.Tabulate("r1", "d1", "d2", 0, 100)
	tB, _ := eB.Tabulate("r1", "d1", "d2", 0, 100)
	if fmt.Sprint(tA) != fmt.Sprint(tB) {
		t.Fatalf("non-deterministic across batch splits:\nA=%+v\nB=%+v", tA, tB)
	}
	t.Logf("batch-split invariant ok: %+v", tA)
}

func TestConcurrentAppendAndTabulate(t *testing.T) {
	// 每格累计较大计数，避免抑制隐藏总计；追加完成后总计应等于全部事件数。
	e := newTestEngine(1000000, 2000000)

	const writers = 8
	const perWriter = 200
	var writerWG, readerWG sync.WaitGroup

	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(w int) {
			defer writerWG.Done()
			b := make([]Event, 0, perWriter)
			for i := 0; i < perWriter; i++ {
				row := "a"
				if w%2 == 0 {
					row = "b"
				}
				col := "x"
				if i%2 == 0 {
					col = "y"
				}
				b = append(b, mkev(int64(w*1000+i), row, col, "z"))
			}
			if err := e.Append(b); err != nil {
				t.Errorf("append: %v", err)
			}
		}(w)
	}

	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = e.Tabulate("r1", "d1", "d2", 0, 100000)
				}
			}
		}()
	}

	writerWG.Wait()
	close(stop)
	readerWG.Wait()
	tab, err := e.Tabulate("r1", "d1", "d2", 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if tab.Total != int64(writers*perWriter) {
		t.Fatalf("total=%d want=%d", tab.Total, writers*perWriter)
	}
	t.Logf("concurrent total=%d", tab.Total)
}
