package vegas

import "math/rand"

// naiveToken 是朴素模拟器的令牌记录：逐项保存，不使用任何索引/队列优化。
type naiveToken struct {
	seq       int64
	w         int64
	expiresAt int64
	out       bool // 是否仍在途（未归还、未超时）
	timedOut  bool // 是否曾被超时回收
}

type naiveSample struct {
	t   int64
	rtt int64
}

// naiveLimiter 严格按题目规则逐行实现：每次都全表扫描令牌表与窗口。
type naiveLimiter struct {
	lmin, lmax, alpha, beta, tmo, cd, wm int64
	l, n                                 int64
	hasLastCut                           bool
	lastCut                              int64
	maxNow                               int64
	nextSeq                              int64
	tokens                               []*naiveToken
	window                               []naiveSample
}

func newNaive(c Config) *naiveLimiter {
	return &naiveLimiter{
		lmin: c.Lmin, lmax: c.Lmax, alpha: c.Alpha, beta: c.Beta,
		tmo: c.Tmo, cd: c.Cooldown, wm: c.Wm,
		l: c.L0, nextSeq: 1,
	}
}

func (m *naiveLimiter) cut(now int64) {
	if !m.hasLastCut || now >= m.lastCut+m.cd {
		x := m.l * 9 / 10
		if x < m.lmin {
			x = m.lmin
		}
		m.l = x
		m.lastCut = now
		m.hasLastCut = true
	}
}

func (m *naiveLimiter) reap(now int64) {
	// 按序号升序，逐项扫描所有令牌，挑出在途且已超时者，每回收一个 cut 一次。
	var due []*naiveToken
	for _, tk := range m.tokens {
		if tk.out && tk.expiresAt <= now {
			due = append(due, tk)
		}
	}
	for _, tk := range due {
		tk.out = false
		tk.timedOut = true
		m.n--
		m.cut(now)
	}
}

// naiveOutcome 是一次操作对外可观察的结果。
type naiveOutcome struct {
	ok       bool
	err      error
	seq      int64
	w        int64
	exp      int64
	l, n, mr int64
}

func errCode(e error) string {
	switch e {
	case nil:
		return ""
	case ErrInvalidResult:
		return "bad-result"
	case ErrInvalidTime:
		return "bad-time"
	case ErrClockRewind:
		return "rewind"
	case ErrAtCapacity:
		return "capacity"
	case ErrTokenTimedOut:
		return "timed-out"
	case ErrInvalidToken:
		return "bad-token"
	case ErrInvalidRTT:
		return "bad-rtt"
	default:
		return "other"
	}
}

func (m *naiveLimiter) checkClock(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < m.maxNow {
		return ErrClockRewind
	}
	m.maxNow = now
	return nil
}

func (m *naiveLimiter) minRTT(now int64) int64 {
	var best int64
	first := true
	for _, s := range m.window {
		if s.t+m.wm > now {
			if first || s.rtt < best {
				best = s.rtt
				first = false
			}
		}
	}
	if first {
		return 0
	}
	return best
}

func (m *naiveLimiter) acquire(now int64) naiveOutcome {
	if err := m.checkClock(now); err != nil {
		return naiveOutcome{err: err, l: m.l, n: m.n}
	}
	m.reap(now)
	if m.n >= m.l {
		return naiveOutcome{err: ErrAtCapacity, l: m.l, n: m.n}
	}
	m.n++
	tk := &naiveToken{seq: m.nextSeq, w: m.n, expiresAt: now + m.tmo, out: true}
	m.nextSeq++
	m.tokens = append(m.tokens, tk)
	return naiveOutcome{ok: true, seq: tk.seq, w: tk.w, exp: tk.expiresAt, l: m.l, n: m.n}
}

func (m *naiveLimiter) release(seq int64, res Result, rtt, now int64) naiveOutcome {
	if res != Success && res != Dropped && res != Ignored {
		return naiveOutcome{err: ErrInvalidResult, l: m.l, n: m.n}
	}
	if err := m.checkClock(now); err != nil {
		return naiveOutcome{err: err, l: m.l, n: m.n}
	}
	m.reap(now)
	// 已超时（在已超时集合中）。
	for _, tk := range m.tokens {
		if tk.seq == seq && tk.timedOut {
			return naiveOutcome{err: ErrTokenTimedOut, l: m.l, n: m.n}
		}
	}
	// 无效：从未发放，或已归还。
	var cur *naiveToken
	for _, tk := range m.tokens {
		if tk.seq == seq {
			cur = tk
			break
		}
	}
	if cur == nil {
		return naiveOutcome{err: ErrInvalidToken, l: m.l, n: m.n}
	}
	if !cur.out {
		return naiveOutcome{err: ErrInvalidToken, l: m.l, n: m.n}
	}
	if res == Success && (rtt < 1 || rtt > maxRTT) {
		return naiveOutcome{err: ErrInvalidRTT, l: m.l, n: m.n}
	}
	cur.out = false
	m.n--
	switch res {
	case Ignored:
	case Dropped:
		m.cut(now)
	case Success:
		m.window = append(m.window, naiveSample{t: now, rtt: rtt})
		mm := m.minRTT(now)
		if cur.w*2 >= m.l {
			q := (m.l*(rtt-mm) + rtt - 1) / rtt
			if q < m.alpha {
				if m.l < m.lmax {
					m.l++
				}
			} else if q > m.beta {
				if m.l > m.lmin {
					m.l--
				}
			}
		}
	}
	return naiveOutcome{ok: true, l: m.l, n: m.n, mr: m.minRTT(now)}
}

// ---- 操作序列生成 ----

type testOp struct {
	kind  int // 0 acquire, 1 release
	now   int64
	seq   int64
	res   Result
	rtt   int64
	noRtt bool
	desc  string
}

func randConfig(r *rand.Rand) Config {
	lmin := int64(1 + r.Intn(20))
	lmax := lmin + int64(r.Intn(40))
	l0 := lmin + int64(r.Intn(int(lmax-lmin)+1))
	alpha := int64(r.Intn(5))
	beta := alpha + 1 + int64(r.Intn(6))
	return Config{
		L0: l0, Lmin: lmin, Lmax: lmax,
		Alpha: alpha, Beta: beta,
		Tmo:      int64(1 + r.Intn(60)),
		Cooldown: int64(r.Intn(20)),
		Wm:       int64(1 + r.Intn(120)),
	}
}

var validResults = []Result{Success, Dropped, Ignored}
