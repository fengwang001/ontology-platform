package ecrepair

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naive is a deliberately plain, step-by-step simulation of the scheduling
// rules, written directly from the spec. It shares no code with Scheduler
// and records a human-readable decision trace for every Tick.
type naive struct {
	k, n     int
	tau      int
	A        int
	R, Cap   int
	D, Q     int
	clock    int
	lastTick int
	tokens   int
	stripes  map[int]*naiveStripe
	trace    []string
}

type naiveStripe struct {
	shards    []int // 0 alive, 1 lost, 2 rebuilding
	firstLost int
	hasFirst  bool
	dead      bool
	repairing bool
	targets   []int
	finish    int
}

func newNaive(k, m, tau, A, R, Cap, D, Q int) *naive {
	return &naive{
		k: k, n: k + m, tau: tau, A: A,
		R: R, Cap: Cap, D: D, Q: Q,
		stripes: make(map[int]*naiveStripe),
	}
}

func (m *naive) aliveOf(st *naiveStripe) int {
	c := 0
	for _, sh := range st.shards {
		if sh == 0 {
			c++
		}
	}
	return c
}

func (m *naive) lostOf(st *naiveStripe) int {
	c := 0
	for _, sh := range st.shards {
		if sh == 1 {
			c++
		}
	}
	return c
}

func (m *naive) rebuildingOf(st *naiveStripe) int {
	c := 0
	for _, sh := range st.shards {
		if sh == 2 {
			c++
		}
	}
	return c
}

func (m *naive) addStripe(now, id int) error {
	if now < 0 || id <= 0 {
		return ErrParam
	}
	if now < m.clock {
		return ErrClock
	}
	if _, dup := m.stripes[id]; dup {
		return ErrExists
	}
	m.stripes[id] = &naiveStripe{shards: make([]int, m.n)}
	m.clock = now
	return nil
}

func (m *naive) lose(now, id, shard int) error {
	if now < 0 || id <= 0 || shard < 0 || shard >= m.n {
		return ErrParam
	}
	if now < m.clock {
		return ErrClock
	}
	st, ok := m.stripes[id]
	if !ok {
		return ErrUnknown
	}
	if st.dead {
		return ErrDead
	}
	if st.shards[shard] != 0 {
		return ErrNotAlive
	}
	if m.lostOf(st) == 0 && m.rebuildingOf(st) == 0 {
		st.firstLost = now
		st.hasFirst = true
	}
	st.shards[shard] = 1
	m.clock = now
	if !st.repairing && m.aliveOf(st) < m.k {
		st.dead = true
	}
	return nil
}

