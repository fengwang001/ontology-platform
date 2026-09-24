package api_test

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func TestNewValidation(t *testing.T) {
	cases := []struct {
		size, lateness int64
		want           error
	}{{1, 0, nil}, {5, 2, nil}, {0, 0, api.ErrSize}, {-3, 0, api.ErrSize}, {1, -1, api.ErrLateness}}
	for _, c := range cases {
		if _, err := api.New(c.size, c.lateness); !errors.Is(err, c.want) {
			t.Fatalf("New(%d,%d) err=%v, want %v", c.size, c.lateness, err, c.want)
		}
	}
	if len(map[error]bool{api.ErrSize: true, api.ErrLateness: true, api.ErrPos: true, api.ErrKey: true}) != 4 {
		t.Fatal("sentinel errors must be distinct")
	}
}

func TestFeedRejectedAtomic(t *testing.T) {
	eng, _ := api.New(5, 2)
	if _, err := eng.Feed([]api.Event{{"a", 0, 1}, {"a", 1, 2}}); err != nil {
		t.Fatal(err)
	}
	nf, nd := len(eng.Fired()), eng.Dropped()
	bads := []struct {
		evs  []api.Event
		want error
	}{
		{[]api.Event{{"a", -1, 0}}, api.ErrPos},
		{[]api.Event{{"", 2, 0}}, api.ErrKey},
		{[]api.Event{{"a", 2, 0}, {"b", -5, 0}}, api.ErrPos}, // 部分非法整批拒
	}
	for _, b := range bads {
		if _, err := eng.Feed(b.evs); !errors.Is(err, b.want) {
			t.Fatalf("Feed(%v) err=%v, want %v", b.evs, err, b.want)
		}
		if len(eng.Fired()) != nf || eng.Dropped() != nd {
			t.Fatal("rejected batch mutated state")
		}
	}
	if _, err := eng.Feed([]api.Event{{"a", 2, 3}}); err != nil { // 拒绝后仍可用
		t.Fatal("engine unusable after rejection")
	}
}

// 独立重算接受集合，与 Fired 逐窗口比对；多档参数与随机到达顺序用循环生成。
func TestBatchConsistency(t *testing.T) {
	type kw struct {
		k string
		w int64
	}
	for trial := 0; trial < 20; trial++ {
		size, lateness := int64(3+trial%4), int64(trial%4)
		var evs []api.Event
		for i := int64(0); i < 200; i++ { // 确定性伪随机：多 Key、乱序、迟到、重复
			evs = append(evs, api.Event{fmt.Sprint((i*7 + int64(trial)) % 4), (i*37 + 11 + int64(trial)) % 40, i%19 - 9})
		}
		eng, _ := api.New(size, lateness)
		if _, err := eng.Feed(evs); err != nil {
			t.Fatal(err)
		}
		acc, cnt, wm := map[kw]int64{}, map[kw]int64{}, map[string]int64{}
		seen := map[[2]interface{}]bool{}
		for _, ev := range evs {
			if seen[[2]interface{}{ev.Key, ev.Pos}] {
				continue
			}
			seen[[2]interface{}{ev.Key, ev.Pos}] = true
			k, w := kw{ev.Key, ev.Pos / size}, wm[ev.Key]-1
			if cnt[k] == size || (ev.Pos < w && ev.Pos < w-lateness) {
				continue
			}
			acc[k] += ev.Val
			cnt[k]++
			if ev.Pos > w {
				wm[ev.Key] = ev.Pos + 1
			}
		}
		for _, f := range eng.Fired() {
			if acc[kw{f.Key, f.Win}] != f.Sum {
				t.Fatalf("trial %d: fire %+v mismatches batch recompute", trial, f)
			}
		}
	}
}

func TestFireOnceAndLate(t *testing.T) {
	eng, _ := api.New(5, 2)
	poss := []int64{0, 1, 2, 3, 4, 5, 9, 7}
	vals := []int64{10, 20, 30, 40, 50, 60, 90, 70}
	var fires []api.Fire
	for i, p := range poss {
		out, err := eng.Feed([]api.Event{{"k", p, vals[i]}})
		if err != nil {
			t.Fatal(err)
		}
		fires = append(fires, out...)
	}
	if len(fires) != 1 || fires[0].Win != 0 || fires[0].Sum != 150 {
		t.Fatalf("fires=%+v, want single fire win0 sum=150", fires)
	}
	if _, err := eng.Feed([]api.Event{{"k", 2, 999}}); err != nil { // 重复投递幂等
		t.Fatal(err)
	}
	if got := eng.Fired(); len(got) != 1 || got[0].Sum != 150 { // 已触发窗口输出不被改变
		t.Fatalf("fired output changed: %+v", got)
	}
	if eng.Dropped() != 0 {
		t.Fatalf("dropped=%d, want 0", eng.Dropped())
	}
}

func TestConcurrentReadOnly(t *testing.T) {
	eng, _ := api.New(5, 2)
	for i := int64(0); i < 50; i++ {
		if _, err := eng.Feed([]api.Event{{"k", i, i}}); err != nil {
			t.Fatal(err)
		}
	}
	wantFired, wantDropped := eng.Fired(), eng.Dropped()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 200; r++ {
				if !slices.Equal(eng.Fired(), wantFired) || eng.Dropped() != wantDropped {
					t.Error("concurrent read mismatch")
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := eng.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
