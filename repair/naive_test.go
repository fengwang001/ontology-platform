package repair_test

// A deliberately naive reference model, written directly from the
// specification text. It shares no logic with the real implementation:
// every count is recomputed by scanning, and every rule is spelled out
// literally. The randomized differential test replays identical operation
// sequences against both models and requires identical outcomes.

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/repair"
)

type naiveStripe struct {
	shards    []int // 0=Alive 1=Lost 2=Rebuilding
	firstLost int64 // -1 when unset
	inFlight  bool
	finish    int64
	targets   []int
	dead      bool
}

type naiveSim struct {
	k, n, tau        int
	A, R, cap_, D, Q int64
	clock            int64
	lastTick         int64
	tokens           int64
	stripes          map[int64]*naiveStripe
}

func newNaive(k, m, tau int, A, R, cap_, D, Q int64) *naiveSim {
	return &naiveSim{
		k: k, n: k + m, tau: tau,
		A: A, R: R, cap_: cap_, D: D, Q: Q,
		stripes: make(map[int64]*naiveStripe),
	}
}

func (sim *naiveSim) count(st *naiveStripe, what int) int {
	c := 0
	for _, sh := range st.shards {
		if sh == what {
			c++
		}
	}
	return c
}

func (sim *naiveSim) alive(st *naiveStripe) int { return sim.count(st, 0) }
func (sim *naiveSim) lost(st *naiveStripe) int  { return sim.count(st, 1) }

func (sim *naiveSim) inFlightCount() int {
	c := 0
	for _, st := range sim.stripes {
		if st.inFlight {
			c++
		}
	}
	return c
}

// Dead judgement: alive < k with no repair in flight. Dead is sticky.
func (sim *naiveSim) judgeDead(st *naiveStripe) {
	if !st.dead && !st.inFlight && sim.alive(st) < sim.k {
		st.dead = true
	}
}

func (sim *naiveSim) addStripe(now, id int64) error {
	if now < 0 || id <= 0 {
		return repair.ErrParam
	}
	if now < sim.clock {
		return repair.ErrClock
	}
	if _, ok := sim.stripes[id]; ok {
		return repair.ErrExists
	}
	sim.stripes[id] = &naiveStripe{shards: make([]int, sim.n), firstLost: -1}
	sim.clock = now
	return nil
}

func (sim *naiveSim) lose(now, id int64, shard int) error {
	if now < 0 || id <= 0 || shard < 0 || shard >= sim.n {
		return repair.ErrParam
	}
	if now < sim.clock {
		return repair.ErrClock
	}
	st, ok := sim.stripes[id]
	if !ok {
		return repair.ErrUnknown
	}
	if st.dead {
		return repair.ErrDead
	}
	if st.shards[shard] != 0 {
		return repair.ErrNotAlive
	}
	hadLostOrRebuilding := false
	for _, sh := range st.shards {
		if sh != 0 {
			hadLostOrRebuilding = true
		}
	}
	st.shards[shard] = 1
	if !hadLostOrRebuilding {
		st.firstLost = now
	}
	sim.judgeDead(st)
	sim.clock = now
	return nil
}

