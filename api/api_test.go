package api

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/pipe"
	"ontology/pred"
)

func testChain() []Op {
	return []Op{pipe.Filter(pred.Even()), pipe.RecordHigh(), pipe.Filter(pred.KindEq("A"))}
}

func sixEvents() []Event {
	return []Event{
		{Seq: 1, Val: 10, Kind: "A"}, {Seq: 2, Val: 25, Kind: "A"},
		{Seq: 3, Val: 18, Kind: "A"}, {Seq: 4, Val: 30, Kind: "B"},
		{Seq: 5, Val: 22, Kind: "A"}, {Seq: 6, Val: 40, Kind: "A"},
	}
}

// TestErrorsDistinct 三类哨兵错误互不相同。
func TestErrorsDistinct(t *testing.T) {
	errs := map[string]error{"ErrBadPredicate": ErrBadPredicate, "ErrBadEvent": ErrBadEvent, "ErrBarrier": ErrBarrier}
	for na, a := range errs {
		for nb, b := range errs {
			if na != nb && errors.Is(a, b) {
				t.Fatalf("%s 与 %s 不可区分", na, nb)
			}
		}
	}
}

// TestBadEvent 非法事件被 Feed 拒绝（表驱动）。
func TestBadEvent(t *testing.T) {
	tests := []struct {
		name string
		ev   Event
	}{
		{"Seq为零", Event{Seq: 0, Val: 2, Kind: "A"}},
		{"Seq为负", Event{Seq: -3, Val: 2, Kind: "A"}},
		{"Kind为空串", Event{Seq: 1, Val: 2, Kind: ""}},
	}
	for _, tt := range tests {
		pl, err := New(testChain())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pl.Feed([]Event{tt.ev}); !errors.Is(err, ErrBadEvent) {
			t.Errorf("%s: err=%v, 应为 ErrBadEvent", tt.name, err)
		}
	}
}

// TestRejectedLeavesNoTrace 不变量4：被拒操作不改变任何状态，之后可正常使用。
func TestRejectedLeavesNoTrace(t *testing.T) {
	pl, err := New(testChain())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Feed([]Event{{Seq: 1, Val: 10, Kind: "A"}}); err != nil {
		t.Fatal(err)
	}
	before := pl.Output()
	// 批次含非法事件：若 {2,100,A} 被执行会把 S.max 抬到 100。
	if _, err := pl.Feed([]Event{{Seq: 2, Val: 100, Kind: "A"}, {Seq: 0, Val: 1, Kind: "x"}}); !errors.Is(err, ErrBadEvent) {
		t.Fatalf("err=%v, 应为 ErrBadEvent", err)
	}
	if got := pl.Output(); !slices.Equal(got, before) {
		t.Fatalf("被拒批次留下痕迹: got %v, want %v", got, before)
	}
	// 之后正常使用：Val=50 只有在 max 仍为 10（未被污染）时才是新高。
	out, err := pl.Feed([]Event{{Seq: 3, Val: 50, Kind: "A"}})
	if err != nil || len(out) != 1 || out[0].Val != 50 {
		t.Fatalf("拒绝后实例异常: out=%v err=%v（S.max 可能被污染）", out, err)
	}
}

// TestConcurrentReaders 并发只读同一已喂满实例，输出逐字段相同。
func TestConcurrentReaders(t *testing.T) {
	pl, err := New(testChain())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Feed(sixEvents()); err != nil {
		t.Fatal(err)
	}
	want := pl.Output()
	const n = 16
	outs := make([][]Out, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i] = pl.Output()
			errs[i] = SelfCheck()
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("并发 SelfCheck 失败: %v", errs[i])
		}
		if !slices.Equal(outs[i], want) {
			t.Fatalf("goroutine %d 输出不一致: got %v, want %v", i, outs[i], want)
		}
	}
}

// TestSelfCheck 内置自检核验四条不变量。
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
