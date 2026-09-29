package alarm

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// logStep 打印单测要求的四要素：输入、累计值、事件类型与判定依据。
func logStep(t *testing.T, key string, ev Event, err error) {
	t.Helper()
	if err != nil {
		t.Logf("input: key=%q delta=%d | cumulative=unchanged | event=REJECTED | reason=%v",
			key, ev.Delta, err)
		return
	}
	t.Logf("input: key=%q delta=%d | cumulative=%d | event=%s | armed=%t | reason=%s",
		key, ev.Delta, ev.Value, ev.Type, ev.Armed, ev.Reason)
}

func apply(t *testing.T, s *Stream, key string, delta int64) Event {
	t.Helper()
	ev, err := s.Add(key, delta)
	logStep(t, key, ev, err)
	if err != nil {
		t.Fatalf("Add(%q,%d) unexpected error: %v", key, delta, err)
	}
	return ev
}

func newTestStream(t *testing.T) *Stream {
	t.Helper()
	s, err := New(100, 20) // 阈值 100，迟滞带 20，解除下沿 80
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// TestBoundaryCritical 覆盖恰好等于阈值触发、恰好等于下沿保持开启、
// 下沿之下 1 解除、解除后再回到阈值重新报警等临界场景。
func TestBoundaryCritical(t *testing.T) {
	s := newTestStream(t)

	ev := apply(t, s, "k", 99)
	if ev.Type != EventNone || s.Armed("k") {
		t.Fatalf("value 99 must stay closed, got %s armed=%v", ev.Type, s.Armed("k"))
	}

	ev = apply(t, s, "k", 1) // 恰好等于阈值 100
	if ev.Type != EventAlarm || !s.Armed("k") || ev.Value != 100 {
		t.Fatalf("value exactly threshold must alarm, got %+v", ev)
	}

	ev = apply(t, s, "k", -20) // 恰好等于下沿 80：保持开启
	if ev.Type != EventNone || !s.Armed("k") || ev.Value != 80 {
		t.Fatalf("value exactly lower bound must stay armed, got %+v", ev)
	}

	ev = apply(t, s, "k", -1) // 79：跌破下沿，解除
	if ev.Type != EventClear || s.Armed("k") || ev.Value != 79 {
		t.Fatalf("value below lower bound must clear, got %+v", ev)
	}

	ev = apply(t, s, "k", 21) // 回到 100：关闭态再次触发
	if ev.Type != EventAlarm || !s.Armed("k") || ev.Value != 100 {
		t.Fatalf("reaching threshold from closed must alarm again, got %+v", ev)
	}

	events := s.Events("k")
	if len(events) != 3 {
		t.Fatalf("expected 3 edge events, got %d: %+v", len(events), events)
	}
	if events[0].Type != EventAlarm || events[1].Type != EventClear || events[2].Type != EventAlarm {
		t.Fatalf("event sequence must be ALARM,CLEAR,ALARM, got %+v", events)
	}
	if events[0].Seq == events[1].Seq || events[1].Seq == events[2].Seq {
		t.Fatalf("event seq numbers must be unique: %+v", events)
	}
}

// TestHysteresisJitter 验证开启态下严格落在迟滞区间 [下沿, 阈值] 的抖动
// 不产生任何边沿事件；关闭态低于阈值的抖动同样不报警。
func TestHysteresisJitter(t *testing.T) {
	s := newTestStream(t)
	apply(t, s, "k", 100) // 进入开启态

	targets := []int64{95, 80, 90, 100, 88, 80, 99, 100}
	prev := int64(100)
	for _, target := range targets {
		ev := apply(t, s, "k", target-prev)
		if ev.Type != EventNone {
			t.Fatalf("jitter to %d must not emit edge, got %s", target, ev.Type)
		}
		if !s.Armed("k") {
			t.Fatalf("jitter to %d must stay armed", target)
		}
		prev = target
	}
	if ev := apply(t, s, "k", -21); ev.Type != EventClear { // 100 -> 79，解除
		t.Fatalf("drop to 79 must clear, got %s", ev.Type)
	}
	if ev := apply(t, s, "k", 1); ev.Type != EventNone { // 79 -> 80，关闭态不报警
		t.Fatalf("closed state reaching lower bound must stay closed, got %s", ev.Type)
	}
}

// TestClosedStateJitter 验证关闭态在阈值下方反复抖动不触发。
func TestClosedStateJitter(t *testing.T) {
	s := newTestStream(t)
	path := []int64{50, 20, 0, -30, 99, 80, 0, 99, 100}
	for _, v := range path {
		ev := apply(t, s, "k", v-s.Value("k"))
		want := EventNone
		if v == 100 {
			want = EventAlarm
		}
		if ev.Type != want {
			t.Fatalf("at value %d expected %s, got %s", v, want, ev.Type)
		}
	}
}

// TestNegativeValues 验证累计值可为负且只有达到阈值才报警。
func TestNegativeValues(t *testing.T) {
	s := newTestStream(t)
	ev := apply(t, s, "k", -500)
	if ev.Type != EventNone || ev.Value != -500 || s.Armed("k") {
		t.Fatalf("negative cumulative must stay closed: %+v", ev)
	}
	ev = apply(t, s, "k", 600) // -500+600=100
	if ev.Type != EventAlarm || ev.Value != 100 {
		t.Fatalf("crossing from negative to threshold must alarm: %+v", ev)
	}
	ev = apply(t, s, "k", -1000) // 100-1000=-900，远低于下沿
	if ev.Type != EventClear || ev.Value != -900 || s.Armed("k") {
		t.Fatalf("drop far below lower bound must clear: %+v", ev)
	}
}

func TestInvalidParameters(t *testing.T) {
	cases := []struct {
		name       string
		threshold  int64
		hysteresis int64
		want       error
	}{
		{"zero threshold", 0, 1, ErrNonPositiveThreshold},
		{"negative threshold", -10, 1, ErrNonPositiveThreshold},
		{"zero hysteresis", 100, 0, ErrNonPositiveHysteresis},
		{"negative hysteresis", 100, -5, ErrNonPositiveHysteresis},
		{"hysteresis equals threshold", 100, 100, ErrHysteresisTooLarge},
		{"hysteresis above threshold", 100, 101, ErrHysteresisTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.threshold, c.hysteresis)
			if !errors.Is(err, c.want) {
				t.Fatalf("New(%d,%d) err=%v, want %v", c.threshold, c.hysteresis, err, c.want)
			}
			t.Logf("input: threshold=%d hysteresis=%d | rejected by %v",
				c.threshold, c.hysteresis, err)
		})
	}
}

