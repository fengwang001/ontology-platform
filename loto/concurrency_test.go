package loto

import (
	"math/rand"
	"sync"
	"testing"
)

// 所有操作可并发调用：内部串行化，结果等价于某个串行顺序。
// 配合 go test -race 验证无数据竞争；结束后校验系统内部一致性。
func TestConcurrentOps(t *testing.T) {
	s := newTestSystem(t)
	const goroutines = 8
	const opsEach = 300

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			people := []string{"alice", "bob", "carol", "dave", "erin", "frank", "grace", "henry"}
			devices := []string{"D1", "D2", "D3"}
			points := []string{"P1", "P2", "P3", "P4"}
			var clock int64
			for i := 0; i < opsEach; i++ {
				clock += int64(r.Intn(3))
				pid := 1 + r.Intn(40)
				person := people[r.Intn(len(people))]
				switch r.Intn(13) {
				case 0:
					start := clock + int64(r.Intn(10))
					_, _ = s.Apply(clock, person, []string{devices[r.Intn(3)]}, WorkType(r.Intn(3)), start, start+1+int64(r.Intn(20)))
				case 1:
					_ = s.Approve(clock, person, pid)
				case 2:
					_ = s.AddWorker(clock, person, pid, people[r.Intn(len(people))])
				case 3:
					_ = s.Lock(clock, pid, person, points[r.Intn(4)])
				case 4:
					_ = s.Verify(clock, pid, person)
				case 5:
					_ = s.Start(clock, pid, person)
				case 6:
					_ = s.Enter(clock, pid, person)
				case 7:
					_ = s.Leave(clock, pid, person)
				case 8:
					_ = s.Complete(clock, pid, person)
				case 9:
					_ = s.Unlock(clock, pid, person, points[r.Intn(4)])
				case 10:
					_ = s.TrialBegin(clock, pid, person)
				case 11:
					_ = s.TrialEnd(clock, pid, person)
				case 12:
					_ = s.ForceUnlock(clock, pid, person, people[r.Intn(len(people))], people[r.Intn(len(people))], points[r.Intn(4)], "理由")
				}
				// 只读查询可随意并发
				_, _ = s.Energizable(devices[r.Intn(3)])
			}
		}(int64(g) * 977)
	}
	wg.Wait()

	// 一致性：锁计数不为负，送电判定可执行且理由自洽
	s.mu.Lock()
	for pt, c := range s.pointLocks {
		if c < 0 {
			t.Fatalf("隔离点 %s 锁计数为负: %d", pt, c)
		}
	}
	for d, c := range s.deviceLocks {
		if c < 0 {
			t.Fatalf("设备 %s 锁计数为负: %d", d, c)
		}
	}
	s.mu.Unlock()
	for _, d := range []string{"D1", "D2", "D3"} {
		dec, err := s.Energizable(d)
		if err != nil {
			t.Fatalf("Energizable(%s): %v", d, err)
		}
		if dec.OK && (dec.Locks != 0 || len(dec.Blocking) != 0) {
			t.Fatalf("判定自洽性失败: %+v", dec)
		}
	}
}
