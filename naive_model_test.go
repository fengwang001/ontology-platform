package sampler

import (
	"sort"

	"sampler/report"
)

// naive is a deliberately simple, spec-literal reference model.
type naive struct {
	n, m, w, kt, tmax uint64
	maxNow            uint64
	ents              map[string]map[string]*[3]uint64 // tenant -> key -> {win,cnt,dropped}
	lru               map[string][]string              // tenant -> keys, oldest first
}

func newNaive(n, m, w, kt, tmax uint64) *naive {
	return &naive{n: n, m: m, w: w, kt: kt, tmax: tmax,
		ents: map[string]map[string]*[3]uint64{}, lru: map[string][]string{}}
}

func (x *naive) record(now uint64, tn, key string, sev int) (bool, []report.Summary, error) {
	if now > maxNow || tn == "" || len(tn) > maxLen || key == "" || len(key) > maxLen || sev < 0 || sev > 5 {
		return false, nil, ErrParam
	}
	if now < x.maxNow {
		return false, nil, ErrClock
	}
	if _, ok := x.ents[tn]; !ok && len(x.ents) >= int(x.tmax) {
		return false, nil, ErrTenantLimit
	}
	x.maxNow = now
	cur := now / x.w
	if x.ents[tn] == nil {
		x.ents[tn] = map[string]*[3]uint64{}
	}
	keys := x.lru[tn]
	for i, k := range keys { // touch: move key to most-recent
		if k == key {
			keys = append(keys[:i], keys[i+1:]...)
			break
		}
	}
	x.lru[tn] = append(keys, key)
	e, ok := x.ents[tn][key]
	var sums []report.Summary
	if !ok {
		if uint64(len(x.ents[tn])) >= x.kt {
			victim := x.lru[tn][0]
			x.lru[tn] = x.lru[tn][1:]
			v := x.ents[tn][victim]
			if v[2] > 0 {
				sums = append(sums, report.Summary{Tenant: tn, Key: victim, Window: v[0], Dropped: v[2], Reason: report.Evicted})
			}
			delete(x.ents[tn], victim)
		}
		e = &[3]uint64{cur, 0, 0}
		x.ents[tn][key] = e
	} else if e[0] != cur {
		if e[2] > 0 {
			sums = append(sums, report.Summary{Tenant: tn, Key: key, Window: e[0], Dropped: e[2], Reason: report.Rolled})
		}
		*e = [3]uint64{cur, 0, 0}
	}
	if sev >= 4 {
		return true, sums, nil
	}
	e[1]++
	if e[1] <= x.n || (e[1]-x.n)%x.m == 0 {
		return true, sums, nil
	}
	e[2]++
	return false, sums, nil
}

func (x *naive) flush(now uint64, sink report.Sink) (int, error) {
	if sink == nil {
		return 0, ErrParam
	}
	if now < x.maxNow {
		return 0, ErrClock
	}
	x.maxNow = now
	cur := now / x.w
	var refs []report.Summary
	for tn, m := range x.ents {
		for k, e := range m {
			if e[0] < cur && e[2] > 0 {
				refs = append(refs, report.Summary{Tenant: tn, Key: k, Window: e[0], Dropped: e[2], Reason: report.Closed})
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Tenant != refs[j].Tenant {
			return refs[i].Tenant < refs[j].Tenant
		}
		return refs[i].Key < refs[j].Key
	})
	n := 0
	for _, sm := range refs {
		if err := sink(sm); err != nil {
			return n, err
		}
		x.ents[sm.Tenant][sm.Key][2] = 0
		n++
	}
	return n, nil
}