func TestEmptyKey(t *testing.T) {
	s := newTestStream(t)
	ev, err := s.Add("", 100)
	logStep(t, "", Event{Delta: 100}, err)
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	if len(s.Keys()) != 0 {
		t.Fatalf("failed add must not create key, keys=%v", s.Keys())
	}
	if ev != (Event{}) {
		t.Fatalf("rejected call must return zero event, got %+v", ev)
	}
}

func TestAccessors(t *testing.T) {
	s := newTestStream(t)
	if s.Threshold() != 100 || s.LowerBound() != 80 {
		t.Fatalf("threshold/lower bound = %d/%d", s.Threshold(), s.LowerBound())
	}
	if s.Value("missing") != 0 || s.Armed("missing") || s.Events("missing") != nil {
		t.Fatalf("unknown key must read as zero-state")
	}
	apply(t, s, "x", 100)
	apply(t, s, "y", 1)
	keys := map[string]bool{}
	for _, k := range s.Keys() {
		keys[k] = true
	}
	if !keys["x"] || !keys["y"] {
		t.Fatalf("Keys() snapshot missing entries: %v", s.Keys())
	}
}

// TestOverflowAtomic 验证双向溢出被拒绝且不改变值、状态与事件列表，
// 同时不影响其他键。
func TestOverflowAtomic(t *testing.T) {
	s, err := New(10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("k", maxInt64); err != nil {
		t.Fatal(err)
	} // k 立即报警，value=maxInt64
	if _, err := s.Add("other", 7); err != nil {
		t.Fatal(err)
	}

	beforeEvents := len(s.Events("k"))
	_, err = s.Add("k", 1)
	logStep(t, "k", Event{Delta: 1}, err)
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow upward, got %v", err)
	}
	if s.Value("k") != maxInt64 || !s.Armed("k") || len(s.Events("k")) != beforeEvents {
		t.Fatalf("overflow must be atomic: value=%d armed=%v events=%d",
			s.Value("k"), s.Armed("k"), len(s.Events("k")))
	}

	// 独立键构造下溢：minInt64 合法，再减 1 必然下溢。
	apply(t, s, "down", minInt64)
	_, err = s.Add("down", -1)
	logStep(t, "k", Event{Delta: -1}, err)
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow downward, got %v", err)
	}
	if s.Value("down") != minInt64 || s.Armed("down") || len(s.Events("down")) != 0 {
		t.Fatalf("downward overflow must be atomic: value=%d armed=%v",
			s.Value("down"), s.Armed("down"))
	}
	if s.Value("other") != 7 || s.Armed("other") { // 阈值 10，7 仍处于关闭态
		t.Fatalf("other key must be unaffected: value=%d armed=%v",
			s.Value("other"), s.Armed("other"))
	}
}

