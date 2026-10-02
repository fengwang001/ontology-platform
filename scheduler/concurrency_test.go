package scheduler

import (
	"sync"
	"testing"
)

func TestConcurrentUpdatesAndQueries(t *testing.T) {
	s, _ := New(64, 256, 50)
	for i := 0; i < 16; i++ {
		s.AddTask(1)
	}
	var failedMu sync.Mutex
	failed := false
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				u := (id + i) % 16
				v := (id*3 + i + 1) % 16
				if u != v && i%3 == 0 {
					_, _ = s.AddDep(u, v, 0)
				}
				if u != v && i%3 == 1 {
					_, _ = s.RemoveDep(u, v)
				}
				if i%3 == 2 {
					_, _ = s.SetDuration(u, int64((id+i)%4))
				}
			}
		}(worker)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				for v := 0; v < 16; v++ {
					es, err := s.ES(v)
					if err != nil || es < 0 {
						failedMu.Lock()
						failed = true
						failedMu.Unlock()
					}
				}
				if len(s.CriticalPath()) == 0 {
					failedMu.Lock()
					failed = true
					failedMu.Unlock()
				}
				if s.PF() < 0 {
					failedMu.Lock()
					failed = true
					failedMu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if failed {
		t.Fatal("concurrent readers observed invalid task state")
	}
	t.Log("输入: 8 个更新 worker 与 8 个查询 worker 并发; 输出: 无数据竞争或半更新状态; 判定依据: -race 与读取值约束")
}
