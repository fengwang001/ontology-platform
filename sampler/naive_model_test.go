package sampler

import (
	"hash/fnv"
	"sort"
)

func hfnv(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// Independent step-by-step naive model: linear scans, same rules, no heaps.
type nTr struct {
	tid             string
	spans           map[string]int64
	last, maxD, cnt int64
	err             bool
}
type nCa struct {
	keep bool
	at   int64
}
type naive struct {
	cfg                Config
	buf                map[string]*nTr
	cache              map[string]nCa
	window, used       int64
	lateK, lateD, decN int64
}

func newNaive(c Config) *naive {
	return &naive{cfg: c, buf: map[string]*nTr{}, cache: map[string]nCa{}, window: -1}
}
func (n *naive) cacheOn() bool { return n.cfg.Td > 0 && n.cfg.Cmax > 0 }

func (n *naive) decide(tr *nTr, at int64, ev bool) Decision {
	if w := at / n.cfg.Wb; w != n.window {
		n.window, n.used = w, 0
	}
	keep, reason := false, ReasonSampledOut
	switch {
	case tr.err:
		keep, reason = true, ReasonError
	case tr.maxD >= n.cfg.L:
		keep, reason = true, ReasonLatency
	case int64(hfnv(tr.tid)%10000) < n.cfg.P:
		keep, reason = true, ReasonProb
	}
	if keep && reason == ReasonProb && n.used >= n.cfg.Q {
		keep, reason = false, ReasonBudget
	}
	if keep {
		n.used++
	}
	d := Decision{tr.tid, keep, reason, tr.cnt, at, ev}
	if n.cacheOn() {
		for id, c := range n.cache {
			if c.at+n.cfg.Td <= at {
				delete(n.cache, id)
			}
		}
		n.cache[d.TraceID] = nCa{d.Keep, at}
		for int64(len(n.cache)) > n.cfg.Cmax {
			old := ""
			for id, c := range n.cache {
				if old == "" || c.at < n.cache[old].at || (c.at == n.cache[old].at && id < old) {
					old = id
				}
			}
			delete(n.cache, old)
		}
	}
	n.decN++
	return d
}

func (n *naive) ingest(now int64, tid, sid string, dur int64, isErr bool) []Decision {
	if tr, ok := n.buf[tid]; ok {
		if _, dup := tr.spans[sid]; dup {
			return nil
		}
		tr.spans[sid], tr.cnt, tr.last = dur, tr.cnt+1, now
		if dur > tr.maxD {
			tr.maxD = dur
		}
		tr.err = tr.err || isErr
		if tr.cnt >= n.cfg.Sc {
			delete(n.buf, tid)
			return []Decision{n.decide(tr, now, false)}
		}
		return nil
	}
	if c, ok := n.cache[tid]; ok && n.cacheOn() && c.at+n.cfg.Td > now {
		if c.keep {
			n.lateK++
		} else {
			n.lateD++
		}
		return nil
	}
	delete(n.cache, tid)
	var out []Decision
	if int64(len(n.buf)) >= n.cfg.Nmax {
		var old *nTr
		for _, t := range n.buf {
			if old == nil || t.last < old.last || (t.last == old.last && t.tid < old.tid) {
				old = t
			}
		}
		delete(n.buf, old.tid)
		out = append(out, n.decide(old, now, true))
	}
	tr := &nTr{tid: tid, spans: map[string]int64{sid: dur}, last: now, maxD: dur, err: isErr, cnt: 1}
	n.buf[tid] = tr
	if tr.cnt >= n.cfg.Sc {
		delete(n.buf, tid)
		out = append(out, n.decide(tr, now, false))
	}
	return out
}

func (n *naive) tick(now int64) []Decision {
	all := make([]*nTr, 0, len(n.buf))
	for _, t := range n.buf {
		all = append(all, t)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].last < all[j].last || (all[i].last == all[j].last && all[i].tid < all[j].tid)
	})
	var out []Decision
	for _, tr := range all {
		if tr.last+n.cfg.W > now {
			break
		}
		delete(n.buf, tr.tid)
		out = append(out, n.decide(tr, now, false))
	}
	return out
}

func eqDec(a, b []Decision) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustNew(c Config) *Sampler {
	s, err := New(c)
	if err != nil {
		panic(err)
	}
	return s
}

// TestNaiveEquivalenceRandom replays identical inputs through the sampler and
// the linear-scan naive model, asserting identical ordered decisions and the
