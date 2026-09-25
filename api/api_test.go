package api_test

import (
	"errors"
	"testing"

	"ontology/api"
)

// TestEightStepSequence pins the section-3 derivation: the exact 8-op
// sequence, every step's four-tuple, and the post-restart replay to 15.
func TestEightStepSequence(t *testing.T) {
	p := api.New()
	type want struct {
		applied, flushed int64
		ckpt             api.Ckpt
		total            int64
	}
	none, c28 := api.Ckpt{}, api.Ckpt{Pos: 2, Total: 8, Valid: true}
	steps := []struct {
		name string
		op   func()
		want want
	}{
		{"Apply(1,+5)", func() { _ = p.Apply(1, 5) }, want{1, 0, none, 5}},
		{"Apply(2,+3)", func() { _ = p.Apply(2, 3) }, want{2, 0, none, 8}},
		{"Flush", p.Flush, want{2, 2, none, 8}},
		{"Apply(3,-1)", func() { _ = p.Apply(3, -1) }, want{3, 2, none, 7}},
		{"Checkpoint", p.Checkpoint, want{3, 2, c28, 7}},
		{"Apply(4,+6)", func() { _ = p.Apply(4, 6) }, want{4, 2, c28, 13}},
		{"Apply(5,+2)", func() { _ = p.Apply(5, 2) }, want{5, 2, c28, 15}},
		{"Restart", func() { _, _ = p.Restart() }, want{2, 2, c28, 8}},
	}
	for i, st := range steps {
		st.op()
		s := p.Snapshot()
		if got := (want{s.Applied, s.Flushed, s.Ckpt, s.Total}); got != st.want {
			t.Fatalf("step %d %s: got %+v, want %+v", i+1, st.name, got, st.want)
		}
	}
	c, err := p.Restart()
	if err != nil || c != c28 {
		t.Fatalf("restart: ckpt=%+v err=%v", c, err)
	}
	for pos, d := range []int64{-1, 6, 2} { // replay positions 3,4,5
		if err := p.Apply(int64(pos)+3, d); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.Snapshot().Total; got != 15 {
		t.Fatalf("replay total = %d, want 15 (no loss, no dup)", got)
	}
}

// TestSentinelErrors pins the three distinguishable failures and that each
// rejection leaves the whole state untouched.
func TestSentinelErrors(t *testing.T) {
	cases := []struct {
		name string
		op   func(p *api.Processor) error
		want error
	}{
		{"zero pos", func(p *api.Processor) error { return p.Apply(0, 1) }, api.ErrNonPositive},
		{"negative pos", func(p *api.Processor) error { return p.Apply(-4, 1) }, api.ErrNonPositive},
		{"gap pos", func(p *api.Processor) error { return p.Apply(7, 1) }, api.ErrGap},
		{"restart w/o ckpt", func(p *api.Processor) error { _, err := p.Restart(); return err }, api.ErrNoCheckpoint},
	}
	for _, distinguish := range [][2]error{
		{api.ErrNonPositive, api.ErrGap}, {api.ErrGap, api.ErrNoCheckpoint},
		{api.ErrNonPositive, api.ErrNoCheckpoint},
	} {
		if errors.Is(distinguish[0], distinguish[1]) {
			t.Fatalf("sentinels not distinct: %v vs %v", distinguish[0], distinguish[1])
		}
	}
	for _, c := range cases {
		p := api.New()
		if c.name != "restart w/o ckpt" { // give the instance some state to protect
			_ = p.Apply(1, 5)
			_ = p.Apply(2, 3)
			p.Flush()
			p.Checkpoint()
		}
		before := p.Snapshot()
		if err := c.op(p); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
		if got := p.Snapshot(); got != before {
			t.Fatalf("%s: state mutated: %+v -> %+v", c.name, before, got)
		}
	}
}

// TestRestartEquivalence: for random Apply/Flush/Checkpoint interleavings at
// several scales, restart + replay equals the naive sum of all applied
// deltas, and ckpt.pos stays within [0, flushed] and non-decreasing.
func TestRestartEquivalence(t *testing.T) {
	for _, seed := range []uint64{3, 11, 97, 1000, 424242} {
		p := api.New()
		var deltas []int64
		var sumAll, lastCkpt int64
		x := seed
		rnd := func() uint64 { x = x*6364136223846793005 + 1442695040888963407; return x >> 33 }
		for i := 0; i < 100+int(seed%400); i++ {
			switch rnd() % 3 {
			case 0:
				d := int64(rnd()%2001) - 1000
				if err := p.Apply(int64(len(deltas))+1, d); err != nil {
					t.Fatal(err)
				}
				deltas = append(deltas, d)
				sumAll += d
			case 1:
				p.Flush()
			case 2:
				p.Checkpoint()
			}
			s := p.Snapshot()
			if s.Flushed > s.Applied || (s.Ckpt.Valid && (s.Ckpt.Pos > s.Flushed || s.Ckpt.Pos < lastCkpt)) {
				t.Fatalf("seed %d: bounds/monotonicity broken: %+v", seed, s)
			}
			if s.Ckpt.Valid {
				lastCkpt = s.Ckpt.Pos
			}
		}
		p.Flush()
		p.Checkpoint()
		c, err := p.Restart()
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for pos := c.Pos + 1; pos <= int64(len(deltas)); pos++ {
			if err := p.Apply(pos, deltas[pos-1]); err != nil {
				t.Fatal(err)
			}
		}
		if got := p.Snapshot().Total; got != sumAll {
			t.Fatalf("seed %d: replay total %d != naive %d", seed, got, sumAll)
		}
	}
}

// TestSelfCheck requires the built-in self-check to pass.
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