func (sim *naiveSim) tick(now int64) (repair.Report, error) {
	if now < 0 {
		return repair.Report{}, repair.ErrParam
	}
	if now < sim.clock {
		return repair.Report{}, repair.ErrClock
	}
	rep := repair.Report{Completed: []int64{}, Started: []repair.Start{}}

	// Step 1: completions, ordered by (finish, stripe id).
	var ids []int64
	for id, st := range sim.stripes {
		if st.inFlight && st.finish <= now {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		fi, fj := sim.stripes[ids[i]].finish, sim.stripes[ids[j]].finish
		if fi != fj {
			return fi < fj
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		st := sim.stripes[id]
		for _, sh := range st.targets {
			st.shards[sh] = 0
		}
		st.targets = nil
		st.inFlight = false
		sim.judgeDead(st)
		if sim.lost(st) == 0 {
			st.firstLost = -1
		}
		rep.Completed = append(rep.Completed, id)
	}

	// Step 2: token accrual.
	sim.tokens += sim.R * (now - sim.lastTick)
	if sim.tokens > sim.cap_ {
		sim.tokens = sim.cap_
	}
	sim.lastTick = now

	// Step 3: scheduling loop.
	for {
		if sim.inFlightCount() >= int(sim.Q) {
			break
		}
		best, found := sim.pick(now)
		if !found {
			break
		}
		if sim.tokens < int64(sim.k) {
			break
		}
		sim.tokens -= int64(sim.k)
		st := sim.stripes[best]
		targets := []int{}
		for sh := 0; sh < sim.n; sh++ {
			if st.shards[sh] == 1 {
				st.shards[sh] = 2
				targets = append(targets, sh)
			}
		}
		st.inFlight = true
		st.targets = targets
		st.finish = now + sim.D
		rep.Started = append(rep.Started, repair.Start{Stripe: best, Targets: targets, Finish: st.finish})
	}

	// Step 4: report.
	sim.clock = now
	return rep, nil
}

func (sim *naiveSim) pick(now int64) (int64, bool) {
	var bestID, bestFirst int64
	var bestTier, bestMargin int
	found := false
	for id, st := range sim.stripes {
		if st.dead || st.inFlight || sim.lost(st) == 0 {
			continue
		}
		margin := sim.alive(st) - sim.k
		aged := now-st.firstLost >= sim.A
		if margin > sim.tau && !aged {
			continue
		}
		tier := 1
		if aged {
			tier = 0
		}
		if !found ||
			tier < bestTier ||
			(tier == bestTier && margin < bestMargin) ||
			(tier == bestTier && margin == bestMargin && st.firstLost < bestFirst) ||
			(tier == bestTier && margin == bestMargin && st.firstLost == bestFirst && id < bestID) {
			found = true
			bestID, bestFirst, bestTier, bestMargin = id, st.firstLost, tier, margin
		}
	}
	return bestID, found
}

func (sim *naiveSim) status(id int64) (repair.Status, error) {
	if id <= 0 {
		return repair.Status{}, repair.ErrParam
	}
	st, ok := sim.stripes[id]
	if !ok {
		return repair.Status{}, repair.ErrUnknown
	}
	out := repair.Status{
		Alive:      sim.alive(st),
		Lost:       sim.lost(st),
		Rebuilding: sim.count(st, 2),
		FirstLost:  st.firstLost,
	}
	out.Margin = out.Alive - sim.k
	switch {
	case st.dead:
		out.State = repair.Dead
	case st.inFlight:
		out.State = repair.Repairing
	case out.Lost > 0:
		out.State = repair.Degraded
	default:
		out.State = repair.Healthy
	}
	return out, nil
}

// Replay 2000 randomized operation sequences against both the real
// scheduler and the naive model. Every operation, its outcome, and the
// cross-check verdict are logged.
func TestRandomSequencesAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 1))
		k := 1 + rng.Intn(5)
		m := 1 + rng.Intn(4)
		tau := rng.Intn(m + 1)
		A := int64(1 + rng.Intn(15))
		R := int64(1 + rng.Intn(6))
		cap_ := R + int64(rng.Intn(25))
		D := int64(1 + rng.Intn(5))
		Q := int64(1 + rng.Intn(3))

		real, err := repair.New(k, m, tau, A, R, cap_, D, Q)
		if err != nil {
			t.Fatalf("seq=%d: New: %v", seq, err)
		}
		naive := newNaive(k, m, tau, A, R, cap_, D, Q)
		n := k + m
		t.Logf("seq=%d 参数: k=%d m=%d tau=%d A=%d R=%d Cap=%d D=%d Q=%d",
			seq, k, m, tau, A, R, cap_, D, Q)

		var ids []int64
		now := int64(0)
		nextID := int64(1)
		const ops = 60
		for op := 0; op < ops; op++ {
			// Advance the clock; occasionally produce an invalid timestamp
			// to exercise ErrParam / ErrClock rejections.
			switch r := rng.Intn(20); {
			case r == 0:
				now-- // clock backwards
			case r == 1:
				now = -1 // negative
			default:
				now += int64(rng.Intn(6))
			}

			kind := rng.Intn(10)
			switch {
			case kind < 2: // AddStripe
				id := nextID
				if len(ids) > 0 && rng.Intn(4) == 0 {
					id = ids[rng.Intn(len(ids))] // duplicate
				}
				if rng.Intn(16) == 0 {
					id = 0 // non-positive
				}
				errReal := real.AddStripe(now, id)
				errNaive := naive.addStripe(now, id)
				if errReal != errNaive {
					t.Fatalf("seq=%d op=%d AddStripe(now=%d,id=%d): real=%v naive=%v",
						seq, op, now, id, errReal, errNaive)
				}
				if errReal == nil && id == nextID {
					ids = append(ids, id)
					nextID++
				}
				t.Logf("seq=%d op=%d AddStripe(now=%d,id=%d) -> %v, 两模型一致",
					seq, op, now, id, errReal)

			case kind < 6: // Lose
				id := int64(1 + rng.Intn(int(nextID+1))) // may be unknown
				shard := rng.Intn(n+2) - 1               // may be out of range
				errReal := real.Lose(now, id, shard)
				errNaive := naive.lose(now, id, shard)
				if errReal != errNaive {
					t.Fatalf("seq=%d op=%d Lose(now=%d,id=%d,shard=%d): real=%v naive=%v",
						seq, op, now, id, shard, errReal, errNaive)
				}
				t.Logf("seq=%d op=%d Lose(now=%d,id=%d,shard=%d) -> %v, 两模型一致",
					seq, op, now, id, shard, errReal)

			case kind < 9: // Tick
				repReal, errReal := real.Tick(now)
				repNaive, errNaive := naive.tick(now)
				if errReal != errNaive {
					t.Fatalf("seq=%d op=%d Tick(%d): real err=%v naive err=%v",
						seq, op, now, errReal, errNaive)
				}
				if errReal == nil && !reflect.DeepEqual(repReal, repNaive) {
					t.Fatalf("seq=%d op=%d Tick(%d):\nreal  %+v\nnaive %+v",
						seq, op, now, repReal, repNaive)
				}
				t.Logf("seq=%d op=%d Tick(now=%d) -> err=%v completed=%v started=%v, 两模型一致",
					seq, op, now, errReal, repReal.Completed, repReal.Started)

			default: // Status of a possibly unknown stripe
				id := int64(1 + rng.Intn(int(nextID+1)))
				stReal, errReal := real.Status(id)
				stNaive, errNaive := naive.status(id)
				if errReal != errNaive || stReal != stNaive {
					t.Fatalf("seq=%d op=%d Status(%d): real=(%+v,%v) naive=(%+v,%v)",
						seq, op, id, stReal, errReal, stNaive, errNaive)
				}
				t.Logf("seq=%d op=%d Status(id=%d) -> %+v err=%v, 两模型一致",
					seq, op, id, stReal, errReal)
			}

			if now < 0 {
				now = 0 // recover a usable clock for the next operation
			}

			// Cross-check the full observable state after every operation.
			if got, want := real.Tokens(), naive.tokens; got != want {
				t.Fatalf("seq=%d op=%d: Tokens real=%d naive=%d", seq, op, got, want)
			}
			for _, id := range ids {
				stReal, errReal := real.Status(id)
				stNaive, errNaive := naive.status(id)
				if errReal != errNaive || stReal != stNaive {
					t.Fatalf("seq=%d op=%d: Status(%d) real=(%+v,%v) naive=(%+v,%v)",
						seq, op, id, stReal, errReal, stNaive, errNaive)
				}
			}
		}
		t.Logf("seq=%d 判定: %d 个操作全部一致 (条带数=%d, tokens=%d)",
			seq, ops, len(ids), real.Tokens())
	}
}
