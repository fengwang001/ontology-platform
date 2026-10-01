package watchdog

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type opKind string

const (
	opFeed    opKind = "Feed"
	opTick    opKind = "Tick"
	opRestart opKind = "Restart"
)

type op struct {
	kind opKind
	t    int64
}

// naiveWatchdog 是按题意逐整数时刻推进的朴素参考模型：
// 每次操作先从上个操作时刻的下一时刻一步步走到 t，
// 每一步先结算该时刻的到点事件（预警先于超时），再在 t 处理操作本身。
type naiveWatchdog struct {
	open, close, pre int64
	f                int64
	reset            bool
	lastT            int64
	warnFired        bool
	events           []Event
}

func newNaive(t0, open, closeAt, pre int64) *naiveWatchdog {
	return &naiveWatchdog{open: open, close: closeAt, pre: pre, f: t0, lastT: t0}
}

func (n *naiveWatchdog) stepTo(t int64) {
	for cur := n.lastT + 1; cur <= t; cur++ {
		if n.reset {
			return
		}
		if n.pre > 0 && !n.warnFired && cur == n.f+n.close-n.pre {
			n.events = append(n.events, Event{Time: cur, Kind: Warning})
			n.warnFired = true
		}
		if cur == n.f+n.close {
			n.events = append(n.events, Event{Time: cur, Kind: Reset, Reason: Timeout})
			n.reset = true
			return
		}
	}
}

func (n *naiveWatchdog) run(kind opKind, t int64) error {
	if t < n.lastT {
		return ErrTimeRewound
	}
	n.stepTo(t)
	n.lastT = t
	switch kind {
	case opTick:
		return nil
	case opFeed:
		if n.reset {
			return ErrFeedWhileReset
		}
		d := t - n.f
		if d < n.open {
			n.events = append(n.events, Event{Time: t, Kind: Reset, Reason: Early})
			n.reset = true
			return ErrFeedTooEarly
		}
		n.f = t
		n.warnFired = false
		return nil
	case opRestart:
		if !n.reset {
			return ErrRestartNotReset
		}
		n.reset = false
		n.f = t
		n.warnFired = false
		return nil
	}
	return nil
}

func errName(err error) string {
	switch {
	case err == nil:
		return "OK"
	case errors.Is(err, ErrInvalidConfig):
		return "InvalidConfig"
	case errors.Is(err, ErrTimeRewound):
		return "TimeRewound"
	case errors.Is(err, ErrFeedWhileReset):
		return "FeedWhileReset"
	case errors.Is(err, ErrFeedTooEarly):
		return "FeedTooEarly(Early reset)"
	case errors.Is(err, ErrRestartNotReset):
		return "RestartNotReset"
	default:
		return err.Error()
	}
}

func formatEvents(es []Event) string {
	if len(es) == 0 {
		return "[]"
	}
	s := "["
	for i, e := range es {
		if i > 0 {
			s += ", "
		}
		if e.Kind == Warning {
			s += fmt.Sprintf("{t=%d Warning}", e.Time)
		} else {
			s += fmt.Sprintf("{t=%d Reset:%s}", e.Time, e.Reason)
		}
	}
	return s + "]"
}

