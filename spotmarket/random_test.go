package spotmarket

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

// simMarket is a naive step-by-step simulation written directly from the
// specification. It is intentionally independent from Market: fees are
// summed hour by hour and prices are looked up by a linear scan.
type simMarket struct {
	K, pmin int64
	maxT    int64
	seq     int64
	reqs    map[int64]*simReq
	hist    [][2]int64
}

type simReq struct {
	id         int64
	bid        int64
	seq        int64
	terminated bool
	running    bool
	segStart   int64
	bill       *big.Int
}

func newSim(K, pmin int64) *simMarket {
	return &simMarket{
		K:    K,
		pmin: pmin,
		reqs: make(map[int64]*simReq),
		hist: [][2]int64{{0, pmin}},
	}
}

func (s *simMarket) price(x int64) int64 {
	p := s.hist[0][1]
	for _, h := range s.hist {
		if h[0] <= x {
			p = h[1]
		}
	}
	return p
}

func (s *simMarket) fee(start, end int64, user bool) *big.Int {
	d := end - start
	var hours int64
	if user {
		hours = (d + HourSeconds - 1) / HourSeconds
	} else {
		hours = d / HourSeconds
	}
	total := new(big.Int)
	for k := int64(0); k < hours; k++ {
		total.Add(total, big.NewInt(s.price(start+HourSeconds*k)))
	}
	return total
}

func (s *simMarket) ranked() []*simReq {
	active := make([]*simReq, 0, len(s.reqs))
	for _, r := range s.reqs {
		if !r.terminated {
			active = append(active, r)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].bid != active[j].bid {
			return active[i].bid > active[j].bid
		}
		return active[i].seq < active[j].seq
	})
	return active
}

func (s *simMarket) endReq(r *simReq, t int64, user bool) Event {
	reason := MarketInterrupted
	if user {
		reason = UserTerminated
	}
	f := s.fee(r.segStart, t, user)
	r.bill.Add(r.bill, f)
	r.running = false
	return Event{Type: EventEnd, ID: r.id, Reason: reason, Fee: f, Start: r.segStart, End: t}
}

// recompute applies the ranking/price rules at time t and returns end
// events then start events, each sorted by id ascending.
func (s *simMarket) recompute(t int64) []Event {
	active := s.ranked()
	run := make(map[int64]bool)
	for i := 0; i < len(active) && int64(i) < s.K; i++ {
		run[active[i].id] = true
	}
	var ends, starts []Event
	for _, r := range s.reqs {
		if r.running && !run[r.id] {
			ends = append(ends, s.endReq(r, t, false))
		}
	}
	for id := range run {
		if r := s.reqs[id]; !r.running {
			r.running = true
			r.segStart = t
			starts = append(starts, Event{Type: EventStart, ID: id, Fee: new(big.Int), Start: t})
		}
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i].ID < ends[j].ID })
	sort.Slice(starts, func(i, j int) bool { return starts[i].ID < starts[j].ID })

	clearing := s.pmin
	if int64(len(active)) > s.K {
		clearing = active[s.K].bid
	}
	last := &s.hist[len(s.hist)-1]
	if last[1] != clearing {
		if last[0] == t {
			last[1] = clearing
		} else {
			s.hist = append(s.hist, [2]int64{t, clearing})
		}
	}
	return append(ends, starts...)
}

func (s *simMarket) request(id, bid, t int64) ([]Event, error) {
	if id < 0 || id > 1_000_000_000 || bid < s.pmin || bid > 1_000_000_000 ||
		t < 0 || t > 1_000_000_000_000_000 {
		return nil, ErrInvalidParam
	}
	if t < s.maxT {
		return nil, ErrClockRegression
	}
	if _, ok := s.reqs[id]; ok {
		return nil, ErrDuplicateID
	}
	s.reqs[id] = &simReq{id: id, bid: bid, seq: s.seq, bill: new(big.Int)}
	s.seq++
	evs := s.recompute(t)
	s.maxT = t
	return evs, nil
}

func (s *simMarket) terminate(id, t int64) ([]Event, error) {
	if id < 0 || id > 1_000_000_000 || t < 0 || t > 1_000_000_000_000_000 {
		return nil, ErrInvalidParam
	}
	if t < s.maxT {
		return nil, ErrClockRegression
	}
	r, ok := s.reqs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if r.terminated {
		return nil, ErrAlreadyTerminated
	}
	r.terminated = true
	var ends []Event
	if r.running {
		ends = append(ends, s.endReq(r, t, true))
	}
	evs := append(ends, s.recompute(t)...)
	// recompute already returns ends-then-starts; the user end event belongs
	// to the end group, so re-sort the end group by id.
	nEnds := len(ends)
	for _, e := range evs[len(ends):] {
		if e.Type == EventEnd {
			nEnds++
		}
	}
	sort.SliceStable(evs[:nEnds], func(i, j int) bool { return evs[i].ID < evs[j].ID })
	s.maxT = t
	return evs, nil
}

func (s *simMarket) setCapacity(k2, t int64) ([]Event, error) {
	if k2 < 1 || k2 > 1000 || t < 0 || t > 1_000_000_000_000_000 {
		return nil, ErrInvalidParam
	}
	if t < s.maxT {
		return nil, ErrClockRegression
	}
	s.K = k2
	evs := s.recompute(t)
	s.maxT = t
	return evs, nil
}

