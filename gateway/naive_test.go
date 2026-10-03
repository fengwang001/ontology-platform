package gateway_test

// naiveTenant / naiveRec are an independent, line-by-line reimplementation of
// the specification used as a differential oracle.
type naiveTenant struct {
	rate, cap, tokens, last int64
	maxPrio                 int
	acc, evi, drn, que      [3]int64
}

type naiveRec struct {
	seq    int64
	tenant string
	prio   int
	size   int64
}

type naiveModel struct {
	cap     int64
	tenants map[string]*naiveTenant
	lanes   [3][]naiveRec
	used    int64
	seq     int64
	maxNow  int64
}

func refill(t *naiveTenant, now int64) int64 {
	if now <= t.last || t.rate <= 0 {
		return t.tokens
	}
	d := now - t.last
	need := t.cap - t.tokens
	if need <= 0 {
		return t.cap
	}
	// Saturate without forming the up-to-1e21 product.
	if d >= (need+t.rate-1)/t.rate {
		return t.cap
	}
	return t.tokens + t.rate*d
}

func (m *naiveModel) ingest(now int64, name string, prio int, size int64) ([]naiveRec, string) {
	t, ok := m.tenants[name]
	if !ok {
		return nil, "no-tenant"
	}
	if prio < t.maxPrio {
		return nil, "forbidden"
	}
	cost := size * 1000
	if refill(t, now) < cost {
		return nil, "quota"
	}
	free := m.cap - m.used
	var evicted []naiveRec
	if size > free {
		need := size - free
		var avail int64
		for p := prio + 1; p < 3; p++ {
			for _, r := range m.lanes[p] {
				avail += r.size
			}
		}
		if avail < need {
			return nil, "full"
		}
		var got int64
		for p := 2; p > prio && got < need; p-- {
			for got < need && len(m.lanes[p]) > 0 {
				i := len(m.lanes[p]) - 1
				r := m.lanes[p][i]
				m.lanes[p] = m.lanes[p][:i]
				ev := m.tenants[r.tenant]
				ev.tokens = refill(ev, now)
				ev.last = now
				ev.tokens += r.size * 1000
				if ev.tokens > ev.cap {
					ev.tokens = ev.cap
				}
				ev.evi[r.prio] += r.size
				ev.que[r.prio] -= r.size
				evicted = append(evicted, r)
				got += r.size
				m.used -= r.size
			}
		}
	}
	t.tokens = refill(t, now) - cost
	t.last = now
	m.seq++
	m.lanes[prio] = append(m.lanes[prio], naiveRec{m.seq, name, prio, size})
	t.acc[prio] += size
	t.que[prio] += size
	m.used += size
	m.maxNow = now
	return evicted, "ok"
}

func (m *naiveModel) drain(now, budget int64) []naiveRec {
	var out []naiveRec
	var used int64
	for p := 0; p < 3; p++ {
		for len(m.lanes[p]) > 0 {
			if used+m.lanes[p][0].size > budget {
				m.maxNow = now
				return out
			}
			r := m.lanes[p][0]
			m.lanes[p] = m.lanes[p][1:]
			used += r.size
			tn := m.tenants[r.tenant]
			tn.drn[r.prio] += r.size
			tn.que[r.prio] -= r.size
			m.used -= r.size
			out = append(out, r)
		}
	}
	m.maxNow = now
	return out
}