func eventsEqual(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type scenario struct {
	name                 string
	t0, open, close, pre int64
	ops                  []op
}

var scenarios = []scenario{
	{
		name: "d=open-1 too early", t0: 10, open: 5, close: 8, pre: 2,
		ops: []op{{opFeed, 14}},
	},
	{
		name: "d=open valid", t0: 10, open: 5, close: 8, pre: 2,
		ops: []op{{opFeed, 15}},
	},
	{
		name: "d=close-1 valid warning already due", t0: 10, open: 5, close: 8, pre: 2,
		ops: []op{{opFeed, 17}},
	},
	{
		name: "d=close timeout then feed rejected", t0: 10, open: 5, close: 8, pre: 2,
		ops: []op{{opFeed, 18}},
	},
	{
		name: "warning exactly at d=close-pre then feed valid", t0: 0, open: 2, close: 10, pre: 4,
		ops: []op{{opFeed, 6}},
	},
	{
		name: "warning off by one no warning at close-pre-1", t0: 0, open: 2, close: 10, pre: 4,
		ops: []op{{opTick, 5}, {opFeed, 6}},
	},
	{
		name: "pre=0 no warning even at timeout", t0: 0, open: 0, close: 3, pre: 0,
		ops: []op{{opTick, 3}},
	},
	{
		name: "open=0 feed at t0 is never early", t0: 7, open: 0, close: 4, pre: 1,
		ops: []op{{opFeed, 7}, {opFeed, 7}},
	},
	{
		name: "tick spans warning and timeout in order", t0: 0, open: 2, close: 6, pre: 2,
		ops: []op{{opTick, 10}},
	},
	{
		name: "warning before open early feed records warning first", t0: 0, open: 6, close: 10, pre: 6,
		ops: []op{{opFeed, 4}},
	},
	{
		name: "restart judged after catchup timeout due exactly at t", t0: 0, open: 2, close: 5, pre: 2,
		ops: []op{{opRestart, 5}, {opFeed, 7}},
	},
	{
		name: "restart not in reset rejected but time advances", t0: 0, open: 2, close: 5, pre: 1,
		ops: []op{{opRestart, 1}, {opTick, 3}},
	},
	{
		name: "feed while reset rejected and events stay locked", t0: 0, open: 1, close: 3, pre: 1,
		ops: []op{{opTick, 5}, {opFeed, 6}, {opTick, 9}},
	},
	{
		name: "time rewind changes nothing", t0: 0, open: 1, close: 5, pre: 1,
		ops: []op{{opTick, 4}, {opFeed, 3}, {opFeed, 4}},
	},
	{
		name: "restart then fresh window times out again", t0: 100, open: 10, close: 20, pre: 5,
		ops: []op{{opTick, 120}, {opRestart, 125}, {opTick, 145}},
	},
	{
		name: "early reset same instant as warning warning first", t0: 0, open: 8, close: 10, pre: 3,
		ops: []op{{opFeed, 7}},
	},
	{
		name: "valid feed rearms warning for new period only", t0: 0, open: 1, close: 4, pre: 1,
		ops: []op{{opFeed, 2}, {opFeed, 4}, {opTick, 8}},
	},
}

func applyOp(w *Watchdog, o op) error {
	switch o.kind {
	case opFeed:
		return w.FeedAt(o.t)
	case opTick:
		return w.TickAt(o.t)
	case opRestart:
		return w.RestartAt(o.t)
	}
	return nil
}

func TestAgainstNaiveSimulation(t *testing.T) {
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			real := New(sc.t0, sc.open, sc.close, sc.pre)
			naive := newNaive(sc.t0, sc.open, sc.close, sc.pre)
			t.Logf("input: New(t0=%d, open=%d, close=%d, pre=%d)", sc.t0, sc.open, sc.close, sc.pre)
			for i, o := range sc.ops {
				gotErr := applyOp(real, o)
				wantErr := naive.run(o.kind, o.t)

				t.Logf("op[%d] input: %s(t=%d) | output: got=%s want=%s",
					i, o.kind, o.t, errName(gotErr), errName(wantErr))
				t.Logf("  basis: window [%d,%d) pre=%d; due events recorded before the op; events got=%s want=%s",
					sc.open, sc.close, sc.pre,
					formatEvents(real.Events()), formatEvents(naive.events))

				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("op %d %s(t=%d): err mismatch: got %v, want %v",
						i, o.kind, o.t, gotErr, wantErr)
				}
				gotEvents := real.Events()
				if !eventsEqual(gotEvents, naive.events) {
					t.Fatalf("op %d %s(t=%d): events mismatch:\n got %v\nwant %v",
						i, o.kind, o.t, gotEvents, naive.events)
				}
				if real.InReset() != naive.reset {
					t.Fatalf("op %d %s(t=%d): reset mismatch: got %v want %v",
						i, o.kind, o.t, real.InReset(), naive.reset)
				}
			}
			t.Logf("final events: %s ; inReset got=%v want=%v",
				formatEvents(real.Events()), real.InReset(), naive.reset)
		})
	}
}

func TestExactWindowBoundaries(t *testing.T) {
	// d = open-1、open、close-1、close 四个点逐一断言并与朴素模型对照。
	const t0, open, close, pre = int64(0), int64(4), int64(8), int64(2)
	cases := []struct {
		d       int64
		wantErr error
		reset   bool
	}{
		{open - 1, ErrFeedTooEarly, true},
		{open, nil, false},
		{close - 1, nil, false},
		{close, ErrFeedWhileReset, true},
	}
	for _, c := range cases {
		real := New(t0, open, close, pre)
		naive := newNaive(t0, open, close, pre)
		err := real.FeedAt(t0 + c.d)
		wantErr := naive.run(opFeed, t0+c.d)
		t.Logf("input: Feed(t=%d) d=%d | output: %s | basis: d<open Early; open<=d<close ok; d==close Timeout first, feed rejected",
			c.d, c.d, errName(err))
		if !errors.Is(err, c.wantErr) || !errors.Is(err, wantErr) {
			t.Fatalf("d=%d: err %v, want %v", c.d, err, c.wantErr)
		}
		if real.InReset() != c.reset || real.InReset() != naive.reset {
			t.Fatalf("d=%d: reset=%v want %v", c.d, real.InReset(), c.reset)
		}
		if !eventsEqual(real.Events(), naive.events) {
			t.Fatalf("d=%d events mismatch %v vs %v", c.d, real.Events(), naive.events)
		}
	}
}

