package campaign

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// simDev is the naive simulation's per-device state.
type simDev struct {
	ver      int
	state    State
	attempts int
	readyAt  int64
	hop      int
	dl       int64
	tok      uint64
}

// sim is a deliberately naive step-by-step model of the specification:
// linear scans, sorting per operation, and whole-state snapshots for the
// rollback of rejected operations.
type sim struct {
	T, C, R, F int
	B, D       int64
	must       []int
	devs       map[string]*simDev
	maxNow     int64
	tok        uint64
	failed     int
	aborted    bool
	inflight   int
}

func newSim(T int, M []int, C, R int, B, D int64, F int) *sim {
	must := make([]int, len(M))
	copy(must, M)
	sort.Ints(must)
	return &sim{T: T, C: C, R: R, B: B, D: D, F: F, must: must, devs: map[string]*simDev{}, maxNow: -1}
}

func (s *sim) clone() *sim {
	cp := *s
	cp.devs = make(map[string]*simDev, len(s.devs))
	for k, d := range s.devs {
		dd := *d
		cp.devs[k] = &dd
	}
	return &cp
}

func (s *sim) next(v int) int {
	for _, m := range s.must {
		if m > v {
			return m
		}
	}
	return s.T
}

// settle applies every timeout with dl <= now in (dl, id) order; the
// failure time of a timeout is dl.
func (s *sim) settle(now int64) {
	type ent struct {
		id string
		dl int64
	}
	var list []ent
	for id, d := range s.devs {
		if d.state == InFlight && d.dl <= now {
			list = append(list, ent{id, d.dl})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].dl != list[j].dl {
			return list[i].dl < list[j].dl
		}
		return list[i].id < list[j].id
	})
	for _, e := range list {
		s.fail(s.devs[e.id], e.dl)
	}
}

func (s *sim) fail(d *simDev, t int64) {
	s.inflight--
	d.attempts++
	switch {
	case d.attempts >= s.R:
		d.state = Failed
		s.failed++
		if !s.aborted && s.failed >= s.F {
			s.aborted = true
			for _, d2 := range s.devs {
				if d2.state == Pending {
					d2.state = Cancelled
				}
			}
		}
	case s.aborted:
		d.state = Cancelled
	default:
		d.state = Pending
		d.readyAt = t + s.B*int64(d.attempts)
	}
}

func (s *sim) add(now int64, id string, v int) error {
	if len(id) == 0 || v < 1 || v > 1_000_000 || now < 0 || now > maxClock {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClockBack
	}
	snap := s.clone()
	s.settle(now)
	if _, ok := s.devs[id]; ok {
		*s = *snap
		return ErrExists
	}
	s.maxNow = now
	d := &simDev{ver: v, state: Pending, readyAt: now}
	if v >= s.T {
		d.state = Skipped
	} else if s.aborted {
		d.state = Cancelled
	}
	s.devs[id] = d
	return nil
}

func (s *sim) dispatch(now int64, n int) ([]Item, error) {
	if n < 1 || n > 10_000 || now < 0 || now > maxClock {
		return nil, ErrInvalid
	}
	if now < s.maxNow {
		return nil, ErrClockBack
	}
	snap := s.clone()
	s.settle(now)
	if s.aborted {
		*s = *snap
		return nil, ErrAborted
	}
	s.maxNow = now
	var ready []string
	for id, d := range s.devs {
		if d.state == Pending && d.readyAt <= now {
			ready = append(ready, id)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		di, dj := s.devs[ready[i]], s.devs[ready[j]]
		if di.readyAt != dj.readyAt {
			return di.readyAt < dj.readyAt
		}
		return ready[i] < ready[j]
	})
	items := []Item{}
	for _, id := range ready {
		if len(items) == n || s.inflight >= s.C {
			break
		}
		d := s.devs[id]
		d.hop = s.next(d.ver)
		d.dl = now + s.D
		s.tok++
		d.tok = s.tok
		d.state = InFlight
		s.inflight++
		items = append(items, Item{ID: []byte(id), Hop: d.hop, Tok: d.tok})
	}
	return items, nil
}

