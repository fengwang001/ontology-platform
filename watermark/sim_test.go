package watermark

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// naive is a straightforward reference implementation written directly
// from the rules: it rescans the whole pane table on every Advance.
type naive struct {
	S, D    int64
	policy  Policy
	AL      int64
	Cap     int64
	I, O    int64
	panes   map[int64]*PaneInfo
	dropped int64
}

func newNaive(S, D int64, p Policy, AL, Cap int64) *naive {
	return &naive{S: S, D: D, policy: p, AL: AL, Cap: Cap, I: -1, O: -1, panes: map[int64]*PaneInfo{}}
}

func (n *naive) raw(ws int64, p *PaneInfo) int64 {
	switch n.policy {
	case Earliest:
		return p.MinTS
	case Latest:
		return p.MaxTS
	default:
		return ws + n.S - 1
	}
}

func (n *naive) add(ts, val int64) ([]LatePane, error) {
	if ts < 0 || ts > maxTS || val < -1_000_000_000 || val > 1_000_000_000 {
		return nil, ErrInvalidParam
	}
	kLo := ceilDiv(ts-n.S+1, n.D)
	kHi := floorDiv(ts, n.D)
	var lates []LatePane
	var buffered []int64
	var newPanes, drops int64
	for k := kLo; k <= kHi; k++ {
		ws := k * n.D
		end := ws + n.S
		switch {
		case n.I >= end+n.AL:
			drops++
		case n.I >= end:
			f := ts
			if n.policy == End {
				f = end - 1
			}
			lates = append(lates, LatePane{WS: ws, Count: 1, Sum: val, TS: max(f, n.O)})
		default:
			buffered = append(buffered, ws)
			if _, ok := n.panes[ws]; !ok {
				newPanes++
			}
		}
	}
	if int64(len(n.panes))+newPanes > n.Cap {
		return nil, ErrCapacity
	}
	n.dropped += drops
	for _, ws := range buffered {
		p, ok := n.panes[ws]
		if !ok {
			p = &PaneInfo{WS: ws, MinTS: ts, MaxTS: ts}
			n.panes[ws] = p
		}
		p.Count++
		p.Sum += val
		p.MinTS = min(p.MinTS, ts)
		p.MaxTS = max(p.MaxTS, ts)
		p.Hold = max(n.raw(ws, p), n.O)
	}
	return lates, nil
}

func (n *naive) advance(I2 int64) ([]OnTimePane, error) {
	if I2 < 0 || I2 > maxTS {
		return nil, ErrInvalidParam
	}
	if I2 < n.I {
		return nil, ErrRegression
	}
	n.I = I2
	var expired []int64
	for ws := range n.panes {
		if ws+n.S <= I2 {
			expired = append(expired, ws)
		}
	}
	sort.Slice(expired, func(i, j int) bool { return expired[i] < expired[j] })
	var out []OnTimePane
	for _, ws := range expired {
		p := n.panes[ws]
		delete(n.panes, ws)
		out = append(out, OnTimePane{WS: ws, Count: p.Count, Sum: p.Sum, TS: p.Hold})
	}
	if len(n.panes) == 0 {
		n.O = max(n.O, I2)
		return out, nil
	}
	var minHold int64
	first := true
	for _, p := range n.panes {
		if first || p.Hold < minHold {
			minHold = p.Hold
			first = false
		}
	}
	n.O = max(n.O, min(I2, minHold))
	return out, nil
}

func (n *naive) paneList() []PaneInfo {
	out := make([]PaneInfo, 0, len(n.panes))
	for _, p := range n.panes {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WS < out[j].WS })
	return out
}

func eqErr(a, b error) bool {
	return errors.Is(a, b) && errors.Is(b, a)
}

