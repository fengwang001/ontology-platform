package api

import (
	"errors"
	"maps"
	"math/rand"
	"sync"
	"testing"

	"ontology/win"
)

// batch 批量重算：每事件落入全部 size/hop 个重叠窗口，按 (Key,窗口) 分组计数。
func batch(evs []Event, size, hop int64) map[Key]int64 {
	v := map[Key]int64{}
	for _, e := range evs {
		for _, w := range win.Windows(e.TS, size, hop) {
			v[Key{Key: e.Key, Start: w.Start, End: w.End}]++
		}
	}
	return v
}

func ev(k string, ts int64) Event { return Event{Key: k, TS: ts} }

type cfg struct {
	size, hop, delay int64
	keys, n          int
	seed             int64
}

var cases = []cfg{{8, 4, 2, 1, 8, 1}, {10, 5, 0, 4, 500, 3}, {6, 2, 7, 2, 300, 4}, {12, 4, 100, 5, 1000, 5}}

func feedAll(t *testing.T, c cfg) (*Engine, []Change, []Event) {
	eng, _ := New(c.size, c.hop, c.delay)
	r := rand.New(rand.NewSource(c.seed))
	evs, ts := make([]Event, c.n), int64(-10)
	for i := range evs {
		ts += r.Int63n(5)
		evs[i] = ev(string(rune('a'+r.Intn(c.keys))), ts)
	}
	var log []Change
	for rest := evs; len(rest) > 0; {
		n := min(1+r.Intn(7), len(rest))
		cs, err := eng.Feed(rest[:n])
		if err != nil {
			t.Fatalf("cfg %+v: %v", c, err)
		}
		log = append(log, cs...)
		rest = rest[n:]
	}
	return eng, append(log, eng.Flush()...), evs
}

func scanLog(t *testing.T, c cfg, log []Change) (map[Key]int64, bool) {
	seen, prev, ordered := map[Key]int64{}, int64(-1<<62), true
	for _, ch := range log {
		k := Key{Key: ch.Key, Start: ch.Start, End: ch.End}
		if _, dup := seen[k]; dup {
			t.Fatalf("cfg %+v: 窗口 %+v 输出多条 +", c, k)
		}
		seen[k], ordered, prev = ch.Count, ordered && ch.End >= prev, ch.End
	}
	return seen, ordered
}

func TestViewMatchesBatch(t *testing.T) { // 不变量1
	for _, c := range cases {
		eng, _, evs := feedAll(t, c)
		if !maps.Equal(eng.View(), batch(evs, c.size, c.hop)) {
			t.Errorf("cfg %+v: 视图与批量重算不一致", c)
		}
	}
}

func TestChangelogExactlyOnce(t *testing.T) { // 不变量2
	for _, c := range cases {
		_, log, evs := feedAll(t, c)
		if seen, _ := scanLog(t, c, log); !maps.Equal(seen, batch(evs, c.size, c.hop)) {
			t.Errorf("cfg %+v: 变更日志计数 != 最终计数", c)
		}
	}
}

func TestTriggerOrderByEnd(t *testing.T) { // 不变量3
	for _, c := range cases {
		_, log, _ := feedAll(t, c)
		if _, ordered := scanLog(t, c, log); !ordered {
			t.Errorf("cfg %+v: 触发顺序非 end 升序", c)
		}
	}
}

func TestSentinelErrors(t *testing.T) { // 不变量4 之可判定错误
	bad := []struct{ s, h, d int64 }{{0, 4, 0}, {-1, 4, 0}, {8, 0, 0}, {8, -1, 0}, {4, 8, 0}, {8, 3, 0}}
	for _, b := range bad {
		if _, err := New(b.s, b.h, b.d); !errors.Is(err, ErrParam) {
			t.Errorf("New(%d,%d,%d) err=%v, want ErrParam", b.s, b.h, b.d, err)
		}
	}
	eng, _ := New(8, 4, 0)
	_, errD := New(8, 4, -1)
	_, errO := eng.Feed([]Event{ev("a", 5), ev("a", 4)})
	if !errors.Is(errD, ErrDelay) || !errors.Is(errO, ErrOrder) {
		t.Errorf("ErrDelay/ErrOrder 判定失败: %v, %v", errD, errO)
	}
	if ErrParam == ErrDelay || ErrDelay == ErrOrder || ErrParam == ErrOrder {
		t.Fatal("三类哨兵错误必须互不相同")
	}
}

func TestRejectLeavesNoTrace(t *testing.T) { // 不变量4 之失败不留痕
	eng, _ := New(8, 4, 2)
	good := []Event{ev("a", 6), ev("a", 10)}
	if _, err := eng.Feed(good); err != nil {
		t.Fatal(err)
	}
	before := eng.View()
	for _, bb := range [][]Event{{ev("a", 9)}, {ev("a", 20), ev("a", 15)}, {ev("b", 100), ev("a", 3)}} {
		if _, err := eng.Feed(bb); !errors.Is(err, ErrOrder) || !maps.Equal(eng.View(), before) {
			t.Errorf("被拒批次 %v 未报 ErrOrder 或状态被改变", bb)
		}
	}
	if _, err := eng.Feed([]Event{ev("a", 30)}); err != nil { // 之后仍可正常使用
		t.Fatal(err)
	}
}

func TestConcurrentView(t *testing.T) { // 并发只读：视图逐字段相同，无 sleep
	eng, _, _ := feedAll(t, cases[3])
	want := eng.View()
	var wg sync.WaitGroup
	ok := make(chan bool, 160)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				ok <- maps.Equal(eng.View(), want) && eng.SelfCheck() == nil
			}
		}()
	}
	wg.Wait()
	close(ok)
	for b := range ok {
		if !b {
			t.Error("并发读到的视图不一致")
		}
	}
}
