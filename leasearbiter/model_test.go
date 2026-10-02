package leasearbiter

import "errors"

// naiveArbiter is a from-scratch, deliberately straightforward restatement
// of the specification, used as an independent oracle in differential
// tests. It shares no structure with the production Arbiter.
type naiveArbiter struct {
	n, q         int
	dur, et      int64
	rho          int
	start, t     int64
	leader       bool
	ackVal       map[int]int64
	hasAck       map[int]bool
	prVal        map[int]int64
	queue        []naiveRead
	nextID       int
	lastExamined int
}

type naiveRead struct {
	id int
	at int64
}

type naiveOutcome struct {
	local     bool
	pending   bool
	id        int
	expiry    int64
	confirmed []int
	aborted   []int
	votes     int
	examined  int
}

func naiveNew(n int, dur int64, rho int, et, start int64) (*naiveArbiter, error) {
	ok := 1 <= n && n <= 9
	ok = ok && 1 <= dur && dur <= 1_000_000_000
	ok = ok && 0 <= rho && rho <= 999
	ok = ok && dur <= et && et <= 1_000_000_000
	ok = ok && 0 <= start && start <= maxTime
	if !ok {
		return nil, ErrInvalidConfig
	}
	return &naiveArbiter{
		n: n, q: n/2 + 1, dur: dur, et: et, rho: rho,
		start: start, t: start, leader: true,
		ackVal: map[int]int64{}, hasAck: map[int]bool{}, prVal: map[int]int64{},
	}, nil
}

func (m *naiveArbiter) window() int64 { return m.dur * int64(1000-m.rho) / 1000 }

func errName(err error) string {
	switch {
	case errors.Is(err, ErrInvalidConfig):
		return "InvalidConfig"
	case errors.Is(err, ErrInvalidArgs):
		return "InvalidArgs"
	case errors.Is(err, ErrClockSkew):
		return "ClockSkew"
	case errors.Is(err, ErrNotLeader):
		return "NotLeader"
	case err == nil:
		return ""
	default:
		return err.Error()
	}
}

func (m *naiveArbiter) baseAndExpiry(now int64) (int64, bool) {
	need := m.q - 1
	if need == 0 {
		return now + m.window(), true
	}
	vals := make([]int64, 0, m.n-1)
	for i := 1; i < m.n; i++ {
		if m.hasAck[i] {
			vals = append(vals, m.ackVal[i])
		}
	}
	if len(vals) < need {
		return 0, false
	}
	// Order statistic via a full insertion sort of the private copy.
	for i := 1; i < len(vals); i++ {
		x := vals[i]
		j := i
		for j > 0 && vals[j-1] < x {
			vals[j] = vals[j-1]
			j--
		}
		vals[j] = x
	}
	return vals[need-1] + m.window(), true
}

func (m *naiveArbiter) ack(i int, s, r int64) (naiveOutcome, error) {
	if i < 1 || i > m.n-1 || s < 0 || r < 0 || s > maxTime || r > maxTime || s > r {
		return naiveOutcome{}, ErrInvalidArgs
	}
	if r < m.t {
		return naiveOutcome{}, ErrClockSkew
	}
	if !m.hasAck[i] || s > m.ackVal[i] {
		m.ackVal[i] = s
	}
	m.hasAck[i] = true
	if cand := r + m.dur; cand > m.prVal[i] {
		m.prVal[i] = cand
	}
	m.t = r

	m.lastExamined = 0
	out := naiveOutcome{}
	for len(m.queue) > 0 {
		head := m.queue[0]
		m.lastExamined++
		supporters := 0
		for f := 1; f < m.n; f++ {
			if m.hasAck[f] && m.ackVal[f] >= head.at {
				supporters++
			}
		}
		if supporters < m.q-1 {
			break
		}
		out.confirmed = append(out.confirmed, head.id)
		m.queue = m.queue[1:]
	}
	out.examined = m.lastExamined
	return out, nil
}

func (m *naiveArbiter) read(now int64) (naiveOutcome, error) {
	if now < 0 || now > maxTime {
		return naiveOutcome{}, ErrInvalidArgs
	}
	if now < m.t {
		return naiveOutcome{}, ErrClockSkew
	}
	if !m.leader {
		return naiveOutcome{}, ErrNotLeader
	}
	m.t = now
	if e, ok := m.baseAndExpiry(now); ok && now < e {
		return naiveOutcome{local: true, expiry: e}, nil
	}
	m.nextID++
	m.queue = append(m.queue, naiveRead{id: m.nextID, at: now})
	return naiveOutcome{pending: true, id: m.nextID}, nil
}

func (m *naiveArbiter) tick(now int64) (naiveOutcome, error) {
	if now < 0 || now > maxTime {
		return naiveOutcome{}, ErrInvalidArgs
	}
	if now < m.t {
		return naiveOutcome{}, ErrClockSkew
	}
	out := naiveOutcome{}
	if m.leader && now >= m.start+m.et {
		contact := 0
		for f := 1; f < m.n; f++ {
			if m.hasAck[f] && m.ackVal[f] >= now-m.et {
				contact++
			}
		}
		if contact+1 < m.q {
			m.leader = false
			for _, rd := range m.queue {
				out.aborted = append(out.aborted, rd.id)
			}
			m.queue = nil
		}
	}
	m.t = now
	return out, nil
}

func (m *naiveArbiter) challenger(now int64) (naiveOutcome, error) {
	if now < 0 || now > maxTime {
		return naiveOutcome{}, ErrInvalidArgs
	}
	if now < m.t {
		return naiveOutcome{}, ErrClockSkew
	}
	votes := 0
	for f := 1; f < m.n; f++ {
		if now >= m.prVal[f] {
			votes++
		}
	}
	if !m.leader {
		votes++
	}
	return naiveOutcome{votes: votes}, nil
}