func checkInvariants(t *testing.T, m *Merger, prevO int64, ctx string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.O > m.I {
		t.Fatalf("%s: O=%d > I=%d", ctx, m.O, m.I)
	}
	if m.O < prevO {
		t.Fatalf("%s: O regressed %d -> %d", ctx, prevO, m.O)
	}
	if int64(len(m.panes)) > m.Cap {
		t.Fatalf("%s: table size %d > Cap %d", ctx, len(m.panes), m.Cap)
	}
	for ws, p := range m.panes {
		if p.hold < m.O {
			t.Fatalf("%s: pane ws=%d hold=%d < O=%d", ctx, ws, p.hold, m.O)
		}
	}
}

func TestNaiveComparison2000(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261002, 1179))
	for seq := 0; seq < 2000; seq++ {
		D := 1 + rng.Int64N(8)
		S := D + rng.Int64N(15*D)
		if S > 16*D {
			S = 16 * D
		}
		policy := Policy(rng.Int64N(3))
		AL := rng.Int64N(11)
		Cap := 1 + rng.Int64N(20)

		m, err := New(S, D, policy, AL, Cap)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		n := newNaive(S, D, policy, AL, Cap)

		var lastI int64 = -1
		var prevO int64 = -1
		ops := 30 + rng.Int64N(30)
		for op := int64(0); op < ops; op++ {
			ctx := fmt.Sprintf("seq %d op %d (S=%d D=%d pol=%d AL=%d Cap=%d)", seq, op, S, D, policy, AL, Cap)
			if rng.Int64N(2) == 0 {
				ts := rng.Int64N(400)
				val := rng.Int64N(201) - 100
				if rng.Int64N(20) == 0 {
					ts = -1 - rng.Int64N(3) // invalid
				}
				if rng.Int64N(20) == 0 {
					val = 1_000_000_001 // invalid
				}
				gotL, gotE := m.Add(ts, val)
				wantL, wantE := n.add(ts, val)
				if !eqErr(gotE, wantE) {
					t.Fatalf("%s: Add(%d,%d) err %v vs naive %v", ctx, ts, val, gotE, wantE)
				}
				if !reflect.DeepEqual(gotL, wantL) {
					t.Fatalf("%s: Add(%d,%d) lates %+v vs naive %+v", ctx, ts, val, gotL, wantL)
				}
				t.Logf("%s Add(%d,%d) -> lates=%+v err=%v | basis: window class by I vs end/end+AL, hold=max(raw,O)",
					ctx, ts, val, gotL, gotE)
			} else {
				var I2 int64
				switch rng.Int64N(10) {
				case 0:
					I2 = lastI // equal-I advance
				case 1:
					I2 = -1 - rng.Int64N(3) // invalid
				case 2:
					if lastI > 0 {
						I2 = rng.Int64N(lastI) // regression
					} else {
						I2 = 0
					}
				default:
					I2 = lastI + rng.Int64N(30)
					if I2 < 0 {
						I2 = rng.Int64N(30)
					}
				}
				gotO, gotE := m.Advance(I2)
				wantO, wantE := n.advance(I2)
				if !eqErr(gotE, wantE) {
					t.Fatalf("%s: Advance(%d) err %v vs naive %v", ctx, I2, gotE, wantE)
				}
				if !reflect.DeepEqual(gotO, wantO) {
					t.Fatalf("%s: Advance(%d) ontime %+v vs naive %+v", ctx, I2, gotO, wantO)
				}
				if gotE == nil {
					lastI = I2
				}
				t.Logf("%s Advance(%d) -> ontime=%+v err=%v | basis: expire end<=I' by ws asc, O=max(O,min(I',minHold))",
					ctx, I2, gotO, gotE)
			}
			if got, want := m.Output(), n.O; got != want {
				t.Fatalf("%s: O=%d vs naive %d", ctx, got, want)
			}
			if got, want := m.Dropped(), n.dropped; got != want {
				t.Fatalf("%s: dropped=%d vs naive %d", ctx, got, want)
			}
			if got, want := m.Panes(), n.paneList(); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: panes %+v vs naive %+v", ctx, got, want)
			}
			checkInvariants(t, m, prevO, ctx)
			prevO = m.Output()
		}
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() ([]OnTimePane, []LatePane, int64, int64, []PaneInfo) {
		m := mustNew(t, 12, 3, Latest, 4, 7)
		var ons []OnTimePane
		var lates []LatePane
		rng := rand.New(rand.NewPCG(7, 7))
		var I int64
		for i := 0; i < 200; i++ {
			if rng.Int64N(2) == 0 {
				l, _ := m.Add(rng.Int64N(300), rng.Int64N(21)-10)
				lates = append(lates, l...)
			} else {
				I += rng.Int64N(20)
				o, _ := m.Advance(I)
				ons = append(ons, o...)
			}
		}
		return ons, lates, m.Output(), m.Dropped(), m.Panes()
	}
	a1, b1, c1, d1, e1 := run()
	a2, b2, c2, d2, e2 := run()
	if !reflect.DeepEqual(a1, a2) || !reflect.DeepEqual(b1, b2) ||
		c1 != c2 || d1 != d2 || !reflect.DeepEqual(e1, e2) {
		t.Fatalf("replay mismatch")
	}
}

