package eliminator

import (
	"fmt"
	"sync"
	"testing"
)

// 并发压力：多 goroutine 混合调用，任何串行化都必须维持核心不变量。
func TestConcurrentOperations(t *testing.T) {
	e, err := New(16, 2, 5)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for k := 0; k < 3000; k++ {
				r, err := e.Next()
				if err == nil {
					if (k+seed)%3 == 0 {
						_ = e.Click(r.Arm)
					}
					continue
				}
				if reasonOf(err) != ReasonFinished {
					t.Errorf("意外错误: %v", err)
					return
				}
				// 结束后：Click 仍可记账，Join/Next 被拒。
				_ = e.Click((k + seed) % 16)
				_ = e.Join(900 + seed)
				return
			}
		}(w)
	}

	// 单独的 Join/Click 噪声 goroutine。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for id := 100; id < 148; id++ {
			if err := e.Join(id); reasonOf(err) == ReasonInvalidArgs {
				return
			}
		}
	}()
	wg.Wait()

	s := e.Snapshot()
	if !s.Finished || s.Winner < 0 {
		t.Fatalf("并发结束后应有唯一胜者: done=%v winner=%d", s.Finished, s.Winner)
	}
	var sumST int64
	alive := 0
	for _, a := range s.Arms {
		if a.Clicks > a.Exposures {
			t.Fatalf("cT>sT: %+v", a)
		}
		sumST += a.Exposures
		if !a.Eliminated {
			alive++
		}
	}
	if sumST != s.TotalNext {
		t.Fatalf("曝光总和%d != Next成功次数%d", sumST, s.TotalNext)
	}
	if alive != 1 {
		t.Fatalf("结束时活跃创意数=%d, 期望1", alive)
	}
}

// 确定性：相同操作序列在两个实例上重放，结果逐字节一致。
func TestDeterministicReplay(t *testing.T) {
	run := func() (seq []int, events []Elimination, winner int) {
		e, _ := New(5, 2, 4)
		steps := []struct {
			act string
			id  int
		}{
			{"n", 0}, {"n", 0}, {"c", 1}, {"n", 0}, {"c", 0},
			{"n", 0}, {"c", 2}, {"j", 50}, {"c", 0}, {"n", 0},
			{"c", 3}, {"n", 0}, {"c", 50}, {"n", 0}, {"n", 0},
		}
		for _, st := range steps {
			switch st.act {
			case "n":
				r, err := e.Next()
				if err != nil {
					return
				}
				seq = append(seq, r.Arm)
				events = append(events, r.Eliminated...)
			case "c":
				_ = e.Click(st.id)
			case "j":
				_ = e.Join(st.id)
			}
		}
		for {
			r, err := e.Next()
			if err != nil {
				break
			}
			seq = append(seq, r.Arm)
			events = append(events, r.Eliminated...)
			if r.Winner != -1 {
				winner = r.Winner
				break
			}
		}
		return
	}
	s1, ev1, w1 := run()
	s2, ev2, w2 := run()
	if fmt.Sprint(s1) != fmt.Sprint(s2) ||
		fmt.Sprint(ev1) != fmt.Sprint(ev2) || w1 != w2 {
		t.Fatalf("重放不一致:\n%v %v %d\n%v %v %d", s1, ev1, w1, s2, ev2, w2)
	}
	t.Logf("复现结果: 分流=%v 淘汰=%v 胜者=%d", s1, ev1, w1)
}
