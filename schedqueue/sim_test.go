package schedqueue

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// sim is a naive reference implementation of the queue semantics, written
// directly from the spec with plain maps and linear scans. The randomized
// test compares the heap-based Queue against it operation by operation.

const (
	locActive = iota
	locBackoff
	locUnsched
	locInFlight
)

type simPod struct {
	id     string
	prio   int
	t0     int64
	att    int
	loc    int
	exp    int64
	parked int64
	fb     int
	seq    int64
}

type sim struct {
	B, M, L int64
	maxNow  int64
	seq     int64
	events  []int
	pods    map[string]*simPod
}

func newSim(B, M, L int64) *sim {
	return &sim{B: B, M: M, L: L, pods: make(map[string]*simPod)}
}

func (s *sim) checkClock(now int64) (RejectReason, bool) {
	if now < s.maxNow {
		return RejectClockRegression, true
	}
	s.maxNow = now
	return 0, false
}

func simBackoff(B, M int64, att int) int64 {
	d := B
	for i := 1; i < att; i++ {
		if d >= M || d > M/2 {
			return M
		}
		d *= 2
	}
	if d > M {
		return M
	}
	return d
}

func simRelated(fb, ev int) bool { return fb == 0 || fb&ev != 0 }

func (s *sim) moveParked(p *simPod, now int64) {
	if now < p.exp {
		p.loc = locBackoff
	} else {
		p.loc = locActive
	}
}

func (s *sim) advance(now int64) {
	for _, p := range s.pods {
		if p.loc == locBackoff && p.exp <= now {
			p.loc = locActive
		}
	}
	for _, p := range s.pods {
		if p.loc == locUnsched && p.parked+s.L <= now {
			s.moveParked(p, now)
		}
	}
}

func (s *sim) add(id string, prio int, now int64) (RejectReason, bool) {
	if id == "" || now < 0 {
		return RejectInvalidArgument, true
	}
	if r, rej := s.checkClock(now); rej {
		return r, true
	}
	if _, ok := s.pods[id]; ok {
		return RejectDuplicateID, true
	}
	s.pods[id] = &simPod{id: id, prio: prio, t0: now, loc: locActive}
	return 0, false
}

func (s *sim) pop(now int64) (simPod, bool, RejectReason, bool) {
	if now < 0 {
		return simPod{}, false, RejectInvalidArgument, true
	}
	if r, rej := s.checkClock(now); rej {
		return simPod{}, false, r, true
	}
	s.advance(now)
	var best *simPod
	for _, p := range s.pods {
		if p.loc != locActive {
			continue
		}
		if best == nil || simLess(p, best) {
			best = p
		}
	}
	if best == nil {
		return simPod{}, false, 0, false
	}
	best.loc = locInFlight
	best.att++
	best.seq = s.seq
	return *best, true, 0, false
}

func simLess(a, b *simPod) bool {
	if a.prio != b.prio {
		return a.prio > b.prio
	}
	if a.t0 != b.t0 {
		return a.t0 < b.t0
	}
	return a.id < b.id
}

func (s *sim) done(id string, outcome Outcome, fb int, now int64) (RejectReason, bool) {
	if id == "" || now < 0 || (outcome != Scheduled && outcome != Failed) || fb < 0 || fb > 255 {
		return RejectInvalidArgument, true
	}
	if r, rej := s.checkClock(now); rej {
		return r, true
	}
	p, ok := s.pods[id]
	if !ok {
		return RejectPodNotFound, true
	}
	if p.loc != locInFlight {
		return RejectPodNotInFlight, true
	}
	if outcome == Scheduled {
		delete(s.pods, id)
		return 0, false
	}
	p.fb = fb
	p.exp = now + simBackoff(s.B, s.M, p.att)
	relatedSeen := false
	for i, ev := range s.events {
		if int64(i+1) > p.seq && simRelated(fb, ev) {
			relatedSeen = true
			break
		}
	}
	if relatedSeen {
		p.loc = locBackoff
	} else {
		p.loc = locUnsched
		p.parked = now
	}
	return 0, false
}

func (s *sim) event(ev int, now int64) (RejectReason, bool) {
	if now < 0 || ev < 1 || ev > 255 {
		return RejectInvalidArgument, true
	}
	if r, rej := s.checkClock(now); rej {
		return r, true
	}
	s.seq++
	s.events = append(s.events, ev)
	for _, p := range s.pods {
		if p.loc == locUnsched && simRelated(p.fb, ev) {
			s.moveParked(p, now)
		}
	}
	return 0, false
}

