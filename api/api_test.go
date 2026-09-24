package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/win"
	"ontology/wmgr"
)

// 第三节七步序列（T=10, id=1），逐步核验 agg/trg/state 与触发器输出。
func TestSevenStepSequence(t *testing.T) {
	steps := []struct {
		op           string
		val          int64
		agg, trg     int64
		st           win.State
		fired, exist bool
	}{
		{"ingest", 7, 7, 0, win.Active, false, true},
		{"ingest", 5, 12, 1, win.Active, true, true},
		{"purge", 0, 12, 1, win.Purged, false, true},
		{"late", 3, 3, 1, win.Revived, false, true},
		{"late", 8, 11, 1, win.Revived, false, true},
		{"ingest", 2, 13, 1, win.Revived, false, true},
		{"gc", 0, 13, 1, win.Revived, false, true},
	}
	e := api.New(10, 100)
	for i, s := range steps {
		before := e.Triggers()
		switch s.op {
		case "ingest":
			e.Ingest("1", s.val)
		case "late":
			e.Late("1", s.val)
		case "purge":
			e.Purge("1")
		case "gc":
			e.GC()
		}
		agg, trg, st, ok := e.Snapshot("1")
		fired := e.Triggers() - before
		wantFired := int64(0)
		if s.fired {
			wantFired = 1
		}
		if ok != s.exist || agg != s.agg || trg != s.trg || st != s.st || fired != wantFired {
			t.Errorf("步%d(%s): got agg=%d trg=%d %v exist=%v fired=%d", i+1, s.op, agg, trg, st, ok, fired)
		}
	}
}

// 不变量 1：复活只服务一条迟到事件，丢弃冻结历史。多档冻结值表驱动。
func TestReviveServesSingleLate(t *testing.T) {
	for _, c := range []struct{ frozen, late int64 }{{12, 3}, {50, 1}, {100, 9}} {
		e := api.New(10, 100)
		e.Ingest("w", c.frozen)
		e.Purge("w")
		e.Late("w", c.late)
		if agg, _, _, _ := e.Snapshot("w"); agg != c.late {
			t.Errorf("冻结=%d 迟到=%d: agg=%d, 期望 %d", c.frozen, c.late, agg, c.late)
		}
	}
}

// 不变量 2：revived 窗口后续 Ingest/Late 交错都不再触发。
func TestRevivedSuppressesTriggers(t *testing.T) {
	e := api.New(10, 100)
	e.Ingest("w", 12) // trg=1
	e.Purge("w")
	e.Late("w", 1)
	for i := 0; i < 20; i++ { // 随机交错：奇偶交替 Ingest/Late
		if i%2 == 0 {
			e.Ingest("w", 5)
		} else {
			e.Late("w", 5)
		}
	}
	if _, trg, _, _ := e.Snapshot("w"); trg != 1 || e.Triggers() != 1 {
		t.Errorf("revived 仍触发: trg=%d total=%d", trg, e.Triggers())
	}
}

// 不变量 3：GC 删 purged 未复活、保留 revived 与 active。
func TestGCKeepsRevived(t *testing.T) {
	e := api.New(10, 100)
	e.Ingest("p", 1)
	e.Purge("p")
	e.Ingest("r", 1)
	e.Purge("r")
	e.Late("r", 2)
	e.Ingest("a", 1)
	e.GC()
	cases := []struct{ id, want string }{{"p,删除", ""}, {"r,保留", "revived"}, {"a,保留", "active"}}
	for _, c := range cases {
		id := c.id[:1]
		_, _, st, ok := e.Snapshot(id)
		if c.want == "" && ok {
			t.Errorf("%s: 应被 GC 删除", c.id)
		}
		if c.want != "" && (!ok || st.String() != c.want) {
			t.Errorf("%s: got ok=%v state=%v", c.id, ok, st)
		}
	}
}

// 不变量 4：四类错误互不相同，被拒后状态不变且可继续用。
func TestErrorsDistinctAndNoTrace(t *testing.T) {
	e := api.New(10, 1)
	e.Ingest("a", 5)
	got := []error{e.Ingest("", 1), e.Ingest("a", 0), e.Late("ghost", 1), e.Ingest("b", 1)}
	want := []error{wmgr.ErrBadID, wmgr.ErrBadVal, wmgr.ErrNoSuchWindow, wmgr.ErrTooManyWindows}
	for i := range got {
		if !errors.Is(got[i], want[i]) {
			t.Errorf("错误%d: got %v, want %v", i, got[i], want[i])
		}
		for j := range want {
			if i != j && errors.Is(got[i], want[j]) {
				t.Errorf("错误%d 与错误%d 不可区分", i, j)
			}
		}
	}
	if agg, _, _, _ := e.Snapshot("a"); agg != 5 {
		t.Errorf("被拒操作改变了状态: agg=%d", agg)
	}
	if err := e.Ingest("a", 1); err != nil {
		t.Errorf("被拒后不可继续用: %v", err)
	}
}

// 并发：N 个 goroutine 各 Ingest 一条，agg 为总和、trg 为越过次数。
func TestConcurrentIngest(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		e := api.New(10, 1000)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; e.Ingest("c", 1) }()
		}
		close(start)
		wg.Wait()
		agg, trg, _, _ := e.Snapshot("c")
		if agg != int64(n) || trg != 1 {
			t.Errorf("n=%d: agg=%d trg=%d, 期望 agg=%d trg=1", n, agg, trg, n)
		}
	}
}

// 并发：GC 与 Late 并发，每个 purged 窗口最终要么被删要么复活，不并存。
func TestConcurrentGCLate(t *testing.T) {
	const k = 64
	e := api.New(10, 10000)
	for i := 0; i < k; i++ {
		id := fmt.Sprintf("w%d", i)
		e.Ingest(id, 1)
		e.Purge(id)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < k; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); <-start; e.Late(fmt.Sprintf("w%d", i), 5) }(i)
		go func() { defer wg.Done(); <-start; e.GC() }()
	}
	close(start)
	wg.Wait()
	for i := 0; i < k; i++ {
		_, _, st, ok := e.Snapshot(fmt.Sprintf("w%d", i))
		if ok && st != win.Revived {
			t.Errorf("w%d: 存在但非 revived(%v)，删与复活并存", i, st)
		}
	}
}
