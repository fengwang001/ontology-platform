package cep

import (
	"bytes"
	"errors"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func testConfig(mode Mode) Config {
	return Config{
		FirstType:  "A",
		SecondType: "B",
		Window:     10,
		MaxPending: 4,
		Mode:       mode,
	}
}

func bufferedConfig(mode Mode, buf *bytes.Buffer) Config {
	cfg := testConfig(mode)
	cfg.Logger = log.New(buf, "", 0)
	return cfg
}

func mustMatcher(t *testing.T, cfg Config) *Matcher {
	t.Helper()
	m, err := NewMatcher(cfg)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}
	return m
}

func mustBatch(t *testing.T, m *Matcher, events ...Event) []Match {
	t.Helper()
	matches, err := m.ProcessBatch(events)
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	return matches
}

func TestStrictBoundaryWindowExact(t *testing.T) {
	m := mustMatcher(t, testConfig(Strict))

	matches := mustBatch(t, m,
		Event{Key: "k", Type: "A", Time: 100},
		Event{Key: "k", Type: "B", Time: 110}, // delta == window, closed interval
	)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match at delta==window, got %d", len(matches))
	}
	if matches[0].Delta != 10 {
		t.Fatalf("expected delta 10, got %d", matches[0].Delta)
	}

	matches = mustBatch(t, m,
		Event{Key: "k", Type: "A", Time: 200},
		Event{Key: "k", Type: "B", Time: 211}, // delta == window+1
	)
	if len(matches) != 0 {
		t.Fatalf("expected no match beyond window, got %v", matches)
	}
}

func TestRelaxedBoundaryWindowExact(t *testing.T) {
	m := mustMatcher(t, testConfig(Relaxed))

	matches := mustBatch(t, m,
		Event{Key: "k", Type: "A", Time: 0},
		Event{Key: "k", Type: "X", Time: 5},
		Event{Key: "k", Type: "B", Time: 10}, // delta == window
	)
	if len(matches) != 1 || matches[0].Delta != 10 {
		t.Fatalf("expected 1 match with delta 10, got %v", matches)
	}

	matches = mustBatch(t, m,
		Event{Key: "k", Type: "A", Time: 20},
		Event{Key: "k", Type: "B", Time: 31}, // delta == window+1
	)
	if len(matches) != 0 {
		t.Fatalf("expected no match beyond window, got %v", matches)
	}
}

func TestStrictNotInterruptedByOtherKeys(t *testing.T) {
	m := mustMatcher(t, testConfig(Strict))

	matches := mustBatch(t, m,
		Event{Key: "k1", Type: "A", Time: 1},
		Event{Key: "k2", Type: "X", Time: 2},
		Event{Key: "k2", Type: "A", Time: 3},
		Event{Key: "k1", Type: "B", Time: 4},
	)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %v", matches)
	}
	if matches[0].Key != "k1" || matches[0].First.Time != 1 || matches[0].Second.Time != 4 {
		t.Fatalf("unexpected match: %+v", matches[0])
	}
}

func TestStrictBrokenBySameKeyIntervening(t *testing.T) {
	m := mustMatcher(t, testConfig(Strict))

	matches := mustBatch(t, m,
		Event{Key: "k", Type: "A", Time: 1},
		Event{Key: "k", Type: "X", Time: 2},
		Event{Key: "k", Type: "B", Time: 3},
	)
	if len(matches) != 0 {
		t.Fatalf("strict contiguity must be broken by same-key event, got %v", matches)
	}
}

func TestRelaxedSkipsInterveningAndPairsFIFO(t *testing.T) {
	m := mustMatcher(t, testConfig(Relaxed))

	matches := mustBatch(t, m,
		Event{Key: "k", Type: "A", Time: 1},
		Event{Key: "k", Type: "A", Time: 2},
		Event{Key: "k", Type: "X", Time: 3},
		Event{Key: "k", Type: "B", Time: 4},
	)
	// One first event pairs at most one second event; oldest pending first wins.
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %v", matches)
	}
	if matches[0].First.Time != 1 || matches[0].Second.Time != 4 {
		t.Fatalf("expected FIFO pairing A@1 with B@4, got %+v", matches[0])
	}

	matches = mustBatch(t, m, Event{Key: "k", Type: "B", Time: 5})
	if len(matches) != 1 || matches[0].First.Time != 2 {
		t.Fatalf("expected remaining A@2 to pair with B@5, got %v", matches)
	}

	if got := len(m.Pending("k")); got != 0 {
		t.Fatalf("expected empty pending queue, got %d", got)
	}
}

