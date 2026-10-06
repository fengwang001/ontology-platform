package loto_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/loto"
)

// 并发调用下系统须等价于某个串行顺序：多个 goroutine 各发一条单调时刻流，
// 最终状态必须是一个合法状态（全员上锁后可验证开工；无数据竞争，由 -race 保证）。
func TestConcurrentCallsAreSerializable(t *testing.T) {
	s := loto.New()
	mustOK(t, s.RegisterPerson("ap", loto.RoleApplicant), "ap")
	mustOK(t, s.RegisterPerson("a1", loto.RoleApprover), "a1")
	mustOK(t, s.RegisterPerson("v"), "v")
	const n = 20
	for i := 0; i < n; i++ {
		id := workerID(i)
		mustOK(t, s.RegisterPerson(id, loto.RoleWorker), id)
	}
	mustOK(t, s.RegisterDevice("d1", []string{"p1"}), "d1")

	// 单调全局时钟：所有调用时刻严格递增，唯一可能的拒绝来自业务条件，
	// 因此最终每张"被抽到去上锁"的票锁状态必须自洽。
	var clock int64
	var clockMu sync.Mutex
	next := func() int64 {
		clockMu.Lock()
		defer clockMu.Unlock()
		clock++
		return clock
	}

	mustOK(t, s.Apply("P", "ap", []string{"d1"}, loto.WorkNormal, 0, 1<<40, workerIDs(n)), "apply")
	mustOK(t, s.Approve("P", "a1", next()), "approve")

	var wg sync.WaitGroup
	ready := make(chan struct{})
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-ready
			// 每个作业人员负责把自己那把锁挂好：若取到的时刻在等待期间被超过，
			// 用更新的时刻重试（ClockRollback 属正常调度结果，非数据损坏）。
			for {
				err := s.PlaceLock("P", workerID(i), "p1", next())
				if err == nil {
					return
				}
				if oe, ok := err.(*loto.OpError); ok && oe.Code == loto.ClockRollback {
					continue
				}
				errs <- err
				return
			}
		}(i)
	}
	close(ready)
	wg.Wait()
	close(errs)
	var collected []string
	for e := range errs {
		collected = append(collected, e.Error())
	}

	// 串行收尾：验证必须成功（每人恰好一把锁）。
	if err := s.Verify("P", "v", next()); err != nil {
		t.Fatalf("verify after concurrent locking: %v (lock errors=%v)", err, collected)
	}
	if err := s.StartWork("P", "ap", next()); err != nil {
		t.Fatalf("start: %v", err)
	}
	snap, ok := s.GetPermit("P")
	if !ok || snap.Phase != loto.PhaseWorking || len(snap.PhysicalLocks) != n {
		t.Fatalf("bad final state: %+v", snap)
	}

	// 并发只读送电判定与写操作并存，不得 panic / race。
	ready = make(chan struct{})
	var wg2 sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			<-ready
			for j := 0; j < 100; j++ {
				_, _, _ = s.CanEnergize("d1", next())
			}
		}()
	}
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		<-ready
		for i := 0; i < n; i++ {
			_ = s.Enter("P", workerID(i), next())
		}
	}()
	close(ready)
	wg2.Wait()
}

func workerID(i int) string { return fmt.Sprintf("cw%02d", i) }

func workerIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = workerID(i)
	}
	return out
}