func (s *simMarket) bill(id int64) *big.Int {
	if r, ok := s.reqs[id]; ok {
		return r.bill
	}
	return new(big.Int)
}

// TestRandomSequencesAgainstNaiveSimulation replays 2000 random operation
// sequences against both Market and the naive simulation, comparing event
// lists, rejection reasons, bills, prices and running sets step by step.
// Inputs, outputs and the verdict basis are logged for every sequence.
func TestRandomSequencesAgainstNaiveSimulation(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		cap0 := int64(1 + rng.Intn(4))
		pmin := int64(1 + rng.Intn(50))
		m, err := New(cap0, pmin)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		sim := newSim(cap0, pmin)
		t.Logf("seq=%d init: K=%d Pmin=%d", seq, cap0, pmin)

		var tm int64
		nOps := 10 + rng.Intn(30)
		for op := 0; op < nOps; op++ {
			dt := rng.Int63n(4000)
			if rng.Intn(4) == 0 {
				dt = 0 // same-timestamp operations
			}
			tm += dt
			opT := tm
			if rng.Intn(20) == 0 && tm > 0 {
				opT = rng.Int63n(tm) // clock regression attempt
			}
			if rng.Intn(40) == 0 {
				opT = 1_000_000_000_000_001 // invalid t
			}

			var gotEvs, wantEvs []Event
			var gotErr, wantErr error
			var desc string
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4: // Request
				id := int64(rng.Intn(8))
				if rng.Intn(30) == 0 {
					id = -1 // invalid id
				}
				bid := pmin + rng.Int63n(60)
				switch rng.Intn(15) {
				case 0:
					bid = pmin - 1 // below floor (may still be valid if pmin==1? no: bid<pmin invalid)
				case 1:
					bid = 1_000_000_001 // above max
				}
				desc = fmt.Sprintf("Request(id=%d,bid=%d,t=%d)", id, bid, opT)
				gotEvs, gotErr = m.Request(id, bid, opT)
				wantEvs, wantErr = sim.request(id, bid, opT)
			case 5, 6, 7: // Terminate
				id := int64(rng.Intn(8))
				desc = fmt.Sprintf("Terminate(id=%d,t=%d)", id, opT)
				gotEvs, gotErr = m.Terminate(id, opT)
				wantEvs, wantErr = sim.terminate(id, opT)
			case 8: // SetCapacity
				k2 := int64(1 + rng.Intn(6))
				switch rng.Intn(10) {
				case 0:
					k2 = 0
				case 1:
					k2 = 1001
				}
				desc = fmt.Sprintf("SetCapacity(k=%d,t=%d)", k2, opT)
				gotEvs, gotErr = m.SetCapacity(k2, opT)
				wantEvs, wantErr = sim.setCapacity(k2, opT)
			case 9: // Bill query only
				id := int64(rng.Intn(8))
				desc = fmt.Sprintf("Bill(id=%d)", id)
				if g, w := m.Bill(id), sim.bill(id); g.Cmp(w) != 0 {
					t.Fatalf("seq %d op %d %s: Bill = %s, want %s", seq, op, desc, g, w)
				}
			}

			if (gotErr == nil) != (wantErr == nil) ||
				(gotErr != nil && !errors.Is(gotErr, wantErr)) {
				t.Fatalf("seq %d op %d %s: err = %v, want %v",
					seq, op, desc, gotErr, wantErr)
			}
			gs, ws := eventsString(gotEvs), eventsString(wantEvs)
			if gotErr == nil && gs != ws {
				t.Fatalf("seq %d op %d %s:\n got  %s\n want %s",
					seq, op, desc, gs, ws)
			}
			t.Logf("seq=%d op=%d %s -> events=%s err=%v (market==sim: %v)",
				seq, op, desc, gs, gotErr, gs == ws && errors.Is(gotErr, wantErr))

			// Invariants after every operation.
			m.mu.Lock()
			running := int64(0)
			var minRunBid int64 = 1_000_000_001
			for _, r := range m.reqs {
				if r.running {
					running++
					if r.bid < minRunBid {
						minRunBid = r.bid
					}
				}
				if r.bill.Sign() < 0 {
					t.Fatalf("seq %d op %d: negative bill for id %d", seq, op, r.id)
				}
			}
			if running > m.capacity {
				t.Fatalf("seq %d op %d: running %d > capacity %d",
					seq, op, running, m.capacity)
			}
			curPrice := m.history[len(m.history)-1].price
			if running > 0 && minRunBid < curPrice {
				t.Fatalf("seq %d op %d: running bid %d < clearing price %d",
					seq, op, minRunBid, curPrice)
			}
			m.mu.Unlock()
		}

		// Final comparison: bills of every id and prices at sample times.
		for id := int64(0); id < 8; id++ {
			if g, w := m.Bill(id), sim.bill(id); g.Cmp(w) != 0 {
				t.Fatalf("seq %d: final Bill(%d) = %s, want %s", seq, id, g, w)
			}
		}
		for _, x := range []int64{0, 1, 1800, 3600, tm / 2, tm} {
			if g, w := m.PriceAt(x), sim.price(x); g != w {
				t.Fatalf("seq %d: PriceAt(%d) = %d, want %d", seq, x, g, w)
			}
		}
		t.Logf("seq=%d final: bills and prices match naive simulation", seq)
	}
}
