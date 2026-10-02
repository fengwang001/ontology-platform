package tablespace

import (
	"sync"
	"testing"
)

// TestConcurrentExercises 多 goroutine 并发调用全部操作与查询：
// 验证不存在数据竞争；串行化执行下最终状态仍满足全部不变量，
// 且分配出去的页互不重叠（任何时刻每页至多归属一个段）。
func TestConcurrentExercises(t *testing.T) {
	const (
		goroutines = 8
		rounds     = 400
	)
	a, err := New(16, 6, 40)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			s := a.NewSegment()
			var held []int
			rng := randLocal(uint64(g + 1))
			for r := 0; r < rounds; r++ {
				switch rng.IntN(10) {
				case 0, 1, 2, 3, 4:
					hint := -1
					if rng.IntN(2) == 0 && len(held) > 0 {
						hint = held[rng.IntN(len(held))]
					}
					p, err := a.AllocPage(s, hint)
					if err == nil {
						held = append(held, p)
					}
				case 5, 6:
					if len(held) == 0 {
						continue
					}
					idx := rng.IntN(len(held))
					p := held[idx]
					if err := a.FreePage(s, p); err == nil {
						held = append(held[:idx], held[idx+1:]...)
					}
				case 7:
					_, _ = a.Used(s)
				case 8:
					_ = a.ExtentStates()
				default:
					p := rng.IntN(40 * 16)
					_, _ = a.PageOwner(p)
				}
			}
			// 结束前归还全部页。
			for _, p := range held {
				if err := a.FreePage(s, p); err != nil {
					t.Errorf("final FreePage(%d,%d): %v", s, p, err)
				}
			}
			if err := a.FreeSegment(s); err != nil {
				t.Errorf("final FreeSegment(%d): %v", s, err)
			}
		}(g)
	}
	wg.Wait()

	// 所有段释放后，全部区段恢复 FREE，无任何页归属。
	for _, st := range a.ExtentStates() {
		if st.State != StateFree || st.Used != 0 || st.Owner != 0 {
			t.Fatalf("extent %d not fully free: %+v", st.Index, st)
		}
	}
	for p := 0; p < 40*16; p++ {
		owner, err := a.PageOwner(p)
		if err != nil || owner != 0 {
			t.Fatalf("page %d owner=%d err=%v after cleanup", p, owner, err)
		}
	}
	checkInvariants(t, a, "after concurrent run")
}

// randLocal 返回一个简单的确定性伪随机源（避免测试间共享状态）。
func randLocal(seed uint64) *rngWrap {
	return &rngWrap{state: seed*6364136223846793005 + 1442695040888963407}
}

type rngWrap struct{ state uint64 }

func (r *rngWrap) IntN(n int) int {
	r.state ^= r.state >> 12
	r.state ^= r.state << 25
	r.state ^= r.state >> 27
	v := r.state * 2685821657736338717
	return int((v >> 33) % uint64(n))
}
