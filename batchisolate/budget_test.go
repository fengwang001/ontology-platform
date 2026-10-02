package batchisolate

import (
	"fmt"
	"sync"
	"testing"
)

// Cmax=3：规格同一批在第 3 次调用后需要第 4 次调用 [a4] 时中止。
func TestBudgetSpecCmax3(t *testing.T) {
	sink := newScriptSink([]string{"a3"}, nil)
	iso := mustNew(t, 1, 100, 3, sink.Write)
	res, _ := iso.Submit(idsRange("a", 5))
	// 1整批永久 2左半成功 3[a3]永久Poison；[a4] 需第4次→Budget。
	if res.Calls != 3 {
		t.Fatalf("calls=%d want 3", res.Calls)
	}
	if fmt.Sprint(res.Delivered) != "[a0 a1 a2]" {
		t.Fatalf("delivered=%v", res.Delivered)
	}
	want := []DeadLetter{{"a3", Poison}, {"a4", Budget}}
	if fmt.Sprint(res.Dead) != fmt.Sprint(want) {
		t.Fatalf("dead=%v want %v", res.Dead, want)
	}
	if fmt.Sprint(iso.Known()) != "[a3]" {
		t.Fatalf("known=%v want [a3]（中止时已确认毒丸仍入表）", iso.Known())
	}
	t.Logf("Cmax=3 | 交付=%v 死信=%v Calls=%d 表=%v",
		res.Delivered, res.Dead, res.Calls, iso.Known())
}

// Cmax=2：a3 与 a4 都记 Budget（a3 尚未被裁决）。
func TestBudgetSpecCmax2(t *testing.T) {
	sink := newScriptSink([]string{"a3"}, nil)
	iso := mustNew(t, 1, 100, 2, sink.Write)
	res, _ := iso.Submit(idsRange("a", 5))
	if res.Calls != 2 {
		t.Fatalf("calls=%d want 2", res.Calls)
	}
	if fmt.Sprint(res.Delivered) != "[a0 a1 a2]" {
		t.Fatalf("delivered=%v", res.Delivered)
	}
	want := []DeadLetter{{"a3", Budget}, {"a4", Budget}}
	if fmt.Sprint(res.Dead) != fmt.Sprint(want) {
		t.Fatalf("dead=%v want %v", res.Dead, want)
	}
	if len(iso.Known()) != 0 {
		t.Fatalf("known=%v want empty", iso.Known())
	}
	t.Logf("Cmax=2 | 交付=%v 死信=%v Calls=%d 表=%v",
		res.Delivered, res.Dead, res.Calls, iso.Known())
}

// Cmax=1：整批永久失败后，左半需要第 2 次即中止，整批未裁决记录均 Budget。
func TestBudgetAbortMarksAllUndecided(t *testing.T) {
	sink := newScriptSink([]string{"a3"}, nil)
	iso := mustNew(t, 1, 100, 1, sink.Write)
	res, _ := iso.Submit(idsRange("a", 5))
	if res.Calls != 1 || len(res.Delivered) != 0 {
		t.Fatalf("calls=%d delivered=%v", res.Calls, res.Delivered)
	}
	for _, d := range res.Dead {
		if d.Reason != Budget {
			t.Fatalf("dead=%v all should be Budget", res.Dead)
		}
	}
}

// 预算在瞬时重试途中耗尽：中止且不记任何毒丸/耗尽。
func TestBudgetDuringTransientRetries(t *testing.T) {
	// R=2，整批 [x,y] 连续瞬时；Cmax=2：尝试第3次时中止。
	tr := map[string]int{setKey([]string{"x", "y"}): 3}
	sink := newScriptSink(nil, tr)
	iso := mustNew(t, 2, 100, 2, sink.Write)
	res, _ := iso.Submit([]string{"x", "y"})
	if res.Calls != 2 {
		t.Fatalf("calls=%d want 2", res.Calls)
	}
	if fmt.Sprint(deadIDs(res.Dead)) != "[x y]" {
		t.Fatalf("dead=%v", res.Dead)
	}
	for _, d := range res.Dead {
		if d.Reason != Budget {
			t.Fatalf("reason=%s want Budget", d.Reason)
		}
	}
}

