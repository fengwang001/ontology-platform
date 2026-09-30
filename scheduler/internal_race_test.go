package scheduler

import (
	"io"
	"math/rand/v2"
	"sync"
	"testing"
)

// TestConcurrentInvariants 在高并发到达/完成/取消/查询压力下持续校验内部不变量：
//   - 运行数不超过 K；
//   - 运行中的事务两两不冲突；
//   - 每个运行中的事务与所有更早未完成事务（含等待者）都不冲突；
//   - 等待队列严格按编号升序。
func TestConcurrentInvariants(t *testing.T) {
	const (
		K        = 4
		gor      = 8
		perG     = 40
		keySpace = 5
	)
	s := New(K, WithLogger(io.Discard))

	idCh := make(chan int, gor*perG)
	closeCh := make(chan struct{})
	var consWG sync.WaitGroup

	// 生产者：不断到达事务。
	go func() {
		rng := rand.New(rand.NewPCG(1, 2))
		for i := 0; i < gor*perG; i++ {
			r, w := randomSets(rng, keySpace)
			id, _, err := s.Arrive(r, w)
			if err != nil {
				t.Errorf("Arrive: %v", err)
				close(closeCh)
				return
			}
			idCh <- id
		}
	}()

	// 消费者：轮询运行中的事务并完成，偶尔取消等待者、查询阻塞者。
	for g := 0; g < gor-1; g++ {
		consWG.Add(1)
		go func(seed uint64) {
			defer consWG.Done()
			rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed*3+1)))
			for {
				select {
				case <-closeCh:
					return
				default:
				}
				seen := s.totalSeen()
				if seen == 0 {
					continue
				}
				id := 1 + rng.IntN(seen)
				switch rng.IntN(10) {
				case 0, 1, 2:
					if st, ok := s.Status(id); ok && st == StatusRunning {
						_, _ = s.Complete(id)
					}
				case 3:
					if st, ok := s.Status(id); ok && st == StatusWaiting {
						_, _ = s.Cancel(id)
					}
				default:
					_, _ = s.Blockers(id)
				}
				s.assertInvariants(t)
			}
		}(uint64(g + 10))
	}
	for i := 0; i < gor*perG; i++ {
		<-idCh
	}
	// 所有到达均已登记：先停止消费者，再由本线程统一排空，避免完成竞争。
	close(closeCh)
	consWG.Wait()

	// 排空：反复按编号升序完成运行中的事务；连锁放行最终把每个未取消的等待者送达完成。
	total := s.totalSeen()
	for {
		progress := false
		for id := 1; id <= total; id++ {
			if st, ok := s.Status(id); ok && st == StatusRunning {
				if _, err := s.Complete(id); err != nil {
					t.Errorf("drain Complete(%d): %v", id, err)
				}
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	s.assertInvariants(t)

	// 所有未取消事务必须已完成；等待队列必须为空。
	s.mu.Lock()
	if len(s.waiting) != 0 || s.running != 0 {
		t.Fatalf("after drain: waiting=%v running=%d", s.waiting, s.running)
	}
	s.mu.Unlock()
}

func (s *Scheduler) totalSeen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextID
}

func (s *Scheduler) assertInvariants(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running > s.k {
		t.Fatalf("running=%d exceeds K=%d", s.running, s.k)
	}

	runningIDs := make([]int, 0)
	for id, tx := range s.txns {
		if tx.status == StatusRunning {
			runningIDs = append(runningIDs, id)
		}
	}
	if len(runningIDs) != s.running {
		t.Fatalf("running counter=%d but %d txns running", s.running, len(runningIDs))
	}
	for i := 0; i < len(runningIDs); i++ {
		for j := i + 1; j < len(runningIDs); j++ {
			a, b := s.txns[runningIDs[i]], s.txns[runningIDs[j]]
			if conflict(a, b) {
				t.Fatalf("running txns %d and %d conflict", a.id, b.id)
			}
		}
		// 运行者必须与所有更早未完成（含等待）事务不冲突。
		id := runningIDs[i]
		tx := s.txns[id]
		for otherID := 1; otherID < id; otherID++ {
			other, ok := s.txns[otherID]
			if ok && other.status != StatusCompleted && conflict(tx, other) {
				t.Fatalf("running %d conflicts with earlier unfinished %d (%s)",
					id, otherID, other.status)
			}
		}
	}

	for i := 1; i < len(s.waiting); i++ {
		if s.waiting[i-1] >= s.waiting[i] {
			t.Fatalf("waiting queue not ascending: %v", s.waiting)
		}
	}
}

func randomSets(rng *rand.Rand, keySpace int) (reads, writes []string) {
	pick := func() []string {
		n := rng.IntN(3)
		out := make([]string, 0, n)
		seen := map[string]bool{}
		for i := 0; i < n; i++ {
			k := string(rune('a' + rng.IntN(keySpace)))
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
		return out
	}
	writes = pick()
	reads = pick()
	if len(reads) == 0 && len(writes) == 0 {
		writes = []string{"a"}
	}
	return reads, writes
}
