package vegas

// naiveToken is the literal token entry used by the reference model.
type naiveToken struct {
	seq     int64
	w       int64
	expires int64
}

type naiveSample struct {
	at  int64
	rtt int64
}

// naiveLimiter reimplements the specification with straightforward linear
// scans: reap walks the whole outstanding table, window minimum scans every
// in-window sample, and token lookup scans the token table.
type naiveLimiter struct {
	cfg      Config
	L, n     int64
	nextSeq  int64
	tokens   []naiveToken
	timedOut map[int64]bool
	samples  []naiveSample
	lastCut  *int64
	maxNow   int64
}

func newNaive(c Config) *naiveLimiter {
	return &naiveLimiter{
		cfg:      c,
		L:        c.L0,
		timedOut: map[int64]bool{},
	}
}

func (m *naiveLimiter) validTime(now int64) bool {
	return now >= 0 && now <= maxTime
}

func (m *naiveLimiter) cut(now int64) {
	if m.lastCut != nil && now < *m.lastCut+m.cfg.Cd {
		return
	}
	next := m.L * 9 / 10
	if next < m.cfg.Lmin {
		next = m.cfg.Lmin
	}
	m.L = next
	t := now
	m.lastCut = &t
}

func (m *naiveLimiter) reap(now int64) {
	// Process every expired token in ascending seq order, one cut per token.
	for {
		idx := -1
		for i := range m.tokens {
			if m.tokens[i].expires <= now {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		m.n--
		m.timedOut[m.tokens[idx].seq] = true
		m.cut(now)
		m.tokens = append(m.tokens[:idx], m.tokens[idx+1:]...)
	}
}

func (m *naiveLimiter) Acquire(now int64) (int64, error) {
	if !m.validTime(now) {
		return 0, ErrInvalidTime
	}
	if now < m.maxNow {
		return 0, ErrClockRewind
	}
	m.maxNow = now
	m.reap(now)
	if m.n >= m.L {
		return 0, ErrAtLimit
	}
	m.n++
	m.nextSeq++
	m.tokens = append(m.tokens, naiveToken{
		seq:     m.nextSeq,
		w:       m.n,
		expires: now + m.cfg.Tmo,
	})
	return m.nextSeq, nil
}

func (m *naiveLimiter) Release(seq int64, res Result, rtt int64, now int64) error {
	if res != ResultSuccess && res != ResultDrop && res != ResultIgnore {
		return ErrInvalidResult
	}
	if !m.validTime(now) {
		return ErrInvalidTime
	}
	if now < m.maxNow {
		return ErrClockRewind
	}
	m.maxNow = now
	m.reap(now)

	if m.timedOut[seq] {
		return ErrTokenTimedOut
	}
	idx := -1
	var tok naiveToken
	for i := range m.tokens {
		if m.tokens[i].seq == seq {
			idx = i
			tok = m.tokens[i]
			break
		}
	}
	if idx < 0 {
		return ErrInvalidToken
	}
	if res == ResultSuccess && (rtt < 1 || rtt > maxRTT) {
		return ErrInvalidRTT
	}

	m.n--
	m.tokens = append(m.tokens[:idx], m.tokens[idx+1:]...)

	switch res {
	case ResultDrop:
		m.cut(now)
	case ResultSuccess:
		m.samples = append(m.samples, naiveSample{at: now, rtt: rtt})
		alive := m.samples[:0]
		mn := int64(-1)
		for _, s := range m.samples {
			if s.at+m.cfg.Wm > now {
				alive = append(alive, s)
				if mn < 0 || s.rtt < mn {
					mn = s.rtt
				}
			}
		}
		m.samples = alive
		if tok.w*2 >= m.L {
			q := (m.L*(rtt-mn) + rtt - 1) / rtt
			if q < m.cfg.Alpha {
				if m.L < m.cfg.Lmax {
					m.L++
				}
			} else if q > m.cfg.Beta {
				if m.L > m.cfg.Lmin {
					m.L--
				}
			}
		}
	}
	return nil
}
