package alert

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
)

func newTestMonitor(t *testing.T, threshold, hysteresis int64) *Monitor {
	t.Helper()
	m, err := NewMonitor(Config{Threshold: threshold, Hysteresis: hysteresis})
	if err != nil {
		t.Fatalf("NewMonitor(%d, %d) failed: %v", threshold, hysteresis, err)
	}
	return m
}

func logStep(t *testing.T, key string, delta int64, res Result, err error) {
	t.Helper()
	if err != nil {
		t.Logf("input key=%q delta=%d => rejected: %v", key, delta, err)
		return
	}
	ev := "none"
	reason := fmt.Sprintf("state=%s, no edge crossed", res.State)
	if res.Event != nil {
		ev = res.Event.Type.String()
		reason = res.Event.Reason
	}
	t.Logf("input key=%q delta=%d => value=%d state=%s event=%s reason=%s",
		key, delta, res.Value, res.State, ev, reason)
}

func TestTriggerAtExactThreshold(t *testing.T) {
	m := newTestMonitor(t, 100, 10)

	res, err := m.Add("cpu", 99)
	if err != nil {
		t.Fatal(err)
	}
	logStep(t, "cpu", 99, res, nil)
	if res.Event != nil || res.State != StateClosed {
		t.Fatalf("value=99 below threshold: want no event/closed, got %+v", res)
	}

	res, err = m.Add("cpu", 1)
	if err != nil {
		t.Fatal(err)
	}
	logStep(t, "cpu", 1, res, nil)
	if res.Event == nil || res.Event.Type != EventAlarm || res.State != StateOpen {
		t.Fatalf("value=100 equals threshold: want alarm/open, got %+v", res)
	}
	if res.Value != 100 {
		t.Fatalf("want value 100, got %d", res.Value)
	}
}

func TestHysteresisBandJitter(t *testing.T) {
	m := newTestMonitor(t, 100, 10) // clearLine = 90

	steps := []int64{100, -5, 3, -8, 9, -9} // 值在 [90,100) 内抖动，均不发事件
	for _, d := range steps {
		res, err := m.Add("mem", d)
		if err != nil {
			t.Fatal(err)
		}
		logStep(t, "mem", d, res, nil)
	}
	if got := len(m.EventsFor("mem")); got != 1 {
		t.Fatalf("jitter inside hysteresis band: want exactly 1 alarm event, got %d", got)
	}
	if m.State("mem") != StateOpen {
		t.Fatalf("want still open, got %s", m.State("mem"))
	}
	if v := m.Value("mem"); v != 90 {
		t.Fatalf("want value 90 (== clearLine, stays open), got %d", v)
	}
}

func TestClearLineBoundary(t *testing.T) {
	m := newTestMonitor(t, 100, 10) // clearLine = 90

	if _, err := m.Add("disk", 100); err != nil {
		t.Fatal(err)
	}
	// 恰好等于解除线 90：保持开启，不发事件。
	res, err := m.Add("disk", -10)
	if err != nil {
		t.Fatal(err)
	}
	logStep(t, "disk", -10, res, nil)
	if res.Event != nil || res.State != StateOpen {
		t.Fatalf("value=90 equals clearLine: want no event/open, got %+v", res)
	}

	// 跌破解除线：发解除并转关闭。
	res, err = m.Add("disk", -1)
	if err != nil {
		t.Fatal(err)
	}
	logStep(t, "disk", -1, res, nil)
	if res.Event == nil || res.Event.Type != EventClear || res.State != StateClosed {
		t.Fatalf("value=89 below clearLine: want clear/closed, got %+v", res)
	}
}

func TestNoRepeatAlarmWhileOpen(t *testing.T) {
	m := newTestMonitor(t, 100, 10)

	for _, d := range []int64{150, 50, 50} {
		res, err := m.Add("net", d)
		if err != nil {
			t.Fatal(err)
		}
		logStep(t, "net", d, res, nil)
	}
	if got := len(m.EventsFor("net")); got != 1 {
		t.Fatalf("repeated crossings above threshold: want 1 event, got %d", got)
	}
}

func TestNegativeValueCanRetrigger(t *testing.T) {
	m := newTestMonitor(t, 100, 10)

	seq := []int64{100, -200, 200} // alarm -> clear(负值) -> 回到阈值再次 alarm
	var types []EventType
	for _, d := range seq {
		res, err := m.Add("k", d)
		if err != nil {
			t.Fatal(err)
		}
		logStep(t, "k", d, res, nil)
		if res.Event != nil {
			types = append(types, res.Event.Type)
		}
	}
	want := []EventType{EventAlarm, EventClear, EventAlarm}
	if fmt.Sprint(types) != fmt.Sprint(want) {
		t.Fatalf("want events %v, got %v", want, types)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want error
	}{
		{"threshold zero", Config{Threshold: 0, Hysteresis: 1}, ErrThresholdNonPositive},
		{"threshold negative", Config{Threshold: -5, Hysteresis: 1}, ErrThresholdNonPositive},
		{"hysteresis zero", Config{Threshold: 100, Hysteresis: 0}, ErrHysteresisNonPositive},
		{"hysteresis negative", Config{Threshold: 100, Hysteresis: -1}, ErrHysteresisNonPositive},
		{"hysteresis equals threshold", Config{Threshold: 100, Hysteresis: 100}, ErrHysteresisNotBelowThld},
		{"hysteresis above threshold", Config{Threshold: 100, Hysteresis: 101}, ErrHysteresisNotBelowThld},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewMonitor(tc.cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("cfg=%+v: want %v, got %v", tc.cfg, tc.want, err)
			}
			t.Logf("cfg=%+v rejected: %v", tc.cfg, err)
		})
	}
}