func (s *sim) advanceOp(now int64) (RejectReason, bool) {
	if now < 0 {
		return RejectInvalidArgument, true
	}
	if r, rej := s.checkClock(now); rej {
		return r, true
	}
	s.advance(now)
	return 0, false
}

func (s *sim) remove(id string) (RejectReason, bool) {
	if id == "" {
		return RejectInvalidArgument, true
	}
	if _, ok := s.pods[id]; !ok {
		return RejectPodNotFound, true
	}
	delete(s.pods, id)
	return 0, false
}

func (s *sim) sizes() (int, int, int, int) {
	var c [4]int
	for _, p := range s.pods {
		c[p.loc]++
	}
	return c[0], c[1], c[2], c[3]
}

func reasonOf(err error) (RejectReason, bool) {
	if err == nil {
		return 0, false
	}
	e, ok := err.(*Error)
	if !ok {
		panic(fmt.Sprintf("non-*Error rejection: %T %v", err, err))
	}
	return e.Reason, true
}

// TestRandomAgainstSim replays 2000 random operation sequences against both
// the Queue and the naive sim, requiring identical rejections, pop results
// and sizes after every single operation. Every operation is logged with
// its input, both outputs and the comparison verdict. Each sequence is also
// replayed on a second Queue to prove identical replay determinism.
func TestRandomAgainstSim(t *testing.T) {
	const sequences = 2000
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		B := 1 + rng.Int63n(1000)
		M := B + rng.Int63n(100000)
		L := 1 + rng.Int63n(100000)
		q, err := New(B, M, L)
		if err != nil {
			t.Fatalf("seed=%d New: %v", seed, err)
		}
		replay, err := New(B, M, L)
		if err != nil {
			t.Fatalf("seed=%d New replay: %v", seed, err)
		}
		s := newSim(B, M, L)
		now := int64(0)
		ops := 20 + rng.Intn(40)
		for i := 0; i < ops; i++ {
			now += rng.Int63n(30)
			opNow := now
			if rng.Intn(8) == 0 {
				opNow = now - rng.Int63n(40) // may regress or go negative
			}
			id := fmt.Sprintf("p%d", rng.Intn(8))
			if rng.Intn(20) == 0 {
				id = "" // invalid
			}
			step := fmt.Sprintf("seed=%d op=%d B=%d M=%d L=%d", seed, i, B, M, L)
			switch rng.Intn(6) {
			case 0: // Add
				prio := rng.Intn(5)
				qErr := q.Add(id, prio, opNow)
				rErr := replay.Add(id, prio, opNow)
				sr, sRej := s.add(id, prio, opNow)
				qr, qRej := reasonOf(qErr)
				t.Logf("%s Add(id=%q prio=%d now=%d) -> queue=%v sim=(%v,%v) match=%v",
					step, id, prio, opNow, qErr, sr, sRej, qr == sr && qRej == sRej)
				if qr != sr || qRej != sRej {
					t.Fatalf("%s Add: queue=(%v,%v) sim=(%v,%v)", step, qr, qRej, sr, sRej)
				}
				compareErr(t, step, "Add replay", qErr, rErr)
			case 1: // Pop
				qp, qok, qErr := q.Pop(opNow)
				rp, rok, rErr := replay.Pop(opNow)
				sp, sok, sr, sRej := s.pop(opNow)
				qr, qRej := reasonOf(qErr)
				match := qr == sr && qRej == sRej && qok == sok &&
					(!qok || (qp.ID == sp.id && qp.Prio == sp.prio && qp.T0 == sp.t0 && qp.Att == sp.att))
				t.Logf("%s Pop(now=%d) -> queue=(%+v,%v,%v) sim=(%+v,%v,(%v,%v)) match=%v",
					step, opNow, qp, qok, qErr, sp, sok, sr, sRej, match)
				if !match {
					t.Fatalf("%s Pop: queue=(%+v,%v,%v) sim=(%+v,%v,%v)",
						step, qp, qok, qErr, sp, sok, sr)
				}
				if rp != qp || rok != qok {
					t.Fatalf("%s Pop replay mismatch: %+v,%v vs %+v,%v", step, rp, rok, qp, qok)
				}
				compareErr(t, step, "Pop replay", qErr, rErr)
			case 2: // Done
				outcome := Outcome(rng.Intn(3)) // 2 is invalid
				fb := rng.Intn(260) - 2         // may be <0 or >255
				qErr := q.Done(id, outcome, fb, opNow)
				rErr := replay.Done(id, outcome, fb, opNow)
				sr, sRej := s.done(id, outcome, fb, opNow)
				qr, qRej := reasonOf(qErr)
				t.Logf("%s Done(id=%q outcome=%d fb=%d now=%d) -> queue=%v sim=(%v,%v) match=%v",
					step, id, outcome, fb, opNow, qErr, sr, sRej, qr == sr && qRej == sRej)
				if qr != sr || qRej != sRej {
					t.Fatalf("%s Done: queue=(%v,%v) sim=(%v,%v)", step, qr, qRej, sr, sRej)
				}
				compareErr(t, step, "Done replay", qErr, rErr)
			case 3: // Event
				ev := rng.Intn(260) // 0 and >255 are invalid
				qErr := q.Event(ev, opNow)
				rErr := replay.Event(ev, opNow)
				sr, sRej := s.event(ev, opNow)
				qr, qRej := reasonOf(qErr)
				t.Logf("%s Event(ev=%d now=%d) -> queue=%v sim=(%v,%v) match=%v",
					step, ev, opNow, qErr, sr, sRej, qr == sr && qRej == sRej)
				if qr != sr || qRej != sRej {
					t.Fatalf("%s Event: queue=(%v,%v) sim=(%v,%v)", step, qr, qRej, sr, sRej)
				}
				compareErr(t, step, "Event replay", qErr, rErr)
			case 4: // Advance
				qErr := q.Advance(opNow)
				rErr := replay.Advance(opNow)
				sr, sRej := s.advanceOp(opNow)
				qr, qRej := reasonOf(qErr)
				t.Logf("%s Advance(now=%d) -> queue=%v sim=(%v,%v) match=%v",
					step, opNow, qErr, sr, sRej, qr == sr && qRej == sRej)
				if qr != sr || qRej != sRej {
					t.Fatalf("%s Advance: queue=(%v,%v) sim=(%v,%v)", step, qr, qRej, sr, sRej)
				}
				compareErr(t, step, "Advance replay", qErr, rErr)
			case 5: // Remove
				qErr := q.Remove(id)
				rErr := replay.Remove(id)
				sr, sRej := s.remove(id)
				qr, qRej := reasonOf(qErr)
				t.Logf("%s Remove(id=%q) -> queue=%v sim=(%v,%v) match=%v",
					step, id, qErr, sr, sRej, qr == sr && qRej == sRej)
				if qr != sr || qRej != sRej {
					t.Fatalf("%s Remove: queue=(%v,%v) sim=(%v,%v)", step, qr, qRej, sr, sRej)
				}
				compareErr(t, step, "Remove replay", qErr, rErr)
			}
			qa, qb, qu, qf := q.Sizes()
			sa, sb, su, sf := s.sizes()
			ra, rb, ru, rf := replay.Sizes()
			if qa != sa || qb != sb || qu != su || qf != sf {
				t.Fatalf("%s Sizes: queue=(%d,%d,%d,%d) sim=(%d,%d,%d,%d)",
					step, qa, qb, qu, qf, sa, sb, su, sf)
			}
			if qa != ra || qb != rb || qu != ru || qf != rf {
				t.Fatalf("%s Sizes replay mismatch", step)
			}
			if qa+qb+qu+qf != len(s.pods) {
				t.Fatalf("%s invariant: sizes sum %d != live pods %d",
					step, qa+qb+qu+qf, len(s.pods))
			}
		}
		checkInvariant(t, q)
	}
}

