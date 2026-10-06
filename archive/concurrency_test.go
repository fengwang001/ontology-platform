package archive_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/archive"
)

// TestConcurrentReserveOneVolume：同一卷大量并发预约，结果等价于某个串行顺序：
// 队列人数正确、每人恰出现一次、无数据竞争（-race）。
func TestConcurrentReserveOneVolume(t *testing.T) {
	cfg := archive.Config{
		LoanDays: [4]int{10, 10, 10, 10}, PickupDeadlineDays: 3,
		RenewWindowDays: 5, MaxRenewals: 1, OverdueThreshold: 99, CooldownDays: 2,
	}
	s := archive.NewService(cfg)
	n := 60
	for i := 0; i < n; i++ {
		s.AddUser(fmt.Sprintf("u%d", i), archive.ClassTopSecret)
	}
	s.AddVolume("v", archive.ClassPublic)
	if o := s.Borrow(0, "u0", "v"); !o.OK {
		t.Fatal(o.Reason)
	}
	var wg sync.WaitGroup
	errs := make([]archive.Outcome, 0, n)
	var mu sync.Mutex
	start := make(chan struct{})
	for i := 1; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			o := s.Reserve(1, fmt.Sprintf("u%d", i), "v")
			mu.Lock()
			errs = append(errs, o)
			mu.Unlock()
		}(i)
	}
	close(start)
	wg.Wait()
	for _, o := range errs {
		if !o.OK {
			t.Fatalf("concurrent reserve rejected: %s %s", o.Err, o.Reason)
		}
	}
	q := s.GetVolume("v").QueueUserIDs
	if len(q) != n-1 {
		t.Fatalf("queue len=%d want %d", len(q), n-1)
	}
	seen := map[string]bool{}
	for _, id := range q {
		if seen[id] {
			t.Fatalf("duplicate in queue: %s", id)
		}
		seen[id] = true
	}
}

// TestConcurrentDisjointVolumes：不同 goroutine 操作互不相交的卷集合，
// 终态必须与某个串行顺序一致（全部在库、无在借）。
func TestConcurrentDisjointVolumes(t *testing.T) {
	cfg := archive.Config{
		LoanDays: [4]int{10, 10, 10, 10}, PickupDeadlineDays: 3,
		RenewWindowDays: 5, MaxRenewals: 1, OverdueThreshold: 99, CooldownDays: 2,
	}
	w := newWorld(cfg, 8, 8)
	var wg sync.WaitGroup
	for i := 0; i < w.nVols; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			uid := fmt.Sprintf("u%d", i)
			w.s.Borrow(2, uid, fmt.Sprintf("v%d", i))
			w.s.Return(5, uid, fmt.Sprintf("v%d", i))
		}(i)
	}
	wg.Wait()
	for i := 0; i < w.nVols; i++ {
		v := w.s.GetVolume(fmt.Sprintf("v%d", i))
		if v.Status != archive.VolInLibrary || v.Loan != nil {
			t.Fatalf("v%d should be back in library: %+v", i, v)
		}
	}
}

// TestReplayDeterminism：相同操作序列重放两次，结果日志与快照必须逐字节相同。
func TestReplayDeterminism(t *testing.T) {
	cfg := archive.Config{
		LoanDays: [4]int{6, 8, 10, 12}, PickupDeadlineDays: 2,
		RenewWindowDays: 3, MaxRenewals: 2, OverdueThreshold: 5, CooldownDays: 2,
	}
	seq := []step{
		{kind: opBorrow, now: 0, user: 0, vol: 0},
		{kind: opReserve, now: 1, user: 1, vol: 0},
		{kind: opReserve, now: 1, user: 2, vol: 0},
		{kind: opDowngrade, user: 1},
		{kind: opReturn, now: 3, user: 0, vol: 0},
		{kind: opPickup, now: 4, user: 2, vol: 0},
		{kind: opRestore, user: 1},
		{kind: opReturn, now: 9, user: 2, vol: 0},
		{kind: opPickup, now: 9, user: 1, vol: 0},
		{kind: opRenew, now: 12, user: 1, vol: 0, approver: "boss"},
		{kind: opReturn, now: 18, user: 1, vol: 0},
		{kind: opSeal, vol: 1},
		{kind: opAdvance, now: 20},
	}
	run := func() ([]string, string) {
		w := newWorld(cfg, 4, 4)
		var log []string
		for _, st := range seq {
			w.apply(t, st, func(format string, a ...interface{}) {
				log = append(log, fmt.Sprintf(format, a...))
			})
		}
		return log, w.s.Snapshot()
	}
	log1, snap1 := run()
	log2, snap2 := run()
	if snap1 != snap2 {
		t.Fatalf("replay snapshots differ:\n%s\n%s", snap1, snap2)
	}
	if len(log1) != len(log2) {
		t.Fatal("replay log length differs")
	}
	for i := range log1 {
		if log1[i] != log2[i] {
			t.Fatalf("replay log differs at %d:\n%s\n%s", i, log1[i], log2[i])
		}
	}
	t.Logf("replay snapshot:\n%s", snap1)
}
