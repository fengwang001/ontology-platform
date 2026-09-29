package cep

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func testConfig(mode Mode) Config {
	return Config{FirstType: "A", SecondType: "B", Window: 10, MaxPending: 3, Mode: mode}
}

// newTestMatcher 返回一个把日志写入 buf 的匹配器，便于断言日志内容。
func newTestMatcher(t *testing.T, cfg Config, buf *bytes.Buffer) *Matcher {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	m, err := NewMatcher(cfg, logger)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}
	return m
}

func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want error
	}{
		{"empty first type", Config{FirstType: "", SecondType: "B", Window: 1, MaxPending: 1}, ErrEmptyEventType},
		{"empty second type", Config{FirstType: "A", SecondType: "", Window: 1, MaxPending: 1}, ErrEmptyEventType},
		{"same types", Config{FirstType: "A", SecondType: "A", Window: 1, MaxPending: 1}, ErrSameEventType},
		{"zero window", Config{FirstType: "A", SecondType: "B", Window: 0, MaxPending: 1}, ErrInvalidWindow},
		{"negative window", Config{FirstType: "A", SecondType: "B", Window: -5, MaxPending: 1}, ErrInvalidWindow},
		{"zero max pending", Config{FirstType: "A", SecondType: "B", Window: 1, MaxPending: 0}, ErrInvalidMaxPending},
		{"bad mode", Config{FirstType: "A", SecondType: "B", Window: 1, MaxPending: 1, Mode: Mode(99)}, ErrInvalidMode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewMatcher(tc.cfg, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRelaxedWindowBoundary(t *testing.T) {
	var buf bytes.Buffer
	m := newTestMatcher(t, testConfig(RelaxedContiguity), &buf)

	// 时间差恰好等于上限（闭区间）必须配对；超过上限 1 个单位不得配对。
	got, err := m.ProcessBatch([]Event{
		{Key: "k1", Type: "A", Timestamp: 100},
		{Key: "k1", Type: "B", Timestamp: 110}, // elapsed == window，应配对
		{Key: "k1", Type: "A", Timestamp: 200},
		{Key: "k1", Type: "B", Timestamp: 211}, // elapsed == window+1，不应配对
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1: %+v", len(got), got)
	}
	match := got[0]
	if match.Key != "k1" || match.First.Timestamp != 100 || match.Second.Timestamp != 110 || match.Elapsed != 10 {
		t.Fatalf("unexpected match: %+v", match)
	}

	logs := buf.String()
	for _, want := range []string{"process batch", "match", "basis", "k1"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestRelaxedPendingConsumedAndExpiry(t *testing.T) {
	var buf bytes.Buffer
	m := newTestMatcher(t, testConfig(RelaxedContiguity), &buf)

	got, err := m.ProcessBatch([]Event{
		{Key: "k1", Type: "A", Timestamp: 1},
		{Key: "k1", Type: "A", Timestamp: 2},
		{Key: "k1", Type: "X", Timestamp: 3}, // 宽松模式允许夹杂任意事件
		{Key: "k1", Type: "B", Timestamp: 5}, // 消费两个待匹配先事件
		{Key: "k1", Type: "B", Timestamp: 6}, // 队列已空，不再配对
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2: %+v", len(got), got)
	}
	if got[0].First.Timestamp != 1 || got[1].First.Timestamp != 2 {
		t.Fatalf("unexpected matches: %+v", got)
	}

	// 窗口过期：A@1 在 B@12 时已超出窗口（11 > 10），不得配对。
	got, err = m.ProcessBatch([]Event{
		{Key: "k2", Type: "A", Timestamp: 1},
		{Key: "k2", Type: "B", Timestamp: 12},
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d matches, want 0: %+v", len(got), got)
	}
	if !strings.Contains(buf.String(), "pending expired") {
		t.Fatalf("logs missing expiry basis:\n%s", buf.String())
	}
}

func TestStrictContiguity(t *testing.T) {
	var buf bytes.Buffer
	m := newTestMatcher(t, testConfig(StrictContiguity), &buf)

	got, err := m.ProcessBatch([]Event{
		{Key: "k1", Type: "A", Timestamp: 1},
		{Key: "k2", Type: "X", Timestamp: 2}, // 其他键的事件不打断 k1 的严格连续
		{Key: "k2", Type: "A", Timestamp: 3},
		{Key: "k1", Type: "B", Timestamp: 5},  // 紧挨 k1 的 A，配对
		{Key: "k2", Type: "B", Timestamp: 13}, // elapsed=10，恰好等于上限，配对
		{Key: "k1", Type: "A", Timestamp: 20},
		{Key: "k1", Type: "X", Timestamp: 21}, // 同键夹杂事件，打断严格连续
		{Key: "k1", Type: "B", Timestamp: 22}, // 不配对
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2: %+v", len(got), got)
	}
	if got[0].Key != "k1" || got[0].First.Timestamp != 1 || got[0].Second.Timestamp != 5 {
		t.Fatalf("unexpected match: %+v", got[0])
	}
	if got[1].Key != "k2" || got[1].Elapsed != 10 {
		t.Fatalf("unexpected match: %+v", got[1])
	}
}

func TestStrictWindowExceeded(t *testing.T) {
	var buf bytes.Buffer
	m := newTestMatcher(t, testConfig(StrictContiguity), &buf)

	got, err := m.ProcessBatch([]Event{
		{Key: "k1", Type: "A", Timestamp: 1},
		{Key: "k1", Type: "B", Timestamp: 12}, // elapsed=11 > window=10
	})
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d matches, want 0: %+v", len(got), got)
	}
	if !strings.Contains(buf.String(), "elapsed exceeds window") {
		t.Fatalf("logs missing window basis:\n%s", buf.String())
	}
}

func TestInvalidEventsRejectedAtomically(t *testing.T) {
	var buf bytes.Buffer
	m := newTestMatcher(t, testConfig(RelaxedContiguity), &buf)

	first, err := m.ProcessBatch([]Event{
		{Key: "k1", Type: "A", Timestamp: 10},
	})
	if err != nil || len(first) != 0 {
		t.Fatalf("setup batch: matches=%v err=%v", first, err)
	}

	cases := []struct {
		name   string
		events []Event
		want   error
	}{
		{"empty key", []Event{{Key: "", Type: "A", Timestamp: 11}}, ErrEmptyKey},
		{"empty type", []Event{{Key: "k1", Type: "", Timestamp: 11}}, ErrEmptyEventType},
		{"non-monotonic", []Event{{Key: "k1", Type: "B", Timestamp: 5}}, ErrNonMonotonicTime},
		{"non-monotonic within batch", []Event{
			{Key: "k1", Type: "X", Timestamp: 20},
			{Key: "k1", Type: "X", Timestamp: 19},
		}, ErrNonMonotonicTime},
		{"pending limit", []Event{
			{Key: "k1", Type: "A", Timestamp: 11},
			{Key: "k1", Type: "A", Timestamp: 12},
			{Key: "k1", Type: "A", Timestamp: 13}, // 已有 1 个待匹配，超限 3
		}, ErrPendingLimitExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := m.Matches()
			_, err := m.ProcessBatch(tc.events)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			// 被拒绝的批不得改变已输出的配对。
			after := m.Matches()
			if len(after) != len(before) {
				t.Fatalf("matches changed after rejection: before=%v after=%v", before, after)
			}
		})
	}

	// 被拒批不得改变队列与上一事件：k1 的 A@10 仍在队列中，
	// 且上一事件时间戳仍是 10（否则 B@15 会因窗口或单调性而失败）。
	got, err := m.ProcessBatch([]Event{{Key: "k1", Type: "B", Timestamp: 15}})
	if err != nil {
		t.Fatalf("ProcessBatch after rejections: %v", err)
	}
	if len(got) != 1 || got[0].First.Timestamp != 10 || got[0].Elapsed != 5 {
		t.Fatalf("state leaked from rejected batch: %+v", got)
	}
}

func TestDeterministicAndConcurrentRead(t *testing.T) {
	input := []Event{
		{Key: "k1", Type: "A", Timestamp: 1},
		{Key: "k2", Type: "A", Timestamp: 1},
		{Key: "k1", Type: "X", Timestamp: 2},
		{Key: "k2", Type: "B", Timestamp: 11},
		{Key: "k1", Type: "B", Timestamp: 11},
		{Key: "k1", Type: "A", Timestamp: 20},
		{Key: "k1", Type: "B", Timestamp: 30},
	}
	run := func() []Match {
		var buf bytes.Buffer
		m := newTestMatcher(t, testConfig(RelaxedContiguity), &buf)
		if _, err := m.ProcessBatch(input); err != nil {
			t.Fatalf("ProcessBatch: %v", err)
		}
		return m.Matches()
	}
	want := run()
	for i := 0; i < 5; i++ {
		got := run()
		if len(got) != len(want) {
			t.Fatalf("run %d: got %d matches, want %d", i, len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d match %d differs: got %+v want %+v", i, j, got[j], want[j])
			}
		}
	}

	// 并发读取与写入（-race 下验证）。
	var buf bytes.Buffer
	m := newTestMatcher(t, testConfig(RelaxedContiguity), &buf)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(key string, ts int64) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := m.ProcessBatch([]Event{
					{Key: key, Type: "A", Timestamp: ts},
					{Key: key, Type: "B", Timestamp: ts},
				}); err != nil {
					t.Errorf("ProcessBatch: %v", err)
					return
				}
				ts++
			}
		}(string(rune('a'+w)), int64(w*1000))
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				prev := m.Matches()
				cur := m.Matches()
				if len(cur) < len(prev) {
					t.Errorf("matches shrank: %d -> %d", len(prev), len(cur))
					return
				}
			}
		}()
	}
	wg.Wait()
}
