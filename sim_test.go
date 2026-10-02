package ontology

import (
	"errors"
	"fmt"
	"strings"
)

type simRun struct {
	id      int64
	size    int64
	created int64
	busy    bool
	fails   int
}

type simPlan struct {
	id     int64
	reason string
	runs   []int64 // newest-first
	total  int64
	basis  string
}

// naiveSim is an independent, line-by-line transcription of the four rules,
// used as an oracle in randomized differential tests.
type naiveSim struct {
	cfg      Config
	runs     []simRun // newest-first
	nextID   int64
	nextPlan int64
	lastNow  int64
	active   map[int64][]int64
	ended    map[int64]bool
	probes   int
	log      []string
}

func newNaiveSim(cfg Config) *naiveSim {
	return &naiveSim{
		cfg:      cfg,
		nextID:   1,
		nextPlan: 1,
		active:   map[int64][]int64{},
		ended:    map[int64]bool{},
	}
}

func (s *naiveSim) recordf(format string, args ...any) {
	s.log = append(s.log, fmt.Sprintf(format, args...))
}

func (s *naiveSim) addRun(now, size int64) (int64, error) {
	if size < 1 || size > 1_000_000_000_000 || now < 0 {
		s.recordf("AddRun(now=%d,size=%d) -> ErrParam", now, size)
		return 0, ErrParam
	}
	if now < s.lastNow {
		s.recordf("AddRun(now=%d,size=%d) -> ErrClock(last=%d)", now, size, s.lastNow)
		return 0, ErrClock
	}
	id := s.nextID
	s.nextID++
	s.runs = append([]simRun{{id: id, size: size, created: now}}, s.runs...)
	s.lastNow = now
	s.recordf("AddRun(now=%d,size=%d) -> id=%d", now, size, id)
	return id, nil
}

func (s *naiveSim) pick(now int64) (simPlan, error) {
	if now < 0 {
		s.recordf("Pick(now=%d) -> ErrParam", now)
		return simPlan{}, ErrParam
	}
	if now < s.lastNow {
		s.recordf("Pick(now=%d) -> ErrClock(last=%d)", now, s.lastNow)
		return simPlan{}, ErrClock
	}
	if len(s.active) >= s.cfg.Cmax {
		s.recordf("Pick(now=%d) -> ErrBusy(active=%d)", now, len(s.active))
		return simPlan{}, ErrBusy
	}
	n := len(s.runs)
	s.probes = 0

	// Rule 1: SpaceAmp.
	if n >= s.cfg.MinRuns {
		anyBusy := false
		var others int64
		for i := 0; i < n; i++ {
			if s.runs[i].busy {
				anyBusy = true
			}
			if i < n-1 {
				others += s.runs[i].size
			}
		}
		oldest := s.runs[n-1].size
		lhs, rhs := others*100, s.cfg.A*oldest
		if !anyBusy && lhs >= rhs {
			p := s.buildPlan(now, ReasonSpaceAmp, 0, n,
				fmt.Sprintf("SpaceAmp E=%d S=%d E*100=%d >= A*S=%d", others, oldest, lhs, rhs))
			s.recordf("Pick(now=%d) -> #%d %s ids=%v total=%d | %s",
				now, p.id, p.reason, p.runs, p.total, p.basis)
			return p, nil
		}
	}

	// Rule 2: SizeRatio.
	if n >= s.cfg.MinRuns {
		for i := 0; i < n; i++ {
			s.probes++
			st := s.runs[i]
			if st.busy || st.fails >= 2 {
				continue
			}
			acc := st.size
			count := 1
			j := i + 1
			examined := 1
			for j < n && count < s.cfg.MaxMerge && examined < s.cfg.MaxMerge {
				s.probes++
				examined++
				nxt := s.runs[j]
				if nxt.busy || nxt.fails >= 2 {
					break
				}
				if nxt.size*100 > acc*(100+s.cfg.Rho) {
					break
				}
				acc += nxt.size
				count++
				j++
			}
			if count >= s.cfg.MinMerge {
				p := s.buildPlan(now, ReasonSizeRatio, i, j,
					fmt.Sprintf("SizeRatio start=%d count=%d acc=%d probes=%d",
						st.id, count, acc, s.probes))
				s.recordf("Pick(now=%d) -> #%d %s ids=%v total=%d | %s",
					now, p.id, p.reason, p.runs, p.total, p.basis)
				return p, nil
			}
		}
	}

	// Rule 3: CountReduce.
	if n > s.cfg.MaxRuns {
		c := n - s.cfg.MaxRuns + 1
		if c > s.cfg.MaxMerge {
			c = s.cfg.MaxMerge
		}
		for i := 0; i+c <= n; i++ {
			ok := true
			for k := 0; k < c; k++ {
				if s.runs[i+k].busy {
					ok = false
					break
				}
			}
			if ok {
				p := s.buildPlan(now, ReasonCountReduce, i, i+c,
					fmt.Sprintf("CountReduce n=%d c=min(%d,%d)=%d window@%d",
						n, n-s.cfg.MaxRuns+1, s.cfg.MaxMerge, c, i))
				s.recordf("Pick(now=%d) -> #%d %s ids=%v total=%d | %s",
					now, p.id, p.reason, p.runs, p.total, p.basis)
				return p, nil
			}
		}
	}

	// Rule 4: Periodic.
	if s.cfg.P > 0 {
		for i := n - 1; i >= 0; i-- {
			r := s.runs[i]
			age := now - r.created
			if !r.busy && age >= s.cfg.P {
				p := s.buildPlan(now, ReasonPeriodic, i, i+1,
					fmt.Sprintf("Periodic id=%d created=%d age=%d>=P=%d (oldest position)",
						r.id, r.created, age, s.cfg.P))
				s.recordf("Pick(now=%d) -> #%d %s ids=%v total=%d | %s",
					now, p.id, p.reason, p.runs, p.total, p.basis)
				return p, nil
			}
		}
	}

	s.recordf("Pick(now=%d) -> ErrNotNeeded (probes=%d)", now, s.probes)
	return simPlan{}, ErrNotNeeded
}

