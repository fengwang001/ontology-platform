package api_test

import (
	"errors"
	"fmt"
	"ontology/api"
	"slices"
	"sync"
	"testing"
)

// naiveCounts 是独立的暴力参照：逐事件按路由规则落分片。
func naiveCounts(S, T int, evs []api.Event) []int64 {
	cnt := make([]int64, S)
	hot, ded, base := map[string]bool{}, map[string]int{}, map[string]int64{}
	for _, e := range evs {
		if hot[e.Key] {
			cnt[ded[e.Key]]++
			continue
		}
		cnt[e.Base]++
		base[e.Key]++
		if base[e.Key] >= int64(T) {
			hot[e.Key] = true
			ded[e.Key] = S + len(ded)
			cnt = append(cnt, 0)
		}
	}
	return cnt
}

var specSeq = []api.Event{
	{Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "b", Base: 1},
	{Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "c", Base: 1}, {Key: "a", Base: 0},
}

func bigSeq() []api.Event {
	var evs []api.Event
	for i := 0; i < 200; i++ {
		for j := 0; j <= i%9; j++ {
			evs = append(evs, api.Event{Key: fmt.Sprintf("k%d", i), Base: i % 4})
		}
	}
	return evs
}

// TestCountsInvariant 钉住不变量 1（逐事件总量守恒）与 2（与朴素参照一致）。
func TestCountsInvariant(t *testing.T) {
	names := []string{"spec", "many-keys", "single-shard"}
	Ss := []int{2, 4, 1}
	Ts := []int{3, 5, 2}
	evss := [][]api.Event{specSeq, bigSeq(), {{Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "a", Base: 0}}}
	for i := range names {
		sys, _ := api.New(Ss[i], Ts[i])
		for j, e := range evss[i] {
			if err := sys.Feed([]api.Event{e}); err != nil {
				t.Fatal(err)
			}
			var got int64
			for _, c := range sys.Counts() {
				got += c
			}
			if got != int64(j+1) {
				t.Fatalf("%s: after %d events sum=%d", names[i], j+1, got)
			}
		}
		if got, want := sys.Counts(), naiveCounts(Ss[i], Ts[i], evss[i]); !slices.Equal(got, want) {
			t.Fatalf("%s: counts=%v want=%v", names[i], got, want)
		}
	}
}

// TestMigrationAtomicity 钉住不变量 3：触发事件落基础分片，之后全落专属分片。
func TestMigrationAtomicity(t *testing.T) {
	sys, _ := api.New(2, 3)
	if err := sys.Feed(specSeq); err != nil {
		t.Fatal(err)
	}
	if got := sys.Counts(); !slices.Equal(got, []int64{3, 2, 3}) {
		t.Fatalf("counts=%v want [3 2 3]", got)
	}
	d, ok := sys.Dedicated("a")
	if _, bMig := sys.Dedicated("b"); !ok || d != 2 || bMig || !sys.IsHot("a") || sys.IsHot("c") {
		t.Fatal("migration state wrong")
	}
}

// TestRejectNoTrace 钉住不变量 4：四类可判定错误互不相同，被拒后状态不变且可继续用。
func TestRejectNoTrace(t *testing.T) {
	if len(map[error]int{api.ErrInvalidS: 1, api.ErrInvalidT: 2, api.ErrBaseOutOfRange: 3, api.ErrEmptyKey: 4}) != 4 {
		t.Fatal("sentinels not distinct")
	}
	for i, c := range [][2]int{{0, 1}, {1, 0}} {
		if _, err := api.New(c[0], c[1]); !errors.Is(err, []error{api.ErrInvalidS, api.ErrInvalidT}[i]) {
			t.Fatalf("New%v err=%v", c, err)
		}
	}
	sys, _ := api.New(2, 2)
	if err := sys.Feed([]api.Event{{Key: "a", Base: 0}, {Key: "a", Base: 0}}); err != nil {
		t.Fatal(err)
	}
	before := sys.Counts()
	bad := []struct {
		evs  []api.Event
		want error
	}{
		{[]api.Event{{Key: "x", Base: 2}}, api.ErrBaseOutOfRange},
		{[]api.Event{{Key: "x", Base: -1}}, api.ErrBaseOutOfRange},
		{[]api.Event{{Key: "", Base: 0}}, api.ErrEmptyKey},
		{[]api.Event{{Key: "ok", Base: 1}, {Key: "bad", Base: 9}}, api.ErrBaseOutOfRange}, // 部分非法整批拒
	}
	for _, c := range bad {
		if err := sys.Feed(c.evs); !errors.Is(err, c.want) || !slices.Equal(sys.Counts(), before) {
			t.Fatalf("reject %v failed: err or state wrong", c.want)
		}
	}
	if err := sys.Feed([]api.Event{{Key: "b", Base: 1}}); err != nil || sys.Counts()[1] != 1 {
		t.Fatal("system unusable after rejects")
	}
}

// TestConcurrentReads 钉住并发只读：N 个 goroutine 的 Counts 逐字段相同。不用 sleep。
func TestConcurrentReads(t *testing.T) {
	sys, _ := api.New(4, 5)
	if err := sys.Feed(bigSeq()); err != nil {
		t.Fatal(err)
	}
	want := sys.Counts()
	var wg sync.WaitGroup
	bad := make(chan struct{}, 32)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, ok := sys.Dedicated("k4")
				if !ok || !slices.Equal(sys.Counts(), want) || !sys.IsHot("k4") || sys.SelfCheck() != nil {
					bad <- struct{}{}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	if len(bad) > 0 {
		t.Fatal("inconsistent concurrent reads")
	}
}