func TestEmptyKeyRejected(t *testing.T) {
	m := newTestMonitor(t, 100, 10)
	res, err := m.Add("", 100)
	logStep(t, "", 100, res, err)
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	if len(m.Events()) != 0 {
		t.Fatal("rejected op must not emit events")
	}
}

func TestOverflowRejectedAtomically(t *testing.T) {
	m := newTestMonitor(t, 100, 10)

	res, err := m.Add("a", math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	logStep(t, "a", math.MaxInt64, res, nil) // 触发 alarm，值=MaxInt64

	before := m.Events()
	// 正向溢出：整体拒绝。
	if _, err := m.Add("a", 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 负向溢出：整体拒绝。
	if _, err := m.Add("b", math.MinInt64); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add("b", -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 失败不得改变任何键的值、状态与事件列表。
	if v := m.Value("a"); v != math.MaxInt64 {
		t.Fatalf("value changed after failed op: %d", v)
	}
	if s := m.State("a"); s != StateOpen {
		t.Fatalf("state changed after failed op: %s", s)
	}
	if v := m.Value("b"); v != math.MinInt64 {
		t.Fatalf("value of b changed after failed op: %d", v)
	}
	if got := m.Events(); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Fatalf("events changed after failed op: %v -> %v", before, got)
	}
	t.Logf("overflow rejected; value=%d state=%s events=%d unchanged", m.Value("a"), m.State("a"), len(m.Events()))
}

func TestReplayMatchesLiveRun(t *testing.T) {
	cfg := Config{Threshold: 100, Hysteresis: 10}
	steps := []Step{
		{Key: "x", Delta: 60},
		{Key: "y", Delta: 120},
		{Key: "x", Delta: 50},
		{Key: "y", Delta: -15},
		{Key: "x", Delta: -25},
		{Key: "y", Delta: -20},
	}
	m := newTestMonitor(t, cfg.Threshold, cfg.Hysteresis)
	for _, s := range steps {
		res, err := m.Add(s.Key, s.Delta)
		if err != nil {
			t.Fatal(err)
		}
		logStep(t, s.Key, s.Delta, res, nil)
	}
	replayed, err := Replay(cfg, steps)
	if err != nil {
		t.Fatal(err)
	}
	live := m.Events()
	if fmt.Sprint(live) != fmt.Sprint(replayed) {
		t.Fatalf("replay mismatch:\nlive=%v\nreplay=%v", live, replayed)
	}
	t.Logf("replay verified: %d events identical", len(replayed))
}

func TestReplayRejectsInvalidStep(t *testing.T) {
	_, err := Replay(Config{Threshold: 100, Hysteresis: 10}, []Step{{Key: "", Delta: 1}})
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
}

func TestConcurrentSameKey(t *testing.T) {
	m := newTestMonitor(t, 100, 10)
	const goroutines = 8
	const perG = 1000

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if _, err := m.Add("hot", 1); err != nil {
					t.Error(err)
				}
				if _, err := m.Add("hot", -1); err != nil {
					t.Error(err)
				}
				_ = m.Value("hot")
				_ = m.State("hot")
				_ = m.Events()
			}
		}(g)
	}
	wg.Wait()

	if v := m.Value("hot"); v != 0 {
		t.Fatalf("concurrent +1/-1 pairs: want final value 0, got %d", v)
	}
	// 边沿触发不变式：事件类型必须严格交替（alarm, clear, alarm, ...）。
	var prev *Event
	for i := range m.Events() {
		ev := &m.Events()[i]
		if prev != nil && ev.Type == prev.Type {
			t.Fatalf("events not alternating: seq %d and %d both %s", prev.Seq, ev.Seq, ev.Type)
		}
		if ev.Seq != uint64(i+1) {
			t.Fatalf("seq gap: want %d, got %d", i+1, ev.Seq)
		}
		prev = ev
	}
	t.Logf("concurrent run: %d events, strictly alternating, final value=%d", len(m.Events()), m.Value("hot"))
}

func TestConcurrentMatchesSerialReference(t *testing.T) {
	// 并发执行的最终累计值必须与串行参照一致。
	m := newTestMonitor(t, 100, 10)
	ref := newTestMonitor(t, 100, 10)

	keys := []string{"k1", "k2", "k3"}
	deltas := []int64{7, -3, 250, -400, 90}
	var wg sync.WaitGroup
	for _, k := range keys {
		for _, d := range deltas {
			d := d
			wg.Add(1)
			go func(key string) {
				defer wg.Done()
				if _, err := m.Add(key, d); err != nil {
					t.Error(err)
				}
			}(k)
		}
	}
	wg.Wait()

	for _, k := range keys {
		var sum int64
		for _, d := range deltas {
			if _, err := ref.Add(k, d); err != nil {
				t.Fatal(err)
			}
			sum += d
		}
		if got := m.Value(k); got != sum {
			t.Fatalf("key %s: concurrent value %d != serial reference %d", k, got, sum)
		}
		t.Logf("key %s: concurrent value=%d matches serial reference", k, sum)
	}
}