func (s *naiveSim) buildPlan(now int64, reason string, lo, hi int, basis string) simPlan {
	p := simPlan{id: s.nextPlan, reason: reason, basis: basis}
	for i := lo; i < hi; i++ {
		p.runs = append(p.runs, s.runs[i].id)
		p.total += s.runs[i].size
		s.runs[i].busy = true
	}
	s.active[p.id] = append([]int64(nil), p.runs...)
	s.nextPlan++
	s.lastNow = now
	return p
}

func (s *naiveSim) done(now, planID, out int64) (simRun, error) {
	if out < 1 || out > 1_000_000_000_000 || now < 0 {
		s.recordf("Done(now=%d,plan=%d,out=%d) -> ErrParam", now, planID, out)
		return simRun{}, ErrParam
	}
	if now < s.lastNow {
		s.recordf("Done(now=%d,plan=%d,out=%d) -> ErrClock(last=%d)", now, planID, out, s.lastNow)
		return simRun{}, ErrClock
	}
	ids, ok := s.active[planID]
	if !ok {
		s.recordf("Done(now=%d,plan=%d,out=%d) -> ErrUnknown", now, planID, out)
		return simRun{}, ErrUnknown
	}
	lo, hi := s.findSegment(ids)
	id := s.nextID
	s.nextID++
	nr := simRun{id: id, size: out, created: now}
	updated := make([]simRun, 0, len(s.runs)-len(ids)+1)
	updated = append(updated, s.runs[:lo]...)
	updated = append(updated, nr)
	updated = append(updated, s.runs[hi:]...)
	s.runs = updated
	delete(s.active, planID)
	s.ended[planID] = true
	s.lastNow = now
	s.recordf("Done(now=%d,plan=%d,out=%d) -> new id=%d at [%d,%d)", now, planID, out, id, lo, hi)
	return nr, nil
}

func (s *naiveSim) abort(planID int64) error {
	ids, ok := s.active[planID]
	if !ok {
		s.recordf("Abort(plan=%d) -> ErrUnknown", planID)
		return ErrUnknown
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for i := range s.runs {
		if want[s.runs[i].id] {
			s.runs[i].busy = false
			s.runs[i].fails++
		}
	}
	delete(s.active, planID)
	s.ended[planID] = true
	s.recordf("Abort(plan=%d) -> ids=%v cleared busy, fails++", planID, ids)
	return nil
}

func (s *naiveSim) findSegment(ids []int64) (int, int) {
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	lo := -1
	for i, r := range s.runs {
		if want[r.id] {
			if lo < 0 {
				lo = i
			}
		} else if lo >= 0 {
			return lo, i
		}
	}
	return lo, len(s.runs)
}

func (s *naiveSim) snapshot() []simRun {
	out := make([]simRun, len(s.runs))
	copy(out, s.runs)
	return out
}

func (s *naiveSim) logText() string {
	return strings.Join(s.log, "\n")
}

func errCode(err error) string {
	switch {
	case errors.Is(err, ErrParam):
		return "ErrParam"
	case errors.Is(err, ErrClock):
		return "ErrClock"
	case errors.Is(err, ErrBusy):
		return "ErrBusy"
	case errors.Is(err, ErrUnknown):
		return "ErrUnknown"
	case errors.Is(err, ErrNotNeeded):
		return "ErrNotNeeded"
	case err == nil:
		return "nil"
	default:
		return err.Error()
	}
}
