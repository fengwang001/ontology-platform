package scrape_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/head"
	"ontology/query"
	"ontology/scrape"
)

func setup(t *testing.T, l, ooo int64, smax, lim int) (*head.Head, *query.Querier, *scrape.Scraper) {
	t.Helper()
	h, err := head.NewHead(l, ooo, smax, lim)
	if err != nil {
		t.Fatal(err)
	}
	return h, query.New(h), scrape.New(h)
}

func okFetch(m map[string]int64) func() (map[string]int64, error) {
	return func() (map[string]int64, error) { return m, nil }
}

func errFetch(err error) func() (map[string]int64, error) {
	return func() (map[string]int64, error) { return nil, err }
}

func wantInstant(t *testing.T, q *query.Querier, series string, ts int64, want query.Result) {
	t.Helper()
	got := q.Instant(series, ts)
	t.Logf("输入 Instant(%s,%d) 输出 %+v 期望 %+v", series, ts, got, want)
	if got != want {
		t.Fatalf("Instant(%s,%d): 期望 %+v，得到 %+v", series, ts, want, got)
	}
}

// TestWalkthrough 逐步走查题目示例：L=300、Ooo=100、Lim=2。
func TestWalkthrough(t *testing.T) {
	_, q, s := setup(t, 300, 100, 100, 2)
	r, err := s.Scrape(1000, "t", okFetch(map[string]int64{"a": 5, "b": 7}))
	if err != nil || r != (scrape.Result{Up: 1, Reason: scrape.Ok}) {
		t.Fatalf("Scrape(1000): %+v err=%v", r, err)
	}
	r, _ = s.Scrape(1200, "t", okFetch(map[string]int64{"a": 6}))
	if r != (scrape.Result{Up: 1, Reason: scrape.Ok}) {
		t.Fatalf("Scrape(1200): %+v", r)
	}
	wantInstant(t, q, "t/b", 1199, query.Result{Kind: query.Value, V: 7}) // 差 199 < 300
	wantInstant(t, q, "t/b", 1200, query.Result{Kind: query.Stale})       // 消失名字写标记
	wantInstant(t, q, "t/b", 1350, query.Result{Kind: query.Stale})
	wantInstant(t, q, "t/a", 1499, query.Result{Kind: query.Value, V: 6}) // 差 299 < 300
	wantInstant(t, q, "t/a", 1500, query.Result{Kind: query.Absent})      // 差恰等 300
	r, _ = s.Scrape(1300, "t", errFetch(errors.New("boom")))
	if r != (scrape.Result{Up: 0, Reason: scrape.Error}) {
		t.Fatalf("Scrape(1300) 失败: %+v", r)
	}
	wantInstant(t, q, "t/a", 1300, query.Result{Kind: query.Stale})  // prev={a} 写标记
	wantInstant(t, q, "t/up", 1300, query.Result{Kind: query.Value}) // up=0
	r, _ = s.Scrape(1400, "t", okFetch(map[string]int64{"a": 1, "b": 2, "c": 3}))
	if r != (scrape.Result{Up: 0, Reason: scrape.Limit}) {
		t.Fatalf("Scrape(1400) 超限: %+v", r)
	}
	r, _ = s.Scrape(1500, "t", okFetch(map[string]int64{"a": 9, "b": 1}))
	if r != (scrape.Result{Up: 1, Reason: scrape.Ok}) {
		t.Fatalf("Scrape(1500): %+v", r)
	}
	wantInstant(t, q, "t/b", 1500, query.Result{Kind: query.Value, V: 1})
	t.Logf("判定依据: 失败写 prev 标记+up=0 且 prev 清空；超限按失败处理；up 每个被接受时刻恰一个样本")
}

// TestUpSeriesExactlyOneSample 校验 up 序列在每个被接受时刻恰有一个样本。
func TestUpSeriesExactlyOneSample(t *testing.T) {
	h, _, s := setup(t, 300, 100, 100, 2)
	_, _ = s.Scrape(100, "t", okFetch(map[string]int64{"a": 1}))
	_, _ = s.Scrape(200, "t", errFetch(errors.New("x")))
	_, _ = s.Scrape(300, "t", okFetch(map[string]int64{"b": 2}))
	up := h.Snapshot()["t/up"]
	if len(up) != 3 {
		t.Fatalf("up 样本数: 期望 3，得到 %d (%v)", len(up), up)
	}
	for i, want := range []int64{1, 0, 1} {
		if up[i].Ts != int64((i+1)*100) || up[i].V != want || up[i].Stale {
			t.Fatalf("up[%d]: 期望 ts=%d v=%d，得到 %+v", i, (i+1)*100, want, up[i])
		}
	}
	t.Logf("输入: 100 成功 / 200 失败 / 300 成功；输出 up=%v；依据: 成功 1 失败 0", up)
}