// TestMultipleKeys 验证各键累计值、状态与事件列表互相独立。
func TestMultipleKeys(t *testing.T) {
	s := newTestStream(t)
	apply(t, s, "a", 100)
	apply(t, s, "b", 50)
	apply(t, s, "a", -21) // a: 79 解除

	if s.Value("a") != 79 || s.Armed("a") {
		t.Fatalf("a should be cleared at 79, got value=%d armed=%v", s.Value("a"), s.Armed("a"))
	}
	if s.Value("b") != 50 || s.Armed("b") {
		t.Fatalf("b should stay closed at 50")
	}
	if len(s.Events("a")) != 2 || len(s.Events("b")) != 0 {
		t.Fatalf("independent event lists: a=%v b=%v", s.Events("a"), s.Events("b"))
	}
	got := s.Events("a")
	got[0].Type = EventClear // 篡改快照不得影响内部存储
	if s.Events("a")[0].Type != EventAlarm {
		t.Fatalf("Events() must return a defensive copy")
	}
}

// TestReplayReference 用逐步重放核对一段含全部边界的序列。
func TestReplayReference(t *testing.T) {
	ops := []Op{
		{"k", 99}, {"k", 1}, // 临界触发
		{"k", -1}, {"k", -19}, // 带内抖动，到下沿
		{"k", -1},             // 解除
		{"k", 21},             // 重新报警
		{"k", -10}, {"k", 10}, // 带内抖动（90..100），不产生边沿
	}
	steps, err := Replay(100, 20, ops)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []EventType{
		EventNone, EventAlarm,
		EventNone, EventNone,
		EventClear, EventAlarm,
		EventNone, EventNone,
	}
	for i, st := range steps {
		t.Logf("replay step %d: input key=%q delta=%d | cumulative=%d | event=%s | reason=%s",
			i+1, st.Op.Key, st.Op.Delta, st.Value, st.Event.Type, st.Event.Reason)
		if st.Event.Type != wantTypes[i] {
			t.Fatalf("step %d want %s got %s", i+1, wantTypes[i], st.Event.Type)
		}
	}
}

// TestReplayFailures 验证重放入口拒绝非法参数且失败步保留前置状态。
func TestReplayFailures(t *testing.T) {
	if _, err := Replay(0, 1, nil); !errors.Is(err, ErrNonPositiveThreshold) {
		t.Fatalf("got %v", err)
	}
	// 10+maxInt64 必然溢出；失败步必须保留前置状态。
	steps, err := Replay(10, 2, []Op{{"k", 10}, {"k", maxInt64}})
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	if len(steps) != 2 || steps[1].Value != 10 || len(steps[1].Events) != 1 {
		t.Fatalf("failed step must preserve prior state, got %+v", steps)
	}
}

