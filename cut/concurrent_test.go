package cut_test

import (
	"math/rand"
	"sync"
	"testing"

	"ontology/cut"
	"ontology/seq"
	"ontology/stride"
)

// 并发混合调用 Join/Leave/Issue/Reserve/Observe：
// 签发的编号必须全局唯一、不超过 MaxID，且单调性不被并发破坏。
func TestConcurrentMixedOps(t *testing.T) {
	st, err := seq.New(4, 4)
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 8
	const per = 300
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[int64]bool)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			last := make(map[int]int64) // 程序序：本 goroutine 内同系统签发严格递增
			for i := 0; i < per; i++ {
				sys := 1 + r.Intn(4)
				var ids []int64
				switch r.Intn(6) {
				case 0, 1, 2:
					id, err := st.Issue(sys)
					if err == nil {
						ids = []int64{id}
					}
				case 3:
					ids, _ = st.Reserve(sys, 1+r.Intn(5))
				case 4:
					_ = st.Observe(sys, int64(1+r.Intn(50)))
				case 5:
					if r.Intn(2) == 0 {
						_ = cut.Join(st, 2, sys)
					} else {
						_ = cut.Leave(st, 2, sys)
					}
				}
				if len(ids) > 0 {
					for _, id := range ids {
						if id < 1 || id > stride.MaxID {
							t.Errorf("id=%d 越界", id)
						}
						if id <= last[sys] {
							t.Errorf("系统%d 签发未严格递增: %d<=%d", sys, id, last[sys])
						}
						last[sys] = id
					}
					mu.Lock()
					for _, id := range ids {
						if seen[id] {
							t.Errorf("id=%d 被重复签发", id)
						}
						seen[id] = true
					}
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()
	if len(seen) == 0 {
		t.Fatal("并发测试未签发任何编号")
	}
	t.Logf("并发混合操作完成：%d 个 goroutine 共签发 %d 个全局唯一编号", goroutines, len(seen))
}
