package ingest_test

import (
	"math/bits"
	"testing"

	"ontology/ingest"
)

func mustEngine(t *testing.T, p ingest.Params, devs ...string) *ingest.Engine {
	t.Helper()
	e, err := ingest.New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, d := range devs {
		if err := e.Register(d); err != nil {
			t.Fatalf("Register(%q): %v", d, err)
		}
	}
	return e
}

func stdParams() ingest.Params { return ingest.Params{Lm: 4, K: 2, Tq: 30, R: 2} }

func TestNewValidatesParams(t *testing.T) {
	bad := []ingest.Params{
		{Lm: 0, K: 1, Tq: 1, R: 1},
		{Lm: 10001, K: 1, Tq: 1, R: 1},
		{Lm: 1, K: 0, Tq: 1, R: 1},
		{Lm: 1, K: 101, Tq: 1, R: 1},
		{Lm: 1, K: 1, Tq: 0, R: 1},
		{Lm: 1, K: 1, Tq: 1_000_000_001, R: 1},
		{Lm: 1, K: 1, Tq: 1, R: 0},
		{Lm: 1, K: 1, Tq: 1, R: 11},
	}
	for i, p := range bad {
		if _, err := ingest.New(p); err != ingest.ErrInvalid {
			t.Fatalf("case %d (%+v): got %v want ErrInvalid", i, p, err)
		}
	}
	if _, err := ingest.New(stdParams()); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
}

func TestRegister(t *testing.T) {
	e := mustEngine(t, stdParams())
	if err := e.Register(""); err != ingest.ErrInvalid {
		t.Fatalf("empty name: got %v want ErrInvalid", err)
	}
	if err := e.Register("dev"); err != nil {
		t.Fatalf("register dev: %v", err)
	}
	if err := e.Register("dev"); err != ingest.ErrExists {
		t.Fatalf("duplicate: got %v want ErrExists", err)
	}
	if _, err := e.Stats("ghost"); err != ingest.ErrNoDevice {
		t.Fatalf("stats on ghost: got %v want ErrNoDevice", err)
	}
}

// TestErrorPrecedence checks that rejections are reported in the order
// ErrInvalid > ErrClockBack > ErrNoDevice > ErrRegress and change no state.
func TestErrorPrecedence(t *testing.T) {
	e := mustEngine(t, stdParams(), "dev")
	// Establish an accepted clock watermark at now=100.
	if err := e.Ingest("dev", 1, 100); err != nil {
		t.Fatalf("seed ingest: %v", err)
	}
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"invalid seq", func() error { return e.Ingest("dev", 0, 200) }, ingest.ErrInvalid},
		{"invalid seq too large", func() error { return e.Ingest("dev", ingest.MaxSeq+1, 200) }, ingest.ErrInvalid},
		{"invalid now negative", func() error { return e.Ingest("dev", 2, -1) }, ingest.ErrInvalid},
		{"invalid now too large", func() error { return e.Ingest("dev", 2, ingest.MaxNow+1) }, ingest.ErrInvalid},
		{"invalid beats clockback", func() error { return e.Ingest("dev", 0, 50) }, ingest.ErrInvalid},
		{"clockback", func() error { return e.Ingest("dev", 2, 99) }, ingest.ErrClockBack},
		{"clockback beats nodevice", func() error { return e.Ingest("ghost", 2, 50) }, ingest.ErrClockBack},
		{"nodevice", func() error { return e.Ingest("ghost", 2, 200) }, ingest.ErrNoDevice},
		{"hello invalid lo2", func() error { return e.Hello("dev", 0, 1, 200) }, ingest.ErrInvalid},
		{"hello invalid empty-range order", func() error { return e.Hello("dev", 5, 3, 200) }, ingest.ErrInvalid},
		{"hello clockback beats nodevice", func() error { return e.Hello("ghost", 1, 1, 50) }, ingest.ErrClockBack},
		{"hello nodevice beats regress", func() error { return e.Hello("ghost", 1, 0, 200) }, ingest.ErrNoDevice},
		{"hello regress lo", func() error { return e.Hello("dev", 1, 0, 200) }, ingest.ErrRegress},
	}
	for _, tc := range cases {
		if got := tc.op(); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
	// Rejected operations changed nothing: hi=1, lo=1, one received.
	st, err := e.Stats("dev")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	want := ingest.Stats{Hi: 1, Lo: 1, F: 1, Received: 1}
	if st != want {
		t.Fatalf("state mutated by rejected ops: got %+v want %+v", st, want)
	}
	// The clock watermark did not move either.
	if err := e.Ingest("dev", 2, 100); err != nil {
		t.Fatalf("now==maxNow must be accepted: %v", err)
	}
	if err := e.Ingest("dev", 3, 99); err != ingest.ErrClockBack {
		t.Fatalf("got %v want ErrClockBack", err)
	}
}
func TestIngestBasics(t *testing.T) {
	e := mustEngine(t, stdParams(), "dev")
	steps := []struct {
		seq  int64
		want ingest.Stats
	}{
		{1, ingest.Stats{Hi: 1, Lo: 1, F: 1, Received: 1}},
		{2, ingest.Stats{Hi: 2, Lo: 1, F: 2, Received: 2}},
		{2, ingest.Stats{Hi: 2, Lo: 1, F: 2, Received: 2, Dup: 1}},
		{5, ingest.Stats{Hi: 5, Lo: 1, F: 2, Received: 3, Missing: 2, Dup: 1}},
		{3, ingest.Stats{Hi: 5, Lo: 1, F: 3, Received: 4, Missing: 1, Dup: 1}},
		{4, ingest.Stats{Hi: 5, Lo: 1, F: 5, Received: 5, Dup: 1}},
	}
	for i, st := range steps {
		if err := e.Ingest("dev", st.seq, 0); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		got, err := e.Stats("dev")
		if err != nil {
			t.Fatalf("step %d stats: %v", i, err)
		}
		if got != st.want {
			t.Fatalf("step %d: got %+v want %+v", i, got, st.want)
		}
		if got.Received+got.Lost+got.Missing != got.Hi {
			t.Fatalf("step %d: conservation broken: %+v", i, got)
		}
		t.Logf("step=%d seq=%d stats=%+v 判定依据: 守恒式 Received+Lost+Missing==Hi 且 f 单调", i, st.seq, got)
	}
}