func (s *sim) report(now int64, id string, tok uint64, ver int, ok bool) error {
	if len(id) == 0 || tok < 1 || now < 0 || now > maxClock {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClockBack
	}
	snap := s.clone()
	s.settle(now)
	d, exists := s.devs[id]
	switch {
	case !exists:
		*s = *snap
		return ErrUnknown
	case d.state != InFlight:
		*s = *snap
		return ErrNotInFlight
	case d.tok != tok:
		*s = *snap
		return ErrStale
	case ok && ver != d.hop:
		*s = *snap
		return ErrVersion
	}
	s.maxNow = now
	if !ok {
		s.fail(d, now)
		return nil
	}
	s.inflight--
	d.ver = ver
	d.attempts = 0
	switch {
	case ver == s.T:
		d.state = Done
	case s.aborted:
		d.state = Cancelled
	default:
		d.state = Pending
		d.readyAt = now
	}
	return nil
}

// checkAgainstSim compares every observable field of the campaign with
// the naive simulation and verifies global invariants.
func checkAgainstSim(t *testing.T, c *Campaign, s *sim, ctx string) {
	t.Helper()
	if len(c.devices) != len(s.devs) {
		t.Fatalf("%s: device count %d != sim %d", ctx, len(c.devices), len(s.devs))
	}
	counts := map[State]int{}
	for id, sd := range s.devs {
		cd := c.devices[id]
		if cd == nil {
			t.Fatalf("%s: device %q missing in campaign", ctx, id)
		}
		if cd.ver != sd.ver || cd.state != sd.state || cd.attempts != sd.attempts ||
			cd.readyAt != sd.readyAt || cd.hop != sd.hop || cd.dl != sd.dl || cd.tok != sd.tok {
			t.Fatalf("%s: device %q diverged: campaign={ver:%d st:%v att:%d ra:%d hop:%d dl:%d tok:%d} "+
				"sim={ver:%d st:%v att:%d ra:%d hop:%d dl:%d tok:%d}",
				ctx, id, cd.ver, cd.state, cd.attempts, cd.readyAt, cd.hop, cd.dl, cd.tok,
				sd.ver, sd.state, sd.attempts, sd.readyAt, sd.hop, sd.dl, sd.tok)
		}
		counts[cd.state]++
	}
	total := 0
	for _, st := range []State{Pending, InFlight, Done, Failed, Skipped, Cancelled} {
		total += counts[st]
	}
	if total != len(c.devices) {
		t.Fatalf("%s: six-state count %d != device total %d", ctx, total, len(c.devices))
	}
	if c.failed != s.failed || c.aborted != s.aborted || c.tok != s.tok || c.maxNow != s.maxNow {
		t.Fatalf("%s: counters diverge: campaign={failed:%d aborted:%v tok:%d maxNow:%d} sim={failed:%d aborted:%v tok:%d maxNow:%d}",
			ctx, c.failed, c.aborted, c.tok, c.maxNow, s.failed, s.aborted, s.tok, s.maxNow)
	}
	if got := c.slots.Used(); got != s.inflight {
		t.Fatalf("%s: slots used %d != sim inflight %d", ctx, got, s.inflight)
	}
	if s.inflight > s.C {
		t.Fatalf("%s: inflight %d exceeds capacity %d", ctx, s.inflight, s.C)
	}
	if len(c.inflight) != s.inflight {
		t.Fatalf("%s: inflight heap %d != sim inflight %d", ctx, len(c.inflight), s.inflight)
	}
	pending := 0
	for _, d := range s.devs {
		if d.state == Pending {
			pending++
		}
	}
	if len(c.ready) != pending {
		t.Fatalf("%s: ready heap %d != sim pending %d", ctx, len(c.ready), pending)
	}
}

