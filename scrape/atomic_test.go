package scrape_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/query"
)

// 并发抓取与 InstantMany 的原子性：一次 Scrape 的写集对读者要么全可见要么
// 全不可见。每个 target 的第 k 次抓取在 ts=now 写入 v=now、w=-now、up=1，
// 读者在任意时刻读到的 v+w 必为 0, 且 v 可见时 up 必为 1。
func TestConcurrentScrapeAtomicVisibility(t *testing.T) {
	_, q, s := newEnv(t, 1e9, 0, 1e6, 10)
	const targets = 8
	const scrapes = 200
	var producers, readers sync.WaitGroup
	for g := 0; g < targets; g++ {
		target := fmt.Sprintf("tg%d", g)
		producers.Add(1)
		go func() {
			defer producers.Done()
			for k := 1; k <= scrapes; k++ {
				now := int64(k * 10)
				fetch := func() (map[string]int64, error) {
					return map[string]int64{"v": now, "w": -now}, nil
				}
				if _, err := s.Scrape(now, target, fetch); err != nil {
					t.Errorf("Scrape(%d,%q) err = %v", now, target, err)
					return
				}
			}
		}()
	}
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func(r int) {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for g := 0; g < targets; g++ {
					target := fmt.Sprintf("tg%d", g)
					names := []string{target + "/v", target + "/w", target + "/up"}
					for _, ts := range []int64{500, 1000, 2000} {
						res := q.InstantMany(names, ts)
						v, w, up := res[0], res[1], res[2]
						if v.Kind != w.Kind || (v.Kind == query.Value && v.V+w.V != 0) {
							t.Errorf("非原子读: %q t=%d v=%v w=%v", target, ts, v, w)
							return
						}
						if v.Kind == query.Value && (up.Kind != query.Value || up.V != 1) {
							t.Errorf("up 与样本不一致: %q t=%d v=%v up=%v", target, ts, v, up)
							return
						}
					}
				}
			}
		}(r)
	}
	producers.Wait()
	close(stop)
	readers.Wait()
}

// 同一 target 的 Scrape 串行：并发提交乱序 now, 只有严格递增的被接受。
func TestSameTargetSerialized(t *testing.T) {
	_, q, s := newEnv(t, 1e9, 0, 100, 10)
	var wg sync.WaitGroup
	for k := 1; k <= 50; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			_, _ = s.Scrape(int64(k), "t", fetchOK(map[string]int64{"a": int64(k)}))
		}(k)
	}
	wg.Wait()
	// now=50 是最大值, 无论串行顺序如何必被接受, 且之后无更大 now。
	got := q.Instant("t/a", 50)
	if got != (query.Result{Kind: query.Value, V: 50}) {
		t.Fatalf("Instant(t/a,50) = %v, 期望 Value 50", got)
	}
	t.Logf("判定: 同一 target 串行化, t/a 在 t=50 读到 Value %d", got.V)
}
