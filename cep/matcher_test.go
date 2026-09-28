package cep

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func ev(key, typ string, t, seq int64) Event {
	return Event{Key: key, Type: typ, Time: t, Sequence: seq}
}

func quietMatcher(mode Mode, maxPending int) *Matcher {
	m, _ := NewMatcher(Config{
		FirstType: "A", SecondType: "B", Window: 10, Mode: mode,
		MaxPending: maxPending,
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	return m
}

func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// 时间差恰好等于上限必须匹配（闭区间），超出 1 则不匹配。
func TestWindowClosedBoundary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  Mode
		extra []Event
	}{
		{"strict", Strict, nil},
		{"relaxed", Relaxed, []Event{ev("k", "X", 3, 30)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := quietMatcher(tc.mode, 0)
			batch := []Event{ev("k", "A", 0, 1)}
			batch = append(batch, tc.extra...)
			batch = append(batch, ev("k", "B", 10, 2))
			pairs, err := m.Process(batch)
			if err != nil {
				t.Fatalf("boundary match rejected: %v", err)
			}
			if len(pairs) != 1 || pairs[0].First.Time != 0 || pairs[0].Second.Time != 10 {
				t.Fatalf("expected single boundary pair, got %+v", pairs)
			}

			pairs, err = m.Process([]Event{ev("k", "A", 20, 3), ev("k", "B", 31, 4)})
			if err != nil {
				t.Fatal(err)
			}
			if len(pairs) != 0 {
				t.Fatalf("expected no pair beyond window, got %+v", pairs)
			}
		})
	}
}