// TestRandomAgainstSimulation drives 1500 random operation sequences
// through both the campaign and the naive simulation, comparing outputs
// and full state after every step. Each step is logged with its input,
// output and the state facts that justify the verdict.
func TestRandomAgainstSimulation(t *testing.T) {
	const seeds = 1500
	pool := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		T := 2 + rng.Intn(40)
		var M []int
		seenM := map[int]bool{}
		for i := 0; i < rng.Intn(5); i++ {
			if m := 1 + rng.Intn(T-1); !seenM[m] {
				seenM[m] = true
				M = append(M, m)
			}
		}
		C := 1 + rng.Intn(4)
		R := 1 + rng.Intn(3)
		B := int64(1 + rng.Intn(20))
		D := int64(1 + rng.Intn(30))
		F := 1 + rng.Intn(5)
		c, err := New(T, M, C, R, B, D, F)
		if err != nil {
			t.Fatalf("seed=%d New: %v", seed, err)
		}
		s := newSim(T, M, C, R, B, D, F)
		t.Logf("seed=%d 参数: T=%d M=%v C=%d R=%d B=%d D=%d F=%d", seed, T, M, C, R, B, D, F)

		history := map[string][]int{}
		var now int64
		var lastTok uint64
		for step := 0; step < 80; step++ {
			now += int64(rng.Intn(25))
			opNow := now
			if rng.Intn(25) == 0 && now > 0 {
				opNow = now - 1 // occasional clock-back attempt
			}
			ctx := fmt.Sprintf("seed=%d step=%d", seed, step)
			switch rng.Intn(10) {
			case 0, 1, 2: // AddDevice
				id := pool[rng.Intn(len(pool))]
				v := 1 + rng.Intn(T+1)
				if rng.Intn(20) == 0 {
					v = 0 // invalid version
				}
				errC := c.AddDevice(opNow, []byte(id), v)
				errS := s.add(opNow, id, v)
				t.Logf("%s add now=%d id=%s v=%d -> %v (依据: sim=%v)", ctx, opNow, id, v, errC, errS)
				if errC != errS {
					t.Fatalf("%s: AddDevice err %v != sim %v", ctx, errC, errS)
				}
				if errC == nil {
					history[id] = []int{v}
				}
			case 3, 4, 5: // Dispatch
				n := 1 + rng.Intn(6)
				if rng.Intn(20) == 0 {
					n = 0 // invalid n
				}
				itemsC, errC := c.Dispatch(opNow, n)
				itemsS, errS := s.dispatch(opNow, n)
				t.Logf("%s dispatch now=%d n=%d -> items=%v err=%v (依据: sim items=%v err=%v)",
					ctx, opNow, n, itemsC, errC, itemsS, errS)
				if errC != errS {
					t.Fatalf("%s: Dispatch err %v != sim %v", ctx, errC, errS)
				}
				if len(itemsC) != len(itemsS) {
					t.Fatalf("%s: Dispatch %d items != sim %d", ctx, len(itemsC), len(itemsS))
				}
				for i := range itemsC {
					if !reflect.DeepEqual(itemsC[i], itemsS[i]) {
						t.Fatalf("%s: item %d %v != sim %v", ctx, i, itemsC[i], itemsS[i])
					}
					if itemsC[i].Tok != lastTok+1 {
						t.Fatalf("%s: token %d not contiguous after %d", ctx, itemsC[i].Tok, lastTok)
					}
					lastTok = itemsC[i].Tok
				}
			default: // Report
				id := pool[rng.Intn(len(pool))]
				if rng.Intn(15) == 0 {
					id = "unknown-x"
				}
				var tok uint64
				ver := 1
				ok := rng.Intn(2) == 0
				basis := "设备不存在或不在途"
				if sd, exists := s.devs[id]; exists && sd.state == InFlight {
					tok = sd.tok
					ver = sd.hop
					basis = fmt.Sprintf("在途 tok=%d hop=%d", sd.tok, sd.hop)
					if rng.Intn(4) == 0 {
						tok++ // stale token
						basis += ", 使用过期令牌"
					}
					if rng.Intn(3) == 0 {
						ver++ // wrong version
						basis += ", 版本不符"
					}
				} else {
					tok = uint64(rng.Intn(3))
				}
				errC := c.Report(opNow, []byte(id), tok, ver, ok)
				errS := s.report(opNow, id, tok, ver, ok)
				t.Logf("%s report now=%d id=%s tok=%d ver=%d ok=%v -> %v (依据: %s; sim=%v)",
					ctx, opNow, id, tok, ver, ok, errC, basis, errS)
				if errC != errS {
					t.Fatalf("%s: Report err %v != sim %v", ctx, errC, errS)
				}
				if errC == nil && ok {
					h := history[id]
					prev := h[len(h)-1]
					if ver <= prev {
						t.Fatalf("%s: device %s version %d not strictly increasing (prev %d)", ctx, id, ver, prev)
					}
					for _, m := range s.must {
						if prev < m && m < ver {
							t.Fatalf("%s: device %s skipped mandatory version %d (%d -> %d)", ctx, id, m, prev, ver)
						}
					}
					history[id] = append(h, ver)
				}
			}
			checkAgainstSim(t, c, s, ctx)
		}
	}
}