func (m *naive) tick(now int) (Report, error) {
	m.trace = m.trace[:0]
	if now < 0 {
		return Report{}, ErrParam
	}
	if now < m.clock {
		return Report{}, ErrClock
	}
	rep := Report{Completed: []int{}, Started: []RepairStart{}}

	// Step 1: finish repairs with finish <= now, ordered by (finish, id).
	type fin struct{ id, finish int }
	var fs []fin
	for id, st := range m.stripes {
		if st.repairing && st.finish <= now {
			fs = append(fs, fin{id, st.finish})
		}
	}
	sort.Slice(fs, func(i, j int) bool {
		if fs[i].finish != fs[j].finish {
			return fs[i].finish < fs[j].finish
		}
		return fs[i].id < fs[j].id
	})
	for _, f := range fs {
		st := m.stripes[f.id]
		for _, sh := range st.targets {
			st.shards[sh] = 0
		}
		st.repairing = false
		st.targets = nil
		rep.Completed = append(rep.Completed, f.id)
		m.trace = append(m.trace, fmt.Sprintf("complete id=%d finish=%d", f.id, f.finish))
		if !st.repairing && m.aliveOf(st) < m.k {
			st.dead = true
			m.trace = append(m.trace, fmt.Sprintf("id=%d alive<k after completion -> Dead", f.id))
		}
		if m.lostOf(st) == 0 {
			st.hasFirst = false
		}
	}

	// Step 2: token accumulation.
	gained := m.R * (now - m.lastTick)
	m.tokens += gained
	if m.tokens > m.Cap {
		m.tokens = m.Cap
	}
	m.lastTick = now
	m.trace = append(m.trace, fmt.Sprintf("tokens=%d (gained %d, cap %d)", m.tokens, gained, m.Cap))

	// Step 3: scheduling loop.
	for round := 0; ; round++ {
		inflight := 0
		for _, st := range m.stripes {
			if st.repairing {
				inflight++
			}
		}
		if inflight >= m.Q {
			m.trace = append(m.trace, fmt.Sprintf("round %d: stop, inflight=%d >= Q=%d", round, inflight, m.Q))
			break
		}
		best := -1
		var bestKey [4]int
		for id, st := range m.stripes {
			if st.dead || st.repairing || m.lostOf(st) == 0 {
				continue
			}
			margin := m.aliveOf(st) - m.k
			age := now - st.firstLost
			if margin > m.tau && age < m.A {
				continue
			}
			tier := 1
			if age >= m.A {
				tier = 0
			}
			key := [4]int{tier, margin, st.firstLost, id}
			if best == -1 || lessKey(key, bestKey) {
				best = id
				bestKey = key
			}
		}
		if best == -1 {
			m.trace = append(m.trace, fmt.Sprintf("round %d: stop, no candidate", round))
			break
		}
		m.trace = append(m.trace, fmt.Sprintf("round %d: candidate id=%d tier=%d margin=%d firstLost=%d",
			round, best, bestKey[0], bestKey[1], bestKey[2]))
		if m.tokens < m.k {
			m.trace = append(m.trace, fmt.Sprintf("round %d: stop, tokens=%d < k=%d", round, m.tokens, m.k))
			break
		}
		m.tokens -= m.k
		st := m.stripes[best]
		var targets []int
		for sh := 0; sh < m.n; sh++ {
			if st.shards[sh] == 1 {
				st.shards[sh] = 2
				targets = append(targets, sh)
			}
		}
		st.repairing = true
		st.targets = targets
		st.finish = now + m.D
		rep.Started = append(rep.Started, RepairStart{ID: best, Shards: targets, Finish: st.finish})
		m.trace = append(m.trace, fmt.Sprintf("round %d: start id=%d targets=%v finish=%d tokens=%d",
			round, best, targets, st.finish, m.tokens))
	}

	m.clock = now
	return rep, nil
}

func lessKey(a, b [4]int) bool {
	for i := 0; i < 4; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (m *naive) status(id int) (Status, error) {
	if id <= 0 {
		return Status{}, ErrParam
	}
	st, ok := m.stripes[id]
	if !ok {
		return Status{}, ErrUnknown
	}
	alive := m.aliveOf(st)
	lost := m.lostOf(st)
	rebuilding := m.rebuildingOf(st)
	var state StripeState
	switch {
	case st.dead:
		state = Dead
	case rebuilding > 0:
		state = Repairing
	case lost > 0:
		state = Degraded
	default:
		state = Healthy
	}
	first := -1
	if st.hasFirst {
		first = st.firstLost
	}
	return Status{
		State:      state,
		Alive:      alive,
		Lost:       lost,
		Rebuilding: rebuilding,
		Margin:     alive - m.k,
		FirstLost:  first,
	}, nil
}

func (m *naive) tokensNow() int {
	return m.tokens
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) && errors.Is(b, a)
}