// TestPrevalidationAtomic 预校验失败：整个抓取不写，lastNow 不变。
func TestPrevalidationAtomic(t *testing.T) {
	h, q, s := setup(t, 300, 100, 100, 2)
	if err := h.Append("t2/a", 5000, 1); err != nil {
		t.Fatal(err)
	}
	_, err := s.Scrape(4000, "t2", okFetch(map[string]int64{"a": 1}))
	if err != head.ErrTooOld {
		t.Fatalf("t2/a@4000 过旧: 期望 ErrTooOld，得到 %v", err)
	}
	wantInstant(t, q, "t2/up", 5000, query.Result{Kind: query.Absent}) // up 未写
	wantInstant(t, q, "t2/a", 5000, query.Result{Kind: query.Value, V: 1})
	// lastNow 未推进：4000 仍可使用（不是时钟回退）。
	r, err := s.Scrape(4000, "t2", okFetch(map[string]int64{"b": 2}))
	if err != nil || r.Up != 1 {
		t.Fatalf("lastNow 应不变, Scrape(4000) 应成功: %+v err=%v", r, err)
	}
	wantInstant(t, q, "t2/up", 4000, query.Result{Kind: query.Value, V: 1})
	t.Logf("判定依据: 任一项预校验失败则整批不写、prev 与上次 now 都不变，返回该 head 错误")
}

// TestScrapeInstantManyAtomic 并发抓取时，InstantMany 看到的一次 Scrape
// 要么全可见要么全不可见：同批写入的 a、b 值相等，up 与它们同生同现。
func TestScrapeInstantManyAtomic(t *testing.T) {
	_, q, s := setup(t, 1e9, 0, 1000, 10)
	var done atomic.Bool
	var scraperWg, readerWg sync.WaitGroup
	for w := 0; w < 4; w++ {
		target := fmt.Sprintf("t%d", w)
		scraperWg.Add(1)
		go func() {
			defer scraperWg.Done()
			for i := int64(1); i <= 300; i++ {
				now := i
				if _, err := s.Scrape(now, target, func() (map[string]int64, error) {
					return map[string]int64{"a": now, "b": now}, nil
				}); err != nil {
					t.Errorf("Scrape(%s,%d): %v", target, now, err)
					return
				}
			}
		}()
	}
	var checks atomic.Int64
	for r := 0; r < 3; r++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			names := []string{"t0/a", "t0/b", "t0/up"}
			for !done.Load() {
				res := q.InstantMany(names, 300)
				kinds := [3]query.Kind{res[0].Kind, res[1].Kind, res[2].Kind}
				if kinds[0] != kinds[1] || kinds[1] != kinds[2] {
					t.Errorf("批次被撕裂: kinds=%v res=%v", kinds, res)
					return
				}
				if kinds[0] == query.Value && res[0].V != res[1].V {
					t.Errorf("同批样本值不一致: a=%d b=%d", res[0].V, res[1].V)
					return
				}
				checks.Add(1)
			}
		}()
	}
	scraperWg.Wait()
	done.Store(true)
	readerWg.Wait()
	t.Logf("输入: 4 个 target 各 300 次抓取 + 3 个并发读者；输出: %d 次读取全部一致；依据: 批量提交对读者原子", checks.Load())
}

// TestSameTargetSerialized 同一 target 并发 Scrape 被串行化：
// 每次调用要么成功，要么因 now 被超越而时钟回退，无其他错误。
func TestSameTargetSerialized(t *testing.T) {
	_, q, s := setup(t, 1e9, 0, 1000, 10)
	var counter, oks, regressions atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				now := counter.Add(1)
				_, err := s.Scrape(now, "shared", func() (map[string]int64, error) {
					return map[string]int64{"a": now}, nil
				})
				switch err {
				case nil:
					oks.Add(1)
				case scrape.ErrClockRegression:
					regressions.Add(1)
				default:
					t.Errorf("意外错误: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if oks.Load() == 0 {
		t.Fatal("至少应有一次抓取成功")
	}
	if got := q.Instant("shared/up", counter.Load()); got.Kind != query.Value {
		t.Fatalf("up 序列应可读: %+v", got)
	}
	t.Logf("输出: 成功 %d 次，时钟回退 %d 次；依据: 同一 target 的 Scrape 串行，now 须严格递增", oks.Load(), regressions.Load())
}