func TestInvalidConfig(t *testing.T) {
	bad := []struct {
		t0, open, close, pre int64
	}{
		{0, -1, 5, 1},
		{0, 5, 5, 1},
		{0, 6, 5, 1},
		{0, 1, 5, -1},
		{0, 1, 5, 5},
	}
	for _, b := range bad {
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			New(b.t0, b.open, b.close, b.pre)
		}()
		t.Logf("input: New(t0=%d, open=%d, close=%d, pre=%d) | output: panic=%v | basis: need 0<=open<close and 0<=pre<close",
			b.t0, b.open, b.close, b.pre, recovered)
		if recovered == nil {
			t.Fatalf("expected panic for invalid config %+v", b)
		}
		if err, ok := recovered.(error); !ok || !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("panic = %v, want ErrInvalidConfig", recovered)
		}
	}
}

func TestReplayDeterminism(t *testing.T) {
	sc := scenarios[0]
	run := func() []Event {
		w := New(sc.t0, sc.open, sc.close, sc.pre)
		for _, o := range sc.ops {
			_ = applyOp(w, o)
		}
		return w.Events()
	}
	first, second := run(), run()
	t.Logf("input: replay scenario %q twice | output: %s vs %s", sc.name,
		formatEvents(first), formatEvents(second))
	if !eventsEqual(first, second) {
		t.Fatalf("replay produced different event tables")
	}
}

func TestInjectedClock(t *testing.T) {
	var cur int64
	clock := ClockFunc(func() int64 { return cur })
	w := New(0, 2, 6, 2)

	cur = 4 // 预警时刻 f+close-pre = 4
	if err := w.Tick(clock); err != nil {
		t.Fatal(err)
	}
	t.Logf("input: clock=4 Tick | output: events=%s basis: warning due at 4", formatEvents(w.Events()))

	cur = 5 // 窗口内喂狗
	if err := w.Feed(clock); err != nil {
		t.Fatalf("feed at 5: %v", err)
	}

	cur = 11 // 新周期 f=5，超时时刻 11
	if err := w.Tick(clock); err != nil {
		t.Fatal(err)
	}
	if err := w.Feed(clock); !errors.Is(err, ErrFeedWhileReset) {
		t.Fatalf("feed after timeout = %v, want ErrFeedWhileReset", err)
	}
	if err := w.Restart(clock); err != nil {
		t.Fatalf("restart after timeout: %v", err)
	}
	t.Logf("input: clock-driven Tick/Feed/Tick/Restart | output: %s inReset=%v",
		formatEvents(w.Events()), w.InReset())

	want := []Event{
		{Time: 4, Kind: Warning},
		{Time: 9, Kind: Warning},
		{Time: 11, Kind: Reset, Reason: Timeout},
	}
	if !eventsEqual(w.Events(), want) {
		t.Fatalf("events = %v, want %v", w.Events(), want)
	}
}

func TestConcurrentSerialEquivalence(t *testing.T) {
	// 并发只做查询与 Tick 补记，事件表必须时刻非递减且与串行重放一致。
	w := New(0, 1, 10, 2)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			for k := int64(0); k <= 20; k++ {
				_ = w.TickAt(k)
				es := w.Events()
				for i := 1; i < len(es); i++ {
					if es[i-1].Time > es[i].Time {
						t.Errorf("events not nondecreasing: %v", es)
						return
					}
				}
				_ = w.InReset()
			}
		}(int64(g))
	}
	wg.Wait()

	serial := New(0, 1, 10, 2)
	for k := int64(0); k <= 20; k++ {
		if err := serial.TickAt(k); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("input: 8 goroutines TickAt(0..20) concurrently | output: %s", formatEvents(w.Events()))
	if !eventsEqual(w.Events(), serial.Events()) {
		t.Fatalf("concurrent events %v != serial %v", w.Events(), serial.Events())
	}
}