func TestConcurrency(t *testing.T) {
	m := mustNew(t, 8, 2, Earliest, 3, 1_000_000)
	const adders = 8
	const perAdder = 500

	var wg sync.WaitGroup
	var mu sync.Mutex
	var totalWindows int64
	var totalLates int64
	var totalOnTime int64
	prevO := int64(-1)

	for a := 0; a < adders; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			for i := 0; i < perAdder; i++ {
				ts := int64(a*perAdder + i)
				lates, err := m.Add(ts, int64(i%7)-3)
				if err != nil {
					t.Errorf("Add(%d): %v", ts, err)
					return
				}
				mu.Lock()
				totalWindows += int64(len(bruteWindows(ts, 8, 2)))
				totalLates += int64(len(lates))
				mu.Unlock()
			}
		}(a)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for I2 := int64(0); I2 <= adders*perAdder+16; I2 += 3 {
			out, err := m.Advance(I2)
			if err != nil {
				t.Errorf("Advance(%d): %v", I2, err)
				return
			}
			mu.Lock()
			for _, p := range out {
				totalOnTime += p.Count
			}
			mu.Unlock()
		}
	}()

	stop := make(chan struct{})
	var watchWg sync.WaitGroup
	watchWg.Add(1)
	go func() {
		defer watchWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			o := m.Output()
			mu.Lock()
			if o < prevO {
				t.Errorf("O regressed: %d -> %d", prevO, o)
			}
			prevO = max(prevO, o)
			mu.Unlock()
			for _, p := range m.Panes() {
				if p.Hold < o {
					t.Errorf("pane ws=%d hold=%d < O=%d", p.WS, p.Hold, o)
				}
			}
			_ = m.Dropped()
		}
	}()

	wg.Wait()
	close(stop)
	watchWg.Wait()

	// Drain everything.
	out, err := m.Advance(adders*perAdder + 16)
	if err != nil {
		t.Fatalf("final Advance: %v", err)
	}
	for _, p := range out {
		totalOnTime += p.Count
	}

	// Conservation: every element-window assignment is exactly one of
	// late-emitted (1 each), on-time-emitted (pane count), still
	// buffered (pane count), or dropped.
	var remaining int64
	for _, p := range m.Panes() {
		remaining += p.Count
	}
	sum := totalLates + totalOnTime + remaining + m.Dropped()
	if sum != totalWindows {
		t.Fatalf("conservation: lates=%d ontime=%d remaining=%d dropped=%d total=%d want %d",
			totalLates, totalOnTime, remaining, m.Dropped(), sum, totalWindows)
	}
	if m.Output() > adders*perAdder+16 {
		t.Fatalf("O=%d exceeds final I", m.Output())
	}
}
