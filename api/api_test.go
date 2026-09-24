package api_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func ch(key string, ver, val int64) api.Change { return api.Change{Key: key, Ver: ver, Val: val} }

func mustFeed(t *testing.T, a *api.API, s ...api.Change) {
	t.Helper()
	if err := a.Feed(s); err != nil {
		t.Fatal(err)
	}
}

// brute 是独立朴素参照：sn=到达序号，逐键取 Ver 最大（并列 sn 最大）者，按 Key 升序。
func brute(chgs []api.Change) []api.Record {
	type win struct{ ver, sn, val int64 }
	m := map[string]win{}
	for i, c := range chgs {
		sn := int64(i + 1)
		if w, ok := m[c.Key]; !ok || c.Ver > w.ver || (c.Ver == w.ver && sn > w.sn) {
			m[c.Key] = win{c.Ver, sn, c.Val}
		}
	}
	keys := slices.Sorted(maps.Keys(m))
	out := make([]api.Record, 0, len(keys))
	for _, k := range keys {
		out = append(out, api.Record{Key: k, Val: m[k].val})
	}
	return out
}

var seqs = map[string][]api.Change{
	"题目七步": {ch("a", 10, 100), ch("b", 5, 50), ch("a", 10, 200), ch("c", 7, 70),
		ch("a", 5, 50), ch("b", 12, 90), ch("c", 7, 77)},
	"单键并列": {ch("x", 3, 1), ch("x", 3, 2)},
	"空批":   {},
}

// TestReplayMatchesBruteForce 钉住不变量 1：Replay 等于暴力参照（含 Key 升序）。
func TestReplayMatchesBruteForce(t *testing.T) {
	for name, s := range seqs {
		a := api.New(len(s) + 1)
		mustFeed(t, a, s...)
		if got, want := a.Replay(), brute(s); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: Replay=%v, 暴力参照=%v", name, got, want)
		}
	}
}

// TestWinnerInvariantAnyTime 钉住不变量 3：逐条喂入，任意时刻 winner 都与参照一致。
func TestWinnerInvariantAnyTime(t *testing.T) {
	s := seqs["题目七步"]
	a := api.New(len(s))
	for i := range s {
		mustFeed(t, a, s[i:i+1]...)
		if got, want := a.Replay(), brute(s[:i+1]); !reflect.DeepEqual(got, want) {
			t.Fatalf("第 %d 步后 Replay=%v, 参照=%v", i+1, got, want)
		}
	}
}

// TestReplayIdempotentAndHistorySorted 钉住不变量 2：连调逐字节相同、不改状态、历史 sn 升序。
func TestReplayIdempotentAndHistorySorted(t *testing.T) {
	a := api.New(16)
	s := seqs["题目七步"]
	mustFeed(t, a, s...)
	r1, h1 := a.Replay(), a.History("a")
	if !reflect.DeepEqual(r1, a.Replay()) || !reflect.DeepEqual(h1, a.History("a")) {
		t.Fatal("Replay/History 连调结果不同或改变了状态")
	}
	n := 0
	for _, k := range []string{"a", "b", "c"} {
		h := a.History(k)
		n += len(h)
		if !slices.IsSortedFunc(h, func(x, y api.Change) int { return int(x.Sn - y.Sn) }) {
			t.Fatalf("键 %s 历史非 sn 升序", k)
		}
	}
	if n != len(s) {
		t.Fatalf("历史总条数 %d != 喂入 %d", n, len(s))
	}
}

// TestRejectedFeedLeavesNoTrace 钉住不变量 4：三类错误可判定互异，整批拒绝不留痕。
func TestRejectedFeedLeavesNoTrace(t *testing.T) {
	a := api.New(3)
	mustFeed(t, a, ch("a", 1, 10))
	beforeR, beforeH := a.Replay(), a.History("a")
	names := []string{"空Key", "负Ver", "历史超限", "混批整体拒"}
	batches := [][]api.Change{
		{ch("", 1, 1)},
		{ch("a", -1, 1)},
		{ch("a", 2, 1), ch("a", 3, 2), ch("a", 4, 3)},
		{ch("b", 1, 1), ch("b", -2, 2)},
	}
	errs := []error{api.ErrEmptyKey, api.ErrNegativeVer, api.ErrHistoryLimit, api.ErrNegativeVer}
	for i := range names {
		if err := a.Feed(batches[i]); !errors.Is(err, errs[i]) {
			t.Fatalf("%s: err=%v, 期望 %v", names[i], err, errs[i])
		}
		if !reflect.DeepEqual(a.Replay(), beforeR) || !reflect.DeepEqual(a.History("a"), beforeH) {
			t.Fatalf("%s: 被拒后状态发生变化", names[i])
		}
	}
	if api.ErrEmptyKey == api.ErrNegativeVer || api.ErrNegativeVer == api.ErrHistoryLimit || api.ErrEmptyKey == api.ErrHistoryLimit {
		t.Fatal("三类哨兵错误不互异")
	}
	// 被拒批次里的合法变更不得消耗序号：下一条合法变更 sn 应紧接。
	mustFeed(t, a, ch("a", 5, 50)) // 被拒后系统仍可用
	if h := a.History("a"); h[len(h)-1].Sn != 2 {
		t.Fatalf("拒绝批次消耗了序号：sn=%d, 期望 2", h[len(h)-1].Sn)
	}
}

// TestConcurrentReadOnly 钉住并发：N 个 goroutine 只读同一实例，结果逐字段相同。
func TestConcurrentReadOnly(t *testing.T) {
	a := api.New(16)
	mustFeed(t, a, seqs["题目七步"]...)
	want := a.Replay()
	outs := make([][]api.Record, 16)
	var wg sync.WaitGroup
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				outs[i] = a.Replay()
				_ = a.History("a")
			}
			if !a.SelfCheck() {
				t.Error("SelfCheck 失败")
			}
		}(i)
	}
	wg.Wait()
	for i, o := range outs {
		if !reflect.DeepEqual(o, want) {
			t.Fatalf("goroutine %d 的 Replay 与其他不同", i)
		}
	}
}