func TestInvalidParams(t *testing.T) {
	base := testConfig(Strict)

	cases := []struct {
		name string
		mut  func(*Config)
		want error
	}{
		{"zero window", func(c *Config) { c.Window = 0 }, ErrInvalidWindow},
		{"negative window", func(c *Config) { c.Window = -1 }, ErrInvalidWindow},
		{"zero max pending", func(c *Config) { c.MaxPending = 0 }, ErrInvalidMaxPending},
		{"negative max pending", func(c *Config) { c.MaxPending = -3 }, ErrInvalidMaxPending},
		{"unknown mode", func(c *Config) { c.Mode = Mode(7) }, ErrInvalidMode},
		{"empty first type", func(c *Config) { c.FirstType = "" }, ErrEmptyFirstType},
		{"empty second type", func(c *Config) { c.SecondType = "" }, ErrEmptySecondType},
		{"same types", func(c *Config) { c.SecondType = c.FirstType }, ErrTypeConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mut(&cfg)
			if _, err := NewMatcher(cfg); !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestRejectionsAreDistinguishableAndAtomic(t *testing.T) {
	cases := []struct {
		name  string
		batch []Event
		want  error
	}{
		{"empty key", []Event{{Key: "", Type: "A", Time: 1}}, ErrEmptyKey},
		{"empty type", []Event{{Key: "k", Type: "", Time: 1}}, ErrEmptyType},
		{"time regression", []Event{
			{Key: "k", Type: "A", Time: 5},
			{Key: "k", Type: "B", Time: 4},
		}, ErrTimeRegression},
		{"queue overflow", []Event{
			{Key: "k", Type: "A", Time: 1},
			{Key: "k", Type: "A", Time: 2},
			{Key: "k", Type: "A", Time: 3},
			{Key: "k", Type: "A", Time: 4},
			{Key: "k", Type: "A", Time: 5}, // exceeds MaxPending=4
		}, ErrQueueFull},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mustMatcher(t, testConfig(Relaxed))
			// Seed state that a rejected batch must not disturb.
			seeded := mustBatch(t, m,
				Event{Key: "k", Type: "A", Time: 0},
				Event{Key: "k", Type: "B", Time: 1},
			)
			if len(seeded) != 1 {
				t.Fatalf("seed failed: %v", seeded)
			}
			beforeMatches := m.Matches()
			beforeLast, _ := m.LastEvent("k")
			beforePending := m.Pending("k")

			// Prepend a valid event to prove partial progress is rolled back.
			batch := append([]Event{{Key: "k", Type: "A", Time: 2}}, tc.batch...)
			if tc.want == ErrTimeRegression || tc.want == ErrQueueFull {
				batch = tc.batch
			}
			if _, err := m.ProcessBatch(batch); !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}

			if !reflect.DeepEqual(m.Matches(), beforeMatches) {
				t.Fatalf("matches changed after rejection: %v", m.Matches())
			}
			afterLast, _ := m.LastEvent("k")
			if afterLast != beforeLast {
				t.Fatalf("last event changed after rejection: %+v -> %+v", beforeLast, afterLast)
			}
			if !reflect.DeepEqual(m.Pending("k"), beforePending) {
				t.Fatalf("pending queue changed after rejection: %v", m.Pending("k"))
			}
		})
	}
}

func TestEmptyBatchIsNoOp(t *testing.T) {
	m := mustMatcher(t, testConfig(Strict))
	matches, err := m.ProcessBatch(nil)
	if err != nil || len(matches) != 0 {
		t.Fatalf("empty batch: matches=%v err=%v", matches, err)
	}
}

func TestConcurrentReads(t *testing.T) {
	m := mustMatcher(t, testConfig(Relaxed))

	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = m.Matches()
				_ = m.Pending("k")
				_, _ = m.LastEvent("k")
			}
		}()
	}
	for i := int64(0); i < 200; i++ {
		if _, err := m.ProcessBatch([]Event{
			{Key: "k", Type: "A", Time: i * 2},
			{Key: "k", Type: "B", Time: i*2 + 1},
		}); err != nil {
			t.Fatalf("ProcessBatch: %v", err)
		}
	}
	wg.Wait()

	if got := len(m.Matches()); got != 200 {
		t.Fatalf("expected 200 matches, got %d", got)
	}
}

func TestDeterministicOutput(t *testing.T) {
	input := [][]Event{
		{
			{Key: "k1", Type: "A", Time: 1},
			{Key: "k2", Type: "A", Time: 1},
			{Key: "k1", Type: "X", Time: 2},
			{Key: "k1", Type: "B", Time: 3},
			{Key: "k2", Type: "B", Time: 12},
		},
		{
			{Key: "k1", Type: "A", Time: 5},
			{Key: "k1", Type: "B", Time: 15},
		},
	}

	run := func() []Match {
		m := mustMatcher(t, testConfig(Relaxed))
		for _, batch := range input {
			mustBatch(t, m, batch...)
		}
		return m.Matches()
	}

	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs:\nfirst=%v\ngot=%v", i, first, got)
		}
	}
}

func TestLogsContainInputPairsAndBasis(t *testing.T) {
	var buf bytes.Buffer
	m := mustMatcher(t, bufferedConfig(Relaxed, &buf))

	mustBatch(t, m,
		Event{Key: "k", Type: "A", Time: 1},
		Event{Key: "k", Type: "B", Time: 11},
	)
	mustBatch(t, m,
		Event{Key: "k", Type: "A", Time: 20},
		Event{Key: "k", Type: "B", Time: 31},
	)

	out := buf.String()
	for _, want := range []string{
		`input: key="k" type="A" time=1`,
		`paired: key="k"`,
		"delta=10 window=10",
		"expired: key=\"k\"",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}
