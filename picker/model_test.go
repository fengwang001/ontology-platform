package picker

import (
	"sort"

	"ontology/pool"
)

// naiveModel 是严格按题目规则独立重写的朴素参考实现，
// 用于与 picker.Picker 做逐步快照差分对照。
type naiveEp struct {
	state     int // 0 active, 1 draining
	infl      int64
	est       int64
	last      int64
	hasSample bool
}

type naiveModel struct {
	tau, p0, pf int64
	m           int64
	nmax        int

	eps    map[string]*naiveEp
	tk     map[int64]string
	nextTk int64
	maxNow int64
}

func newNaive(tau, p0, pf, m int64, nmax int) *naiveModel {
	return &naiveModel{
		tau: tau, p0: p0, pf: pf, m: m, nmax: nmax,
		eps: map[string]*naiveEp{}, tk: map[int64]string{}, nextTk: 1,
	}
}

func (nm *naiveModel) val(e *naiveEp, now int64) int64 {
	if !e.hasSample {
		return nm.p0
	}
	d := now - e.last
	if d > nm.tau {
		d = nm.tau
	}
	return e.est * (nm.tau - d) / nm.tau
}

type opResult struct {
	err      error
	ticket   int64
	endpoint string
}

func (nm *naiveModel) add(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	if _, ok := nm.eps[id]; ok {
		return ErrExists
	}
	if len(nm.eps) >= nm.nmax {
		return ErrFull
	}
	nm.eps[id] = &naiveEp{}
	return nil
}

func (nm *naiveModel) remove(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	e, ok := nm.eps[id]
	if !ok {
		return ErrNotFound
	}
	if e.state == 1 {
		return ErrDraining
	}
	if e.infl == 0 {
		delete(nm.eps, id)
		return nil
	}
	e.state = 1
	return nil
}

func (nm *naiveModel) timeCheck(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidTime
	}
	if now < nm.maxNow {
		return ErrClockBackward
	}
	return nil
}

func (nm *naiveModel) pick(now int64, r1, r2 uint64) (int64, string, error) {
	if err := nm.timeCheck(now); err != nil {
		return 0, "", err
	}
	var elig []string
	active := 0
	for id, e := range nm.eps {
		if e.state == 0 {
			active++
			if e.infl < nm.m {
				elig = append(elig, id)
			}
		}
	}
	if len(elig) == 0 {
		if active > 0 {
			return 0, "", ErrSaturated
		}
		return 0, "", ErrNoEndpoint
	}
	sort.Strings(elig)
	n := len(elig)
	winner := elig[0]
	if n > 1 {
		i := int(r1 % uint64(n))
		j := int((uint64(i) + 1 + r2%uint64(n-1)) % uint64(n))
		ci := nm.val(nm.eps[elig[i]], now) * (nm.eps[elig[i]].infl + 1)
		cj := nm.val(nm.eps[elig[j]], now) * (nm.eps[elig[j]].infl + 1)
		winner = elig[j]
		if ci < cj || (ci == cj && elig[i] < elig[j]) {
			winner = elig[i]
		}
	}
	ticket := nm.nextTk
	nm.nextTk++
	nm.maxNow = now
	nm.eps[winner].infl++
	nm.tk[ticket] = winner
	return ticket, winner, nil
}

func (nm *naiveModel) release(ticket, rtt int64, ok bool, now int64) error {
	if rtt < 0 || rtt > maxParam {
		return ErrInvalidParam
	}
	if err := nm.timeCheck(now); err != nil {
		return err
	}
	id, exists := nm.tk[ticket]
	if !exists {
		return ErrTicket
	}
	delete(nm.tk, ticket)
	e := nm.eps[id]
	e.infl--
	sample := rtt
	if !ok {
		sample = nm.pf
	}
	if !e.hasSample {
		e.est = sample
	} else {
		if v := nm.val(e, now); sample > v {
			e.est = sample
		} else {
			e.est = v
		}
	}
	e.last = now
	e.hasSample = true
	nm.maxNow = now
	if e.state == 1 && e.infl == 0 {
		delete(nm.eps, id)
	}
	return nil
}

type snapshot struct {
	nextTk int64
	maxNow int64
	open   int
	total  int
	eps    map[string]epSnap
}

type epSnap struct {
	state   int
	infl    int64
	val     int64
	present bool
}

func (nm *naiveModel) snap(now int64) snapshot {
	s := snapshot{nextTk: nm.nextTk, maxNow: nm.maxNow, open: len(nm.tk), eps: map[string]epSnap{}}
	for id, e := range nm.eps {
		s.total += int(e.infl)
		s.eps[id] = epSnap{state: e.state, infl: e.infl, val: nm.val(e, now)}
	}
	return s
}

func (p *Picker) snap(now int64) snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s := snapshot{nextTk: p.nextTicket, maxNow: p.maxNow, open: p.pool.OpenTickets(), eps: map[string]epSnap{}}
	for _, id := range p.pool.IDs() {
		e := p.pool.Get(id)
		st := 0
		if e.State() == pool.Draining {
			st = 1
		}
		s.total += int(e.Inflight())
		s.eps[id] = epSnap{state: st, infl: e.Inflight(), val: e.Value(now)}
	}
	return s
}
