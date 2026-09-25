package api_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/pipe"
	"ontology/pred"
)

func f1s2() ([]api.Op, []api.Op) { // 两份独立状态的演示链 F1→S→F2
	mk := func() []api.Op {
		return []api.Op{{Name: "F1", Pred: pred.Even()}, {Name: "S", State: &pred.RecordHigh{}},
			{Name: "F2", Pred: pred.KindIs("A")}}
	}
	return mk(), mk()
}

func seqs() [][]api.Event {
	out := [][]api.Event{{
		{Seq: 1, Val: 10, Kind: "A"}, {Seq: 2, Val: 25, Kind: "A"}, {Seq: 3, Val: 18, Kind: "A"},
		{Seq: 4, Val: 30, Kind: "B"}, {Seq: 5, Val: 22, Kind: "A"}, {Seq: 6, Val: 40, Kind: "A"},
	}}
	for n := 0; n < 3; n++ {
		var evs []api.Event
		for i := 1; i <= 60; i++ {
			evs = append(evs, api.Event{Seq: int64(i), Val: int64((i*11 + n*7) % 60),
				Kind: string(rune('A' + (i+n)%2))})
		}
		out = append(out, evs)
	}
	return out
}

func mustNew(t *testing.T, ops []api.Op) *api.API {
	t.Helper()
	a, err := api.New(ops)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mustFeed(t *testing.T, a *api.API, evs []api.Event) []api.Out {
	t.Helper()
	out, err := a.Feed(evs)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNaiveEqualsPlanned(t *testing.T) { // 不变量 1
	for i, evs := range seqs() {
		on, oo := f1s2()
		naive, err := pipe.Naive(on)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := naive.Feed(evs)
		if got := mustFeed(t, mustNew(t, oo), evs); !slices.Equal(want, got) {
			t.Fatalf("序列%d: 优化链 %v != 朴素链 %v", i, got, want)
		}
	}
}

func TestStatelessCommute(t *testing.T) { // 不变量 2：同段重排/合并输出不变
	preds := []pred.Pred{pred.Even(), pred.KindIs("A"),
		{Name: "lo", Field: "Seq", F: func(e pred.Event) bool { return e.Seq > 3 }}}
	perm := [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}}
	for i, evs := range seqs() {
		var want []api.Out
		for j, ord := range perm {
			ops := []api.Op{{Name: "S", State: &pred.RecordHigh{}}}
			for _, k := range ord {
				ops = append([]api.Op{{Name: preds[k].Name, Pred: preds[k]}}, ops...)
			}
			if got := mustFeed(t, mustNew(t, ops), evs); j == 0 {
				want = got
			} else if !slices.Equal(want, got) {
				t.Fatalf("序列%d 排列%v: 输出不同", i, ord)
			}
		}
	}
}

func TestBarrierRejected(t *testing.T) { // 不变量 3
	ops, _ := f1s2()
	for _, c := range []struct{ from, to int }{{2, 0}, {0, 2}, {1, 0}, {1, 2}} {
		if err := pipe.ValidateMove(ops, c.from, c.to); !errors.Is(err, pipe.ErrBarrier) {
			t.Fatalf("移动 %d→%d 应被拒绝, 得 %v", c.from, c.to, err)
		}
	}
	if err := pipe.ValidateMove(ops, 0, 0); err != nil {
		t.Fatalf("段内原地移动应合法, 得 %v", err)
	}
}

func TestFailureLeavesNoTrace(t *testing.T) { // 不变量 4
	bads := [][]api.Event{
		{{Seq: 1, Val: 100, Kind: "A"}, {Seq: 0, Val: 1, Kind: "A"}}, // 非法事件在尾部
		{{Seq: 2, Val: 100, Kind: ""}},                               // Kind 空串
	}
	for i, bad := range bads {
		a := mustNew(t, []api.Op{{Name: "S", State: &pred.RecordHigh{}}})
		mustFeed(t, a, []api.Event{{Seq: 1, Val: 10, Kind: "A"}})
		if _, err := a.Feed(bad); !errors.Is(err, pred.ErrBadEvent) {
			t.Fatalf("批次%d: 应报 ErrBadEvent, 得 %v", i, err)
		}
		if got := mustFeed(t, a, []api.Event{{Seq: 9, Val: 50, Kind: "A"}}); len(got) != 1 {
			t.Fatalf("批次%d: 被拒批次留下了痕迹", i) // max 仍是 10 而非 100
		}
	}
	badPreds := []pred.Pred{{Name: "nil"}, {Name: "nf", Field: "Nope",
		F: func(pred.Event) bool { return true }}}
	for _, p := range badPreds {
		if _, err := api.New([]api.Op{{Name: "x", Pred: p}}); !errors.Is(err, pred.ErrBadPredicate) {
			t.Fatalf("谓词 %q 应报 ErrBadPredicate, 得 %v", p.Name, err)
		}
	}
}

func TestConcurrentReadOnly(t *testing.T) { // 并发只读，无 sleep
	a := mustNew(t, []api.Op{{Name: "S", State: &pred.RecordHigh{}}})
	want := mustFeed(t, a, seqs()[0])
	start, errs := make(chan struct{}), make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if got := a.Out(); !slices.Equal(got, want) {
				errs <- errors.New("并发读输出不一致")
			}
			if err := api.SelfCheck(); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