// 宽松连续：中间夹任意事件，先事件按 FIFO 各配一个后事件。
func TestRelaxedWithIntermediates(t *testing.T) {
	var buf bytes.Buffer
	m, _ := NewMatcher(Config{FirstType: "A", SecondType: "B", Window: 100, Mode: Relaxed},
		captureLogger(&buf))

	pairs, err := m.Process([]Event{
		ev("k", "A", 1, 1),
		ev("k", "X", 2, 2),
		ev("k", "A", 3, 3),
		ev("k", "B", 4, 4),
		ev("k", "Y", 5, 5),
		ev("k", "B", 6, 6),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
	if pairs[0].First.Time != 1 || pairs[1].First.Time != 3 {
		t.Fatalf("unexpected FIFO pairing: %+v", pairs)
	}
	if m.Pending("k") != 0 {
		t.Fatalf("expected empty pending queue")
	}
	if !strings.Contains(buf.String(), "relaxed contiguity") {
		t.Fatalf("log missing decision rationale:\n%s", buf.String())
	}
}

// 严格连续：其他键的事件不得打断同键相邻关系；同键其他类型必须打断。
func TestStrictOtherKeyDoesNotBreak(t *testing.T) {
	m := quietMatcher(Strict, 0)

	pairs, err := m.Process([]Event{
		ev("k", "A", 1, 1),
		ev("other", "X", 2, 2),
		ev("k", "B", 3, 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 {
		t.Fatalf("cross-key event must not break strict adjacency, got %d pairs", len(pairs))
	}

	if _, err = m.Process([]Event{ev("k", "A", 4, 4), ev("k", "X", 5, 5)}); err != nil {
		t.Fatal(err)
	}
	pairs, err = m.Process([]Event{ev("k", "B", 6, 6)})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 {
		t.Fatalf("same-key intermediate must break strict adjacency, got %+v", pairs)
	}
}

// 严格连续：孤立后事件之后，新的先事件仍能开启新链。
func TestStrictBrokenThenNewChain(t *testing.T) {
	m := quietMatcher(Strict, 0)
	pairs, err := m.Process([]Event{
		ev("k", "B", 1, 1),
		ev("k", "A", 2, 2),
		ev("k", "B", 3, 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].Second.Time != 3 {
		t.Fatalf("expected one pair from new chain, got %+v", pairs)
	}
}

// 各类非法输入，拒绝原因可区分。
func TestRejectionReasons(t *testing.T) {
	badConfigs := []Config{
		{FirstType: "", SecondType: "B", Window: 1},
		{FirstType: "A", SecondType: "", Window: 1},
		{FirstType: "A", SecondType: "B", Window: -1},
		{FirstType: "A", SecondType: "B", Window: 1, Mode: Mode(9)},
		{FirstType: "A", SecondType: "A", Window: 1},
		{FirstType: "A", SecondType: "B", Window: 1, MaxPending: -1},
	}
	for i, cfg := range badConfigs {
		if _, err := NewMatcher(cfg, nil); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: expected ErrInvalidConfig, got %v", i, err)
		}
	}

	cases := []struct {
		name   string
		events []Event
		want   error
		index  int
	}{
		{"empty key", []Event{ev("", "A", 1, 1)}, ErrEmptyKey, 0},
		{"empty type", []Event{ev("k", "", 1, 1)}, ErrEmptyType, 0},
		{"time regression", []Event{
			ev("k", "A", 5, 1), ev("k", "B", 4, 2),
		}, ErrTimeRegression, 1},
		{"queue overflow", []Event{
			ev("k", "A", 1, 1), ev("k", "A", 2, 2), ev("k", "A", 3, 3),
		}, ErrQueueOverflow, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			m, _ := NewMatcher(Config{
				FirstType: "A", SecondType: "B", Window: 100,
				Mode: Relaxed, MaxPending: 2,
			}, captureLogger(&buf))
			pairs, err := m.Process(tc.events)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			var re *RejectError
			if !errors.As(err, &re) || re.Index != tc.index {
				t.Fatalf("expected reject index %d, got %v", tc.index, err)
			}
			if pairs != nil {
				t.Fatalf("rejected batch must return no pairs")
			}
			if !strings.Contains(buf.String(), "batch rejected") {
				t.Fatalf("rejection not logged:\n%s", buf.String())
			}
		})
	}
}

// 被拒绝的批次不得改变队列、上一个事件或已输出配对。
func TestRejectionAtomicity(t *testing.T) {
	m, _ := NewMatcher(Config{
		FirstType: "A", SecondType: "B", Window: 100, Mode: Relaxed, MaxPending: 5,
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	if _, err := m.Process([]Event{ev("k", "A", 1, 1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Process([]Event{ev("k", "A", 2, 2), ev("k", "B", 3, 3)}); err != nil {
		t.Fatal(err)
	}
	before := m.Pairs()

	if _, err := m.Process([]Event{
		ev("k", "A", 4, 4),
		ev("k", "A", 5, 5),
		ev("k", "A", 6, 6),
		ev("k", "A", 7, 7),
		ev("k", "B", 0, 8),
	}); !errors.Is(err, ErrQueueOverflow) {
		t.Fatalf("expected overflow first, got %v", err)
	}
	after := m.Pairs()
	if len(after) != len(before) {
		t.Fatalf("pairs changed after rejected batch: %d vs %d", len(after), len(before))
	}
	// A@1 仍待匹配，A@2 已配对；拒绝批次中的入队必须全部回滚。
	if got := m.Pending("k"); got != 1 {
		t.Fatalf("pending queue changed after rejected batch: %d", got)
	}
	// 上一个事件未变：被拒批次没有推进 last，t=8 相对 t=3 合法。
	pairs, err := m.Process([]Event{ev("k", "B", 8, 9)})
	if err != nil {
		t.Fatalf("state must be unchanged after rejection, got %v", err)
	}
	if len(pairs) != 1 || pairs[0].First.Time != 1 {
		t.Fatalf("expected oldest pending A to match after rollback, got %+v", pairs)
	}
}

// 同一输入序列反复计算结果完全一致（跨批重放）。
func TestDeterministicReplay(t *testing.T) {
	stream := [][]Event{
		{ev("k", "A", 1, 1), ev("other", "A", 2, 2)},
		{ev("k", "X", 3, 3), ev("k", "A", 4, 4)},
		{ev("k", "B", 5, 5), ev("other", "B", 6, 6)},
	}
	run := func() []Pair {
		m, _ := NewMatcher(Config{FirstType: "A", SecondType: "B", Window: 10, Mode: Relaxed},
			slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
		for _, batch := range stream {
			if _, err := m.Process(batch); err != nil {
				t.Fatal(err)
			}
		}
		return m.Pairs()
	}
	first := run()
	for i := 0; i < 5; i++ {
		got := run()
		if len(got) != len(first) {
			t.Fatalf("run %d: length mismatch", i)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d pair %d differs: %+v vs %+v", i, j, got[j], first[j])
			}
		}
	}
	if len(first) != 2 || first[0].First.Key != "k" || first[1].First.Key != "other" {
		t.Fatalf("unexpected pairs: %+v", first)
	}
}

// 并发写入串行化，读取快照逐字段一致。
func TestConcurrentReadSafety(t *testing.T) {
	m, _ := NewMatcher(Config{FirstType: "A", SecondType: "B", Window: 1000, Mode: Relaxed},
		slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = m.Process([]Event{
					ev("k", "A", int64(g*1000+i*2), int64(i)),
					ev("k", "B", int64(g*1000+i*2+1), int64(i)),
				})
			}
		}(g)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				for _, p := range m.Pairs() {
					if p.First.Type != "A" || p.Second.Type != "B" || p.First.Key != p.Second.Key {
						t.Errorf("inconsistent pair read: %+v", p)
						return
					}
				}
			}
		}()
	}
	wg.Wait()

	if len(m.Pairs()) == 0 {
		t.Fatal("expected some pairs under concurrent writers")
	}
}

// 日志必须打印输入、配对结果与判定依据。
func TestLoggingContents(t *testing.T) {
	var buf bytes.Buffer
	m, _ := NewMatcher(Config{FirstType: "A", SecondType: "B", Window: 10, Mode: Strict},
		captureLogger(&buf))

	if _, err := m.Process([]Event{
		ev("k", "A", 1, 1),
		ev("other", "X", 2, 2),
		ev("k", "B", 3, 3),
	}); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{
		"event input",
		"key=k",
		"pair matched",
		"strict adjacency satisfied",
		"diff=2",
		"window=10",
		"batch committed",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}