func compareErr(t *testing.T, step, what string, a, b error) {
	t.Helper()
	ar, aRej := reasonOf(a)
	br, bRej := reasonOf(b)
	if ar != br || aRej != bRej {
		t.Fatalf("%s %s: (%v,%v) vs (%v,%v)", step, what, ar, aRej, br, bRej)
	}
}

// TestConcurrent hammers one queue from many goroutines; the result must be
// race-free and the structural invariant must hold afterwards.
func TestConcurrent(t *testing.T) {
	q := mustNew(t, 10, 1000, 5000)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				now := rng.Int63n(100000)
				id := fmt.Sprintf("g%d-p%d", seed, rng.Intn(20))
				switch rng.Intn(7) {
				case 0:
					_ = q.Add(id, rng.Intn(5), now)
				case 1:
					_, _, _ = q.Pop(now)
				case 2:
					_ = q.Done(id, Outcome(rng.Intn(2)), rng.Intn(256), now)
				case 3:
					_ = q.Event(1+rng.Intn(255), now)
				case 4:
					_ = q.Advance(now)
				case 5:
					_ = q.Remove(id)
				case 6:
					_, _, _, _ = q.Sizes()
				}
			}
		}(int64(g))
	}
	wg.Wait()
	checkInvariant(t, q)
}