func TestHello(t *testing.T) {
	e := mustEngine(t, stdParams(), "dev")
	// hi=10, missing 4..9.
	for seq := int64(1); seq <= 3; seq++ {
		if err := e.Ingest("dev", seq, 0); err != nil {
			t.Fatalf("ingest %d: %v", seq, err)
		}
	}
	if err := e.Ingest("dev", 10, 0); err != nil {
		t.Fatalf("ingest 10: %v", err)
	}
	// Buffer advanced to [7,12]: 4..6 become Lost, 11..12 become Missing.
	if err := e.Hello("dev", 7, 12, 1); err != nil {
		t.Fatalf("hello: %v", err)
	}
	st, _ := e.Stats("dev")
	want := ingest.Stats{Hi: 12, Lo: 7, F: 6, Received: 4, Lost: 3, Missing: 5}
	if st != want {
		t.Fatalf("got %+v want %+v", st, want)
	}
	// Regressions are rejected.
	if err := e.Hello("dev", 6, 12, 2); err != ingest.ErrRegress {
		t.Fatalf("lo regress: got %v want ErrRegress", err)
	}
	if err := e.Hello("dev", 7, 11, 2); err != ingest.ErrRegress {
		t.Fatalf("hi regress: got %v want ErrRegress", err)
	}
	// Empty buffer declaration: lo2 == hi2+1, nothing new becomes missing.
	if err := e.Hello("dev", 13, 12, 3); err != nil {
		t.Fatalf("empty buffer hello: %v", err)
	}
	st, _ = e.Stats("dev")
	want = ingest.Stats{Hi: 12, Lo: 13, F: 12, Received: 4, Lost: 8}
	if st != want {
		t.Fatalf("empty buffer: got %+v want %+v", st, want)
	}
	// Hello may declare a buffer that starts above old hi+1: the skipped
	// fresh sequence numbers are lost immediately.
	if err := e.Hello("dev", 17, 20, 4); err != nil {
		t.Fatalf("hello skip: %v", err)
	}
	st, _ = e.Stats("dev")
	want = ingest.Stats{Hi: 20, Lo: 17, F: 16, Received: 4, Lost: 12, Missing: 4}
	if st != want {
		t.Fatalf("hello skip: got %+v want %+v", st, want)
	}
	t.Logf("final stats=%+v 判定依据: 小于 lo 的 Missing 全部 Lost, hi 推进产生新 Missing", st)
}

// TestVisitedBound proves that an in-order or in-gap Ingest examines at
// most 2*ceil(log2(g+2))+4 interval nodes, for g=10 and g=10000.
func TestVisitedBound(t *testing.T) {
	for _, g := range []int{10, 10000} {
		e := mustEngine(t, stdParams(), "dev")
		// Receive the odd sequence numbers 1,3,...,2g+1, leaving the g
		// singleton gaps 2,4,...,2g.
		for i := 0; i <= g; i++ {
			if err := e.Ingest("dev", int64(2*i+1), 0); err != nil {
				t.Fatalf("g=%d seed %d: %v", g, i, err)
			}
		}
		if got := e.GapSegments("dev"); got != g {
			t.Fatalf("g=%d: GapSegments=%d", g, got)
		}
		bound := 2*bits.Len(uint(g+1)) + 4 // 2*ceil(log2(g+2))+4

		e.ResetVisited("dev")
		if err := e.Ingest("dev", int64(2*g+2), 0); err != nil { // in-order
			t.Fatalf("g=%d in-order: %v", g, err)
		}
		inOrder := e.Visited("dev")

		e.ResetVisited("dev")
		if err := e.Ingest("dev", int64(2*(g/2)), 0); err != nil { // in-gap
			t.Fatalf("g=%d in-gap: %v", g, err)
		}
		inGap := e.Visited("dev")

		t.Logf("g=%d 按序到达 visited=%d 缺口内 visited=%d 上界=%d 判定依据: visited<=2*ceil(log2(g+2))+4",
			g, inOrder, inGap, bound)
		if inOrder > bound {
			t.Fatalf("g=%d: in-order visited %d > bound %d", g, inOrder, bound)
		}
		if inGap > bound {
			t.Fatalf("g=%d: in-gap visited %d > bound %d", g, inGap, bound)
		}
	}
}
