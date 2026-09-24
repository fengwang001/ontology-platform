// Package api is the outward-facing surface of the session-key merge.
package api

import (
	"errors"
	"fmt"

	"ontology/mrg"
	"ontology/seg"
)

// Event is one out-of-order event of a session.
type Event struct {
	Sid   string
	Seq   int
	Value int
}

// API is the entry point. Safe for concurrent use.
type API struct{ m *mrg.Merger }

// New returns a ready-to-use API.
func New() *API                              { return &API{m: mrg.New()} }
func (a *API) Append(ev Event) error         { return a.m.Append(ev.Sid, ev.Seq, ev.Value) }
func (a *API) Close(sid string, n int) error { return a.m.Close(sid, n) }

// Result returns the frozen result, whether the session is closed, and error.
func (a *API) Result(sid string) (int, bool, error) { return a.m.Result(sid) }

// SelfCheck verifies the four invariants on built-in operation sequences.
func (a *API) SelfCheck() error {
	// Invariants 3+4: idempotent replay; conflict/invalid input leave no trace.
	mg := mrg.New()
	for i := 0; i < 2; i++ {
		if err := mg.Append("x", 1, 7); err != nil {
			return fmt.Errorf("seed/replay: %w", err)
		}
	}
	for _, b := range []struct {
		sid    string
		seq, v int
		want   error
	}{
		{"x", 1, 8, seg.ErrConflict},
		{"", 1, 1, mrg.ErrEmptySid},
		{"x", 0, 1, seg.ErrBadSeq},
		{"x", 2, 10, seg.ErrBadValue},
	} {
		if err := mg.Append(b.sid, b.seq, b.v); !errors.Is(err, b.want) {
			return fmt.Errorf("append(%q,%d,%d): want %v, got %v", b.sid, b.seq, b.v, b.want, err)
		}
	}
	if got := fmt.Sprint(mg.Seen("x")); got != "[1]" {
		return fmt.Errorf("rejected appends left trace: seen=%s", got)
	}
	if err := mg.Close("x", 2); !errors.Is(err, seg.ErrIncomplete) {
		return fmt.Errorf("close with gap: want ErrIncomplete, got %v", err)
	}
	if err := mg.Close("x", 1); err != nil {
		return fmt.Errorf("session must stay usable: %w", err)
	}
	// Invariant 2: a frozen result never changes.
	mg2 := mrg.New()
	mg2.Append("s", 1, 4)
	mg2.Append("s", 2, 2)
	if err := mg2.Close("s", 2); err != nil {
		return err
	}
	g := []error{mg2.Append("s", 1, 4), mg2.Append("s", 3, 9), mg2.Close("s", 3), mg2.Close("s", 2)}
	if g[0] == nil || g[1] == nil || g[2] == nil || g[3] != nil {
		return fmt.Errorf("closed-session outcomes wrong: %v", g)
	}
	if r, c, _ := mg2.Result("s"); !c || r != 42 {
		return fmt.Errorf("frozen result changed: %d %v", r, c)
	}
	// Invariant 1: interleaved appends match naive batch recomputation.
	mg3, seed := mrg.New(), 12345
	next := func() int { seed = (seed*1103515245 + 12345) & 0x7fffffff; return seed }
	batch := map[string][]int{}
	var evs []Event
	for k := 0; k < 6; k++ {
		sid := fmt.Sprintf("s%d", k)
		vals := make([]int, k+3) // session k gets seq 1..k+2
		for i := 1; i <= k+2; i++ {
			vals[i] = next() % 10
			evs = append(evs, Event{sid, i, vals[i]})
		}
		batch[sid] = vals
	}
	for i := len(evs) - 1; i > 0; i-- {
		j := next() % (i + 1)
		evs[i], evs[j] = evs[j], evs[i]
	}
	for _, e := range evs {
		if err := mg3.Append(e.Sid, e.Seq, e.Value); err != nil {
			return fmt.Errorf("interleaved append: %w", err)
		}
		if e.Seq%3 == 0 {
			mg3.Append(e.Sid, e.Seq, e.Value) // idempotent replay mixed in
		}
	}
	for sid, vals := range batch {
		n, want := len(vals)-1, 0
		if err := mg3.Close(sid, n); err != nil {
			return fmt.Errorf("close %s: %w", sid, err)
		}
		for i := 1; i <= n; i++ {
			want = want*10 + vals[i]
		}
		if r, c, _ := mg3.Result(sid); !c || r != want {
			return fmt.Errorf("%s: batch=%d got=%d", sid, want, r)
		}
	}
	// The eight-step trace from NOTES.md (isAppend=true => Append else Close).
	mg4 := mrg.New()
	ops := []struct {
		isAppend       bool
		sid            string
		seq, val       int
		wantErr        error
		s1seen, s2seen string
		s1r, s2r       int
		s1c, s2c       bool
	}{
		{true, "s1", 2, 1, nil, "[2]", "[]", 0, 0, false, false},
		{true, "s2", 1, 3, nil, "[2]", "[1]", 0, 0, false, false},
		{true, "s1", 1, 5, nil, "[1 2]", "[1]", 0, 0, false, false},
		{true, "s1", 4, 8, nil, "[1 2 4]", "[1]", 0, 0, false, false},
		{true, "s1", 3, 2, nil, "[1 2 3 4]", "[1]", 0, 0, false, false},
		{false, "s1", 4, 0, nil, "[1 2 3 4]", "[1]", 5128, 0, true, false},
		{true, "s1", 5, 9, seg.ErrClosed, "[1 2 3 4]", "[1]", 5128, 0, true, false},
		{false, "s2", 1, 0, nil, "[1 2 3 4]", "[1]", 5128, 3, true, true},
	}
	for i, o := range ops {
		var err error
		if o.isAppend {
			err = mg4.Append(o.sid, o.seq, o.val)
		} else {
			err = mg4.Close(o.sid, o.seq)
		}
		r1, c1, _ := mg4.Result("s1")
		r2, c2, _ := mg4.Result("s2")
		if !errors.Is(err, o.wantErr) || fmt.Sprint(mg4.Seen("s1")) != o.s1seen ||
			fmt.Sprint(mg4.Seen("s2")) != o.s2seen ||
			r1 != o.s1r || c1 != o.s1c || r2 != o.s2r || c2 != o.s2c {
			return fmt.Errorf("trace step %d mismatch (err=%v)", i+1, err)
		}
	}
	return nil
}
