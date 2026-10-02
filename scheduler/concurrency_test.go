package scheduler

import (
	"errors"
	"sync"
	"testing"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	s := mustNew(t, 64)
	mustSpawn(t, s, 0, 0)
	var wg sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := worker*100 + i + 1
				if err := s.Spawn(id, (i%20)-10); err != nil &&
					!errors.Is(err, ErrSchedulerFull) && !errors.Is(err, ErrTaskExists) {
					t.Errorf("Spawn: %v", err)
					return
				}
				_ = s.Tick()
				if err := s.Sleep(); err != nil && !errors.Is(err, ErrNoCurrentTask) {
					t.Errorf("Sleep: %v", err)
					return
				}
				if err := s.Wake(id); err != nil &&
					!errors.Is(err, ErrTaskNotFound) && !errors.Is(err, ErrTaskNotSleeping) {
					t.Errorf("Wake: %v", err)
					return
				}
				_ = s.Fork(0, id+1000)
				_, _ = s.Current()
				_ = s.Queues()
				_ = s.ExpiredTs()
			}
		}()
	}
	wg.Wait()

	seen := make(map[int]int)
	for id := range s.tasks {
		task := s.tasks[id]
		if task.s < 0 || task.s > 1000 || task.prio < 100 || task.prio > 139 || task.tsLeft <= 0 {
			t.Fatalf("task %d has invalid fields: %+v", id, task)
		}
		seen[id]++
		if task.state == StateRunning {
			if s.cur != task {
				t.Fatalf("running task %d is not cur", id)
			}
		}
	}
	if s.cur != nil {
		if seen[s.cur.id] != 1 || s.cur.state != StateRunning {
			t.Fatalf("cur %d is inconsistent", s.cur.id)
		}
	}
	if (s.expired.count == 0) != (s.expiredTs == 0) {
		t.Fatalf("expiredTs/count mismatch: ts=%d count=%d", s.expiredTs, s.expired.count)
	}
}
