package repl_test

import (
	"sync"
	"testing"

	"ontology/link"
	"ontology/repl"
)

// TestConcurrentSerialEquivalence 并发混合调用：
// 结果必须等价于某个串行顺序——不变量恒成立、无数据竞争（配合 -race）。
func TestConcurrentSerialEquivalence(t *testing.T) {
	sys, err := repl.New(repl.Params{
		Regions: []string{"A", "B", "C"}, C: 50, R: 3,
		Mode: repl.Strict, MarkerRepl: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rs := sys.Regions()
	send := func(item link.Item) (bool, error) {
		// 确定性地让一部分调用失败，覆盖重试/转移路径。
		if item.Ver.Size%3 == 0 && item.Tries < 2 {
			return false, link.FailError{}
		}
		return true, nil
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r := rs[(w+i)%len(rs)]
				switch i % 4 {
				case 0:
					_, _ = sys.Put(r, "k", int64(1+(w+i)%20), int64(i%7))
				case 1:
					_, _ = sys.Delete(r, "k", int64(i%7))
				case 2:
					dst := rs[(w+i+1)%len(rs)]
					_, _ = sys.Deliver(r, dst, 5, send)
				case 3:
					snap := sys.Snapshot()
					for k, l := range snap.Links {
						if l.Stats.Enqueued != l.Stats.Delivered+l.Stats.Transfers+l.Stats.Queued {
							t.Errorf("invariant %v: %+v", k, l.Stats)
							return
						}
						var sum int64
						for _, q := range l.Queue {
							sum += q.Ver.Size
						}
						if sum != l.Stats.Backlog {
							t.Errorf("backlog bytes %v", k)
							return
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// 全部在途项推进完（必要时含失败重试）后，无故障残留：
	// 反复 Deliver + Retry 直到队列与失败列表清空。
	for progress := true; progress; {
		progress = false
		for _, src := range rs {
			for _, dst := range rs {
				if src == dst {
					continue
				}
				n, _ := sys.Deliver(src, dst, 1000, okSend)
				if n > 0 {
					progress = true
				}
				for _, f := range sys.Snapshot().Links[pair(src, dst)].Failed {
					if err := sys.Retry(src, dst, f.ID); err == nil {
						progress = true
					}
				}
			}
		}
	}
	for k, l := range sys.Snapshot().Links {
		if l.Stats.Queued != 0 || len(l.Failed) != 0 {
			t.Fatalf("link %v not drained: queued=%d failed=%d", k, l.Stats.Queued, len(l.Failed))
		}
		if l.Stats.Enqueued != l.Stats.Delivered+l.Stats.Transfers {
			t.Fatalf("final invariant %v: %+v", k, l.Stats)
		}
	}
}

func pair(a, b string) [2]string { return [2]string{a, b} }