// Replay 2000 random operation sequences against the naive simulation.
// Every sequence is also replayed on a second Scheduler instance to prove
// that identical inputs reproduce identical reports and states.
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seed := 0; seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		k := 1 + rng.Intn(4)
		m := 1 + rng.Intn(3)
		tau := rng.Intn(m + 1)
		A := 1 + rng.Intn(8)
		R := 1 + rng.Intn(4)
		Cap := R + rng.Intn(9)
		D := 1 + rng.Intn(4)
		Q := 1 + rng.Intn(3)

		real1, err := New(k, m, tau, A, R, Cap, D, Q)
		if err != nil {
			t.Fatalf("seed %d: New: %v", seed, err)
		}
		real2, err := New(k, m, tau, A, R, Cap, D, Q)
		if err != nil {
			t.Fatalf("seed %d: New: %v", seed, err)
		}
		sim := newNaive(k, m, tau, A, R, Cap, D, Q)

		t.Logf("seed=%d params k=%d m=%d tau=%d A=%d R=%d Cap=%d D=%d Q=%d",
			seed, k, m, tau, A, R, Cap, D, Q)

		now := 0
		const maxID = 4
		n := k + m
		for op := 0; op < 60; op++ {
			switch r := rng.Intn(100); {
			case r < 75:
				now += rng.Intn(4)
			case r < 80 && now > 0:
				now-- // clock regression attempt
			case r < 82:
				now = -1 // invalid clock
			}
			if now < 0 && rng.Intn(100) < 50 {
				now = 0
			}

			id := 1 + rng.Intn(maxID)
			if x := rng.Intn(100); x < 3 {
				id = 0
			} else if x < 6 {
				id = maxID + 3
			}

			kind := rng.Intn(100)
			if op < 3 { // seed some stripes first
				kind, id = 0, op+1
			}
			switch {
			case kind < 15: // AddStripe
				e1 := real1.AddStripe(now, id)
				e2 := real2.AddStripe(now, id)
				en := sim.addStripe(now, id)
				t.Logf("seed=%d op=%d AddStripe(now=%d id=%d) -> %v", seed, op, now, id, en)
				if !sameErr(e1, en) || !sameErr(e2, en) {
					t.Fatalf("seed=%d op=%d AddStripe: real=%v/%v naive=%v", seed, op, e1, e2, en)
				}
			case kind < 45: // Lose
				shard := rng.Intn(n + 1) // occasionally out of range
				e1 := real1.Lose(now, id, shard)
				e2 := real2.Lose(now, id, shard)
				en := sim.lose(now, id, shard)
				t.Logf("seed=%d op=%d Lose(now=%d id=%d shard=%d) -> %v", seed, op, now, id, shard, en)
				if !sameErr(e1, en) || !sameErr(e2, en) {
					t.Fatalf("seed=%d op=%d Lose: real=%v/%v naive=%v", seed, op, e1, e2, en)
				}
			case kind < 75: // Tick
				r1, e1 := real1.Tick(now)
				r2, e2 := real2.Tick(now)
				rn, en := sim.tick(now)
				t.Logf("seed=%d op=%d Tick(now=%d) -> completed=%v started=%v err=%v",
					seed, op, now, rn.Completed, rn.Started, en)
				for _, line := range sim.trace {
					t.Logf("seed=%d op=%d   why: %s", seed, op, line)
				}
				if !sameErr(e1, en) || !sameErr(e2, en) {
					t.Fatalf("seed=%d op=%d Tick err: real=%v/%v naive=%v", seed, op, e1, e2, en)
				}
				if en == nil {
					if !reflect.DeepEqual(r1, rn) || !reflect.DeepEqual(r2, rn) {
						t.Fatalf("seed=%d op=%d Tick report:\n real1=%+v\n real2=%+v\n naive=%+v",
							seed, op, r1, r2, rn)
					}
				}
			case kind < 90: // Status
				s1, e1 := real1.Status(id)
				s2, e2 := real2.Status(id)
				sn, en := sim.status(id)
				t.Logf("seed=%d op=%d Status(id=%d) -> %+v err=%v", seed, op, id, sn, en)
				if !sameErr(e1, en) || !sameErr(e2, en) {
					t.Fatalf("seed=%d op=%d Status err: real=%v/%v naive=%v", seed, op, e1, e2, en)
				}
				if en == nil && (s1 != sn || s2 != sn) {
					t.Fatalf("seed=%d op=%d Status: real=%+v/%+v naive=%+v", seed, op, s1, s2, sn)
				}
			default: // Tokens
				t1, t2, tn := real1.Tokens(), real2.Tokens(), sim.tokensNow()
				t.Logf("seed=%d op=%d Tokens() -> %d", seed, op, tn)
				if t1 != tn || t2 != tn {
					t.Fatalf("seed=%d op=%d Tokens: real=%d/%d naive=%d", seed, op, t1, t2, tn)
				}
			}
			if now < 0 {
				now = 0
			}
		}

		// Final full-state comparison over every stripe id.
		for id := 1; id <= maxID+3; id++ {
			s1, e1 := real1.Status(id)
			sn, en := sim.status(id)
			if !sameErr(e1, en) {
				t.Fatalf("seed=%d final Status(%d) err: real=%v naive=%v", seed, id, e1, en)
			}
			if en == nil && s1 != sn {
				t.Fatalf("seed=%d final Status(%d): real=%+v naive=%+v", seed, id, s1, sn)
			}
		}
		if t1, tn := real1.Tokens(), sim.tokensNow(); t1 != tn {
			t.Fatalf("seed=%d final Tokens: real=%d naive=%d", seed, t1, tn)
		}
		t.Logf("seed=%d final tokens=%d", seed, sim.tokensNow())
	}
}