// TestConcurrentSameKey 同键并发：每轮各协程施加相同增量，
// 轮次结果与提交顺序无关，最终状态、值与事件序列必须与串行重放一致。
func TestConcurrentSameKey(t *testing.T) {
	const goroutines, rounds = 8, 8
	// 阈值 10、迟滞 4、下沿 6；每轮总增量 goroutines*roundDeltas[r]。
	roundDeltas := []int64{2, 2, 2, 2, -2, -2, -2, -2}

	s, err := New(10, 4)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	collected := make(chan Event, goroutines*rounds)
	roundStart := make([]chan struct{}, rounds)
	for r := range roundStart {
		roundStart[r] = make(chan struct{})
	}
	var roundDone sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r, d := range roundDeltas {
				<-roundStart[r] // 轮次屏障：同轮更新并发，轮次之间串行
				ev, err := s.Add("k", d)
				if err != nil {
					t.Errorf("concurrent Add: %v", err)
					roundDone.Done()
					return
				}
				if ev.Type != EventNone {
					collected <- ev
				}
				roundDone.Done()
			}
		}()
	}
	for r := range roundDeltas {
		roundDone.Add(goroutines)
		close(roundStart[r])
		roundDone.Wait()
	}
	wg.Wait()
	close(collected)

	// 串行参照：每轮 goroutines 个相同增量。
	var ops []Op
	for _, d := range roundDeltas {
		for g := 0; g < goroutines; g++ {
			ops = append(ops, Op{"k", d})
		}
	}
	steps, err := Replay(10, 4, ops)
	if err != nil {
		t.Fatal(err)
	}
	var refEdges []Event
	for _, st := range steps {
		if st.Event.Type != EventNone {
			refEdges = append(refEdges, st.Event)
		}
	}

	if s.Value("k") != 0 || s.Armed("k") {
		t.Fatalf("final value must be 0 and closed, got value=%d armed=%v",
			s.Value("k"), s.Armed("k"))
	}
	gotEvents := s.Events("k")
	if len(gotEvents) != len(refEdges) {
		t.Fatalf("edge count: got %d want %d", len(gotEvents), len(refEdges))
	}
	var observed []Event
	for ev := range collected {
		observed = append(observed, ev)
	}
	countByType := func(evs []Event) map[EventType]int {
		m := map[EventType]int{}
		for _, e := range evs {
			m[e.Type]++
		}
		return m
	}
	if fmt.Sprint(countByType(observed)) != fmt.Sprint(countByType(refEdges)) {
		t.Fatalf("concurrent edge multiset=%v, reference=%v",
			countByType(observed), countByType(refEdges))
	}
	for i, e := range gotEvents {
		if e.Type != refEdges[i].Type || e.Value != refEdges[i].Value {
			t.Fatalf("event %d mismatch: got %+v want %+v", i, e, refEdges[i])
		}
	}
	t.Logf("concurrent same-key: %d updates, edges=%v (matches serial replay)",
		goroutines*rounds, countByType(gotEvents))
}

// TestConcurrentDifferentKeys 异键并发：各键互不干扰，且可并发读取。
func TestConcurrentDifferentKeys(t *testing.T) {
	const keysN, opsPerKey = 16, 24
	s, err := New(100, 20)
	if err != nil {
		t.Fatal(err)
	}
	var readerWg, workerWg sync.WaitGroup
	stop := make(chan struct{})

	readerWg.Add(1)
	go func() { // 持续并发读取
		defer readerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				for k := 0; k < keysN; k++ {
					key := fmt.Sprintf("key-%d", k)
					_ = s.Value(key)
					_ = s.Armed(key)
					_ = s.Events(key)
				}
			}
		}
	}()

	for k := 0; k < keysN; k++ {
		workerWg.Add(1)
		go func(k int) {
			defer workerWg.Done()
			key := fmt.Sprintf("key-%d", k)
			for i := 0; i < opsPerKey; i++ {
				// 偶数键锯齿上升下降，奇数键始终低于阈值。
				delta := int64(30)
				if i%2 == 1 {
					delta = -30
				}
				if k%2 == 1 {
					delta = 5
					if i%2 == 1 {
						delta = -5
					}
				}
				if _, err := s.Add(key, delta); err != nil {
					t.Errorf("Add(%q): %v", key, err)
					return
				}
			}
		}(k)
	}
	workerWg.Wait()
	close(stop)
	readerWg.Wait()

	for k := 0; k < keysN; k++ {
		key := fmt.Sprintf("key-%d", k)
		var ops []Op
		for i := 0; i < opsPerKey; i++ {
			delta := int64(30)
			if i%2 == 1 {
				delta = -30
			}
			if k%2 == 1 {
				delta = 5
				if i%2 == 1 {
					delta = -5
				}
			}
			ops = append(ops, Op{key, delta})
		}
		steps, rerr := Replay(100, 20, ops)
		if rerr != nil {
			t.Fatal(rerr)
		}
		ref := steps[len(steps)-1]
		if s.Value(key) != ref.Value || s.Armed(key) != ref.Armed ||
			len(s.Events(key)) != len(ref.Events) {
			t.Fatalf("key %s diverged from serial replay: value=%d/%d armed=%v/%v events=%d/%d",
				key, s.Value(key), ref.Value, s.Armed(key), ref.Armed,
				len(s.Events(key)), len(ref.Events))
		}
		t.Logf("key %s final: cumulative=%d armed=%v events=%d (matches serial replay)",
			key, ref.Value, ref.Armed, len(ref.Events))
	}
}
