package ontology

import (
	"container/heap"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestSmoke(t *testing.T) {
	s, err := NewScheduler(0, 0, 0, 0, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Poll(0)
	if err != nil || len(got) != 0 {
		t.Fatalf("got deliveries=%v err=%v", got, err)
	}
}

func TestConstructorValidation(t *testing.T) {
	cases := []struct {
		name string
		args [5]int64
		lim  int
		want error
	}{
		{"offset low", [5]int64{-721, 0, 0, 0, 1}, 1, ErrInvalidOffset},
		{"offset high", [5]int64{841, 0, 0, 0, 1}, 1, ErrInvalidOffset},
		{"quiet start", [5]int64{0, -1, 0, 0, 1}, 1, ErrInvalidQuietStart},
		{"quiet end", [5]int64{0, 0, 1440, 0, 1}, 1, ErrInvalidQuietEnd},
		{"window", [5]int64{0, 0, 0, 1441, 1}, 1, ErrInvalidMergeWindow},
		{"cap", [5]int64{0, 0, 0, 0, 1001}, 1, ErrInvalidDailyCap},
		{"limit", [5]int64{0, 0, 0, 0, 1}, 100001, ErrInvalidQueueLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewScheduler(tc.args[0], tc.args[1], tc.args[2], tc.args[3], tc.args[4], tc.lim)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestQuietBoundariesAndWindowOrder(t *testing.T) {
	s := mustScheduler(t, 0, 100, 200, 0, 10, 10)
	if got := s.shifted(100); got != 200 {
		t.Fatalf("at qs shifted=%d want 200; input lm=100, basis=start included", got)
	}
	if got := s.shifted(200); got != 200 {
		t.Fatalf("at qe shifted=%d want 200; basis=end excluded", got)
	}

	s.window = 20
	got, err := s.Submit([]byte("g0"), 0, 90)
	if err != nil || len(got) != 0 {
		t.Fatalf("Submit input=(g0,0,90), output deliveries=%v err=%v", got, err)
	}
	batch := s.byKey["g0"]
	if batch.f != 200 {
		t.Fatalf("output f=%d want=200; basis=90+20 enters quiet then shift", batch.f)
	}
}

func TestZeroWindowInsideQuiet(t *testing.T) {
	s := mustScheduler(t, 0, 100, 200, 0, 10, 10)
	got, err := s.Submit([]byte("z"), 0, 150)
	if err != nil || len(got) != 0 {
		t.Fatalf("Submit(z,0,150) output=%v err=%v", got, err)
	}
	if got := s.byKey["z"].f; got != 200 {
		t.Fatalf("zero-window f=%d want=200; basis=shift(150+0)", got)
	}
}

func TestCrossMidnightQuietAndMerge(t *testing.T) {
	s := mustScheduler(t, 480, 1320, 420, 30, 10, 10)
	for _, now := range []int64{830, 900, 1000} {
		got, err := s.Submit([]byte("a"), 0, now)
		t.Logf("input=Submit(a,0,%d) output=%v err=%v basis=all f=1380 and merge", now, got, err)
		if err != nil || len(got) != 0 {
			t.Fatalf("got=%v err=%v", got, err)
		}
	}
	if got := s.byKey["a"].count; got != 3 {
		t.Fatalf("merged count=%d want=3", got)
	}
	if got := s.localMinute(1000); got != 40 {
		t.Fatalf("lm=%d want=40; basis=cross-midnight quiet", got)
	}
	if got := s.shifted(1000); got != 1380 {
		t.Fatalf("shift=%d want=1380", got)
	}
}

func TestDueAtNowBeforeMerge(t *testing.T) {
	s := mustScheduler(t, 0, 0, 0, 10, 10, 10)
	if _, err := s.Submit([]byte("a"), 0, 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.Submit([]byte("a"), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Count != 1 || got[0].At != 10 {
		t.Fatalf("output=%v want old batch delivered before merge at f=now", got)
	}
	if s.byKey["a"].count != 1 {
		t.Fatalf("new batch count=%d want=1", s.byKey["a"].count)
	}
}

func TestExampleUrgentOccupiesCapAndDailyRollover(t *testing.T) {
	s := mustScheduler(t, 480, 1320, 420, 30, 2, 100)
	operations := []struct {
		key  string
		prio int64
		now  int64
	}{
		{"a", 0, 830},
		{"a", 0, 900},
		{"b", 1, 1000},
		{"u", 2, 1100},
	}
	for _, op := range operations {
		got, err := s.Submit([]byte(op.key), op.prio, op.now)
		t.Logf("input=Submit(%s,%d,%d) output=%v err=%v", op.key, op.prio, op.now, got, err)
		if err != nil {
			t.Fatal(err)
		}
		if op.prio == 2 {
			assertDelivery(t, got, "u", 1, 1100)
		} else if len(got) != 0 {
			t.Fatalf("unexpected deliveries=%v", got)
		}
	}

	got, err := s.Poll(1380)
	t.Logf("input=Poll(1380) output=%v err=%v basis=urgent made sent=1, a uses last slot", got, err)
	if err != nil {
		t.Fatal(err)
	}
	assertDelivery(t, got, "a", 2, 1380)

	got, err = s.Poll(2820)
	t.Logf("input=Poll(2820) output=%v err=%v basis=b moved through quiet next midnight", got, err)
	if err != nil {
		t.Fatal(err)
	}
	assertDelivery(t, got, "b", 1, 2820)
}

func TestMultiDayCascadeFromDueLoop(t *testing.T) {
	s := mustScheduler(t, 0, 0, 0, 0, 1, 10)
	s.sent[0] = 1
	s.sent[1] = 1
	s.sent[2] = 1
	heap.Push(s.batches, &batch{key: []byte("x"), count: 7, f: 0})
	s.dueProbes = 0

	got := s.processDue(5000)
	t.Logf("input=processDue(batch f=0,now=5000,sent days 0..2 full) output=%v basis=3 daily deferrals", got)
	assertDelivery(t, got, "x", 7, 4320)
}

func TestNegativeOffsetFloorDivision(t *testing.T) {
	s := mustScheduler(t, -480, 0, 0, 0, 1, 1)
	if got := s.localMinute(100); got != 1060 {
		t.Fatalf("lm=%d want=1060; basis=nonnegative modulo", got)
	}
	if got := s.localDay(100); got != -1 {
		t.Fatalf("dy=%d want=-1; basis=floor toward negative infinity", got)
	}
}

func TestImportantIsNeverMerged(t *testing.T) {
	s := mustScheduler(t, 0, 0, 0, 100, 10, 10)
	if _, err := s.Submit([]byte("a"), 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.byKey["a"]; ok {
		t.Fatal("important batch must not occupy mergeable key index")
	}
	if _, err := s.Submit([]byte("a"), 1, 20); err != nil {
		t.Fatal(err)
	}
	if got := len(*s.batches); got != 1 {
		t.Fatalf("pending batches=%d want=1; first was due and second replaced it", got)
	}
}

func TestQueueFullBoundaryAndRejectionHasNoSideEffect(t *testing.T) {
	s := mustScheduler(t, 0, 0, 0, 100, 10, 1)
	if _, err := s.Submit([]byte("a"), 1, 0); err != nil {
		t.Fatal(err)
	}
	_, err := s.Submit([]byte("b"), 1, 0)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full at equality err=%v want=%v", err, ErrQueueFull)
	}
	if s.lastNow != 0 || len(*s.batches) != 1 {
		t.Fatalf("rejected operation changed state: lastNow=%d batches=%d", s.lastNow, len(*s.batches))
	}

	s2 := mustScheduler(t, 0, 0, 0, 100, 10, 2)
	if _, err := s2.Submit([]byte("a"), 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Submit([]byte("b"), 1, 0); err != nil {
		t.Fatalf("one slot below limit should accept: %v", err)
	}

	_, err = s.Submit([]byte("c"), 1, 1)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("pre-operation fullness err=%v", err)
	}
	if got := (*s.batches)[0].f; got != 0 {
		t.Fatalf("rejected operation ran due processing; f=%d want=0", got)
	}

	urgent, err := s.Submit([]byte("u"), 2, 0)
	if err != nil || len(urgent) != 2 || string(urgent[1].Key) != "u" || urgent[1].At != 0 {
		t.Fatalf("urgent while full output=%v err=%v; basis=urgent needs no batch", urgent, err)
	}
}

func TestQueueFullMergesAfterDailyCapPostponement(t *testing.T) {
	s := mustScheduler(t, 0, 0, 0, 0, 1, 1)
	if _, err := s.Submit([]byte("u"), 2, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit([]byte("a"), 0, 0); err != nil {
		t.Fatal(err)
	}

	got, err := s.Submit([]byte("a"), 0, 1)
	t.Logf("input=Submit(a,0,1) with M=1 full output=%v err=%v basis=a postponed from 0 to 1440 then merges", got, err)
	if err != nil || len(got) != 0 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	batch := s.byKey["a"]
	if batch.count != 2 || batch.f != 1440 {
		t.Fatalf("batch count=%d f=%d want count=2,f=1440", batch.count, batch.f)
	}

	got, err = s.Submit([]byte("b"), 0, 1)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("different key while full err=%v want=%v", err, ErrQueueFull)
	}
	if batch.count != 2 || s.sent[0] != 1 {
		t.Fatalf("rejected different key changed state: count=%d sent0=%d", batch.count, s.sent[0])
	}
}

func TestRejectionOrderAndInvalidOperationDoesNotProcessDue(t *testing.T) {
	s := mustScheduler(t, 0, 0, 0, 0, 1, 10)
	if _, err := s.Submit([]byte("u"), 2, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit([]byte("a"), 0, 0); err != nil {
		t.Fatal(err)
	}

	_, err := s.Submit(nil, 9, 1_000_000_000_001)
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("err=%v want empty key first", err)
	}
	_, err = s.Submit([]byte("x"), 9, 1_000_000_000_001)
	if !errors.Is(err, ErrInvalidPriority) {
		t.Fatalf("err=%v want invalid priority before time", err)
	}
	_, err = s.Submit([]byte("x"), 0, -1)
	if !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("err=%v want invalid time", err)
	}

	_, err = s.Submit(nil, 0, 1)
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("err=%v", err)
	}
	if s.lastNow != 0 || (*s.batches)[0].f != 0 {
		t.Fatalf("invalid submission processed due batch; lastNow=%d f=%d want both 0", s.lastNow, (*s.batches)[0].f)
	}

	got, err := s.Poll(1)
	t.Logf("input=Poll(1) after rejection output=%v err=%v basis=day 0 full, move to 1440", got, err)
	if err != nil || len(got) != 0 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if got := (*s.batches)[0].f; got != 1440 || s.lastNow != 1 {
		t.Fatalf("f=%d want=1440", got)
	}

	if _, err := s.Poll(1); err != nil {
		t.Fatalf("equal timestamps should be accepted: %v", err)
	}
	if _, err := s.Poll(0); !errors.Is(err, ErrClockMovedBackwards) {
		t.Fatalf("backwards err=%v want=%v", err, ErrClockMovedBackwards)
	}
}

func TestDueProbeCounterIndependentOfPendingTotal(t *testing.T) {
	for _, total := range []int{1000, 100000} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			s := mustScheduler(t, 0, 0, 0, 0, 1, total)
			s.sent[0] = 1
			heap.Push(s.batches, &batch{key: []byte("due"), f: 0})
			for i := 1; i < total; i++ {
				heap.Push(s.batches, &batch{key: []byte(fmt.Sprintf("b%d", i)), f: 100000})
			}
			s.dueProbes = 0
			got := s.processDue(0)
			if len(got) != 0 {
				t.Fatalf("unexpected delivery=%v", got)
			}
			if s.dueProbes != 2 {
				t.Fatalf("probe count=%d want=2; basis=1 due batch + 1 postponement + 1 bound", s.dueProbes)
			}
		})
	}
}

func TestConcurrentOperations(t *testing.T) {
	s := mustScheduler(t, 0, 0, 0, 100, 1000, 1000)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				now := int64(i*20 + j)
				_, _ = s.Submit([]byte(fmt.Sprintf("k-%d-%d", i, j)), int64(j%3), now)
			}
		}(i)
	}
	wg.Wait()

	if s.lastNow != 639 || s.seq < 0 || s.seq > 640 {
		t.Fatalf("serialized result inconsistent: lastNow=%d seq=%d", s.lastNow, s.seq)
	}
}

func mustScheduler(t *testing.T, off, qs, qe, window, dailyCap int64, limit int) *Scheduler {
	t.Helper()
	s, err := NewScheduler(off, qs, qe, window, dailyCap, limit)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func assertDelivery(t *testing.T, got []Delivery, key string, count, at int64) {
	t.Helper()
	if len(got) != 1 || string(got[0].Key) != key || got[0].Count != count || got[0].At != at {
		t.Fatalf("deliveries=%v want one (%s,%d,%d)", got, key, count, at)
	}
}