// n=1024：无瞬时、无已知、R 任意、预算足够。
// 毒丸全在最左位置恰为 21 次；毒丸只在最右位置恰为 11 次；
// 并对任意毒丸分布校验 calls ≤ 1+2k*ceil(log2 n)。
func TestBound1024(t *testing.T) {
	const n = 1024
	const cmax = 1_000_000

	run := func(t *testing.T, poison []string) *Result {
		sink := newScriptSink(poison, nil)
		iso := mustNew(t, 3, 1000, cmax, sink.Write)
		res, err := iso.Submit(idsRange("a", n))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	resLeft := run(t, []string{"a0"})
	if resLeft.Calls != 21 {
		t.Fatalf("leftmost calls=%d want 21", resLeft.Calls)
	}
	resRight := run(t, []string{"a1023"})
	if resRight.Calls != 11 {
		t.Fatalf("rightmost calls=%d want 11", resRight.Calls)
	}

	// 若干 k 值与位置，校验上界。
	bound := func(k int) int {
		// ceil(log2 1024) = 10
		return 1 + 2*k*10
	}
	cases := [][]string{
		{"a0", "a1023"},
		{"a1", "a2", "a511", "a512"},
		{"a100", "a200", "a300", "a400", "a500", "a600", "a700", "a800"},
	}
	for i, ps := range cases {
		res := run(t, ps)
		if res.Calls > bound(len(ps)) {
			t.Fatalf("case %d: calls=%d exceeds bound %d (k=%d)",
				i, res.Calls, bound(len(ps)), len(ps))
		}
		if len(res.Dead) != len(ps) {
			t.Fatalf("case %d: dead=%d want %d", i, len(res.Dead), len(ps))
		}
		t.Logf("k=%d calls=%d ≤ 上界%d | 左most=%d 右most=%d",
			len(ps), res.Calls, bound(len(ps)), resLeft.Calls, resRight.Calls)
	}
}

// 被拒绝的操作不调用 sink、不改变已知表。
func TestRejections(t *testing.T) {
	type tc struct {
		r, km, cmax int
	}
	for _, c := range []tc{{-1, 1, 1}, {11, 1, 1}, {0, -1, 1}, {0, 1001, 1}, {0, 1, 0}, {0, 1, 1000001}} {
		if _, err := New(c.r, c.km, c.cmax, func([]string) error { return nil }); err != errInvalidArgs {
			t.Fatalf("New(%+v) err=%v want errInvalidArgs", c, err)
		}
	}
	if _, err := New(0, 1, 1, nil); err != errInvalidArgs {
		t.Fatalf("nil sink err=%v", err)
	}

	sink := newScriptSink(nil, nil)
	iso := mustNew(t, 0, 10, 10, sink.Write)
	iso.mergePoison([]string{"z"})

	// 参数非法优先于已关闭。
	iso.Close()
	if _, err := iso.Submit(nil); err != errInvalidArgs {
		t.Fatalf("empty after close: err=%v want errInvalidArgs", err)
	}
	if _, err := iso.Submit([]string{"ok", "ok"}); err != errInvalidArgs {
		t.Fatalf("dup: %v", err)
	}
	if _, err := iso.Submit([]string{"ok", ""}); err != errInvalidArgs {
		t.Fatalf("empty id: %v", err)
	}
	// 合法参数 → 已关闭。
	if _, err := iso.Submit([]string{"ok"}); err != errClosed {
		t.Fatalf("closed: err=%v want errClosed", err)
	}
	if sink.calls() != 0 {
		t.Fatalf("rejected submit called sink %d times", sink.calls())
	}
	if fmt.Sprint(iso.Known()) != "[z]" {
		t.Fatalf("table changed: %v", iso.Known())
	}
	// Close 幂等。
	if err := iso.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// Close 等待在途 Submit 结束。
func TestCloseWaitsInFlight(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	enterOnce := sync.Once{}
	var done atomicFlag
	sink := func(ids []string) error {
		enterOnce.Do(func() { close(entered) })
		<-release
		done.set()
		return nil
	}
	iso := mustNew(t, 0, 10, 10, sink)
	go func() {
		_, _ = iso.Submit([]string{"x"})
	}()
	<-entered
	closed := make(chan struct{})
	go func() { _ = iso.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned before in-flight Submit finished")
	default:
	}
	close(release)
	<-closed
	if !done.get() {
		t.Fatal("submit did not finish")
	}
}

type atomicFlag struct {
	mu sync.Mutex
	v  bool
}

func (f *atomicFlag) set()      { f.mu.Lock(); f.v = true; f.mu.Unlock() }
func (f *atomicFlag) get() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.v }
