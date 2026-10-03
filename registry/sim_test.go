package registry

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func TestRunNumbersContinuous(t *testing.T) {
	r, _ := New(5, 100)
	for now := int64(0); now < 300; now++ {
		id := []byte{byte(now % 7)}
		run, err := r.Start(id, []byte("p"), int(now%3), int((now/3)%3), now)
		if err == nil && (run < 1 || run > r.nextRun) {
			t.Fatalf("now=%d run=%d 越界", now, run)
		}
		if err == nil && run == r.nextRun && now%2 == 0 {
			r.Finish(id, run, stCompleted, now)
		}
	}
	t.Logf("已分配 run 1..%d 连续无空洞", r.nextRun)
}

func TestPurgeProbeBudget(t *testing.T) {
	for _, extra := range []int{0, 9999} {
		t.Run(fmt.Sprintf("ids=%d", extra+1), func(t *testing.T) {
			r, _ := New(100, 1_000_000)
			for i := 0; i <= extra; i++ {
				id := []byte(fmt.Sprintf("x%05d", i))
				run, _ := r.Start(id, []byte("p"), 2, 0, 0)
				if err := r.Finish(id, run, stCompleted, 0); err != nil {
					t.Fatal(err)
				}
			}
			r.purgeProbes()
			if n, _ := r.Count(50); n != extra+1 {
				t.Fatalf("n=%d want %d", n, extra+1)
			}
			if probes := r.purgeProbes(); probes != 1 {
				t.Fatalf("无到期 probes=%d want 1", probes)
			}
			n, _ := r.Count(100)
			probes := r.purgeProbes()
			if n != 0 || probes < extra+1 || probes > extra+2 {
				t.Fatalf("n=%d probes=%d 超到期+1", n, probes)
			}
		})
	}
}

type simRec struct {
	run          int64
	owner, state int
	ended        int64
}

type naiveModel struct {
	R, clock, nextRun int64
	N                 int
	recs              map[int]*simRec
	admins            map[int]bool
}

func (m *naiveModel) live(id int, now int64) *simRec {
	e := m.recs[id]
	if e != nil && e.state != stRunning && now >= e.ended+m.R {
		delete(m.recs, id)
		return nil
	}
	return e
}

func (m *naiveModel) count(now int64) int {
	n := 0
	for id := range m.recs {
		if m.live(id, now) != nil {
			n++
		}
	}
	return n
}

func (m *naiveModel) start(id, p, reuse, conflict int, now int64) (int64, error) {
	if now < m.clock {
		return 0, ErrClock
	}
	e := m.live(id, now)
	if e != nil && e.state == stRunning {
		if conflict == 0 {
			return 0, ErrRunning
		}
		if conflict == 1 {
			m.clock = now
			return e.run, nil
		}
		if p != e.owner && !m.admins[p] {
			return 0, ErrDenied
		}
		e.state, e.ended = stTerminated, now
	} else if e != nil {
		if reuse == 2 || (reuse == 1 && e.state == stCompleted) {
			return 0, ErrReuse
		}
	} else if m.count(now) >= m.N {
		return 0, ErrCapacity
	}
	m.nextRun++
	m.recs[id] = &simRec{run: m.nextRun, owner: p, state: stRunning}
	m.clock = now
	return m.nextRun, nil
}

func (m *naiveModel) finish(id int, run int64, state int, now int64) error {
	if state < stCompleted || state > stCancelled {
		return ErrInvalid
	}
	if now < m.clock {
		return ErrClock
	}
	e := m.live(id, now)
	switch {
	case e == nil:
		return ErrNotFound
	case e.run != run:
		return ErrStale
	case e.state != stRunning:
		return ErrNotRunning
	}
	e.state, e.ended, m.clock = state, now, now
	return nil
}

func TestRandomAgainstNaive(t *testing.T) {
	const R, N, rounds = int64(37), 5, 1000
	r, _ := New(R, N)
	m := &naiveModel{R: R, N: N, recs: map[int]*simRec{}, admins: map[int]bool{}}
	rng := rand.New(rand.NewSource(1))
	errEq := func(a, b error) bool {
		return (a == nil && b == nil) || (a != nil && b != nil && errors.Is(a, b))
	}
	for step := 0; step < rounds; step++ {
		now := m.clock + int64(rng.Intn(4))
		id, p := rng.Intn(7), rng.Intn(4)
		idb, pb := []byte{byte('A' + id)}, []byte{byte('a' + p)}
		if rng.Intn(10) == 0 {
			if rng.Intn(2) == 0 {
				r.Grant(pb)
				m.admins[p] = true
			} else {
				r.Revoke(pb)
				delete(m.admins, p)
			}
			continue
		}
		if rng.Intn(10) < 3 {
			var run int64
			if e := m.recs[id]; e != nil {
				run = e.run
				if rng.Intn(3) == 0 {
					run--
				}
			}
			state := []int{stCompleted, stFailed, stCancelled, stTerminated}[rng.Intn(4)]
			got, want := r.Finish(idb, run, state, now), m.finish(id, run, state, now)
			t.Logf("step %d Finish(%d,%d,st%d,%d)=%v 依据 NotFound/Stale/NotRunning", step, id, run, state, now, got)
			if !errEq(got, want) {
				t.Fatalf("step %d Finish %v want %v", step, got, want)
			}
		} else {
			reuse, conflict := rng.Intn(3), rng.Intn(3)
			gr, ge := r.Start(idb, pb, reuse, conflict, now)
			wr, we := m.start(id, p, reuse, conflict, now)
			t.Logf("step %d Start(%d,p%d,r%d,c%d,%d)=(%d,%v)", step, id, p, reuse, conflict, now, gr, ge)
			if gr != wr || !errEq(ge, we) {
				t.Fatalf("step %d got=(%d,%v) want=(%d,%v)", step, gr, ge, wr, we)
			}
		}
		gn, _ := r.Count(now)
		if gn != m.count(now) || r.nextRun != m.nextRun || r.clock != m.clock {
			t.Fatalf("step %d 状态发散 count(%d,%d) run(%d,%d) clock(%d,%d)",
				step, gn, m.count(now), r.nextRun, m.nextRun, r.clock, m.clock)
		}
	}
}
