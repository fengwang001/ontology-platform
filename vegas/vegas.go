// Package vegas implements a Vegas-style concurrency limiter.
package vegas

import "sync"

// Result is the outcome reported when a token is released.
type Result int

const (
	ResultSuccess Result = iota
	ResultDrop
	ResultIgnore
)

// Token is an admission permit returned by Acquire.
type Token struct {
	Seq int64 // 1-based permit number; rejected Acquire consumes no number.
	w   int64 // in-flight count n right after this token was admitted.
}

// Config holds the tunable parameters of the limiter.
type Config struct {
	L0    int64 // initial limit
	Lmin  int64 // minimum limit
	Lmax  int64 // maximum limit
	Alpha int64 // grow threshold
	Beta  int64 // shrink threshold
	Tmo   int64 // token timeout, must be in [1, 1e9]
	Cd    int64 // downgrade cooldown, must be in [0, 1e9]
	Wm    int64 // minimum-rtt window length, must be in [1, 1e9]
}

const (
	maxLimit      int64 = 1_000_000
	maxTime       int64 = 1_000_000_000_000_000
	maxRTT        int64 = 1_000_000_000_000
	maxCooldown   int64 = 1_000_000_000
	maxWindowTime int64 = 1_000_000_000
)

type outstanding struct {
	seq     int64
	w       int64
	expires int64
}

type sample struct {
	at  int64
	rtt int64
}

// Limiter is a concurrency-safe Vegas-style limiter.
type Limiter struct {
	mu sync.Mutex

	cfg Config

	L int64 // current limit
	n int64 // in-flight count

	// nextSeq is the number of the next admitted token (first is 1).
	nextSeq int64

	// tokens are outstanding, non-expired permits in ascending seq order;
	// deadlines are non-decreasing because time never moves backwards.
	tokens []outstanding

	// timedOut holds numbers of reaped tokens; a later Release is rejected.
	timedOut map[int64]struct{}

	// Sliding window of successful samples (ascending time) and a monotonic
	// deque of indices holding the window minimum rtt at its front.
	samples []sample
	mind    []int

	// lastCut is the time of the last effective cut, or nil while none ran.
	lastCut *int64

	// maxNow is the largest time that passed the three pre-checks so far.
	// It starts at 0, so times are effectively restricted to [0, 1e15].
	maxNow int64

	// Cumulative work counters (amortized O(1) per operation).
	windowExamined int64
	tokenExamined  int64
}

// New constructs a Limiter or returns ErrInvalidConfig.
func New(c Config) (*Limiter, error) {
	if c.Lmin < 1 ||
		c.Lmax < c.Lmin || c.Lmax > maxLimit ||
		c.L0 < c.Lmin || c.L0 > c.Lmax ||
		c.Alpha < 0 ||
		c.Beta <= c.Alpha ||
		c.Tmo < 1 || c.Tmo > maxWindowTime ||
		c.Wm < 1 || c.Wm > maxWindowTime ||
		c.Cd < 0 || c.Cd > maxCooldown {
		return nil, ErrInvalidConfig
	}
	return &Limiter{
		cfg:      c,
		L:        c.L0,
		timedOut: make(map[int64]struct{}),
	}, nil
}

// Acquire attempts to admit a request at time now. It returns ErrInvalidTime
// or ErrClockRewind for rejected pre-checks (no state change), ErrAtLimit
// after reap when no slot is available (reap and maxNow are kept), or nil
// with a valid Token.
func (l *Limiter) Acquire(now int64) (Token, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !validTime(now) {
		return Token{}, ErrInvalidTime
	}
	if now < l.maxNow {
		return Token{}, ErrClockRewind
	}
	l.maxNow = now
	l.reap(now)

	if l.n >= l.L {
		return Token{}, ErrAtLimit
	}

	l.n++
	l.nextSeq++
	tok := Token{Seq: l.nextSeq, w: l.n}
	l.tokens = append(l.tokens, outstanding{
		seq:     tok.Seq,
		w:       tok.w,
		expires: now + l.cfg.Tmo,
	})
	return tok, nil
}

// Release returns a previously acquired token.
func (l *Limiter) Release(tok Token, res Result, rtt int64, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if res != ResultSuccess && res != ResultDrop && res != ResultIgnore {
		return ErrInvalidResult
	}
	if !validTime(now) {
		return ErrInvalidTime
	}
	if now < l.maxNow {
		return ErrClockRewind
	}
	l.maxNow = now
	l.reap(now)

	if _, dead := l.timedOut[tok.Seq]; dead {
		return ErrTokenTimedOut
	}

	idx := l.tokenIndex(tok.Seq)
	if idx < 0 {
		return ErrInvalidToken
	}

	if res == ResultSuccess && (rtt < 1 || rtt > maxRTT) {
		return ErrInvalidRTT
	}

	admitted := l.tokens[idx]
	l.removeToken(idx)
	l.n--

	switch res {
	case ResultIgnore:
		// nothing else
	case ResultDrop:
		l.cut(now)
	case ResultSuccess:
		l.addSample(now, rtt)
		m := l.windowMin()
		// w is the in-flight count observed when the request was admitted.
		if admitted.w*2 >= l.L {
			q := ceilDiv(l.L*(rtt-m), rtt)
			switch {
			case q < l.cfg.Alpha:
				if l.L < l.cfg.Lmax {
					l.L++
				}
			case q > l.cfg.Beta:
				if l.L > l.cfg.Lmin {
					l.L--
				}
			}
		}
	}
	return nil
}

func validTime(now int64) bool {
	return now >= 0 && now <= maxTime
}

// cut applies a 10% downgrade unless the cooldown is still active. It always
// refreshes lastCut when the cooldown allows the cut to run, even when the
// floor leaves L unchanged.
func (l *Limiter) cut(now int64) {
	if l.lastCut != nil && now < *l.lastCut+l.cfg.Cd {
		return
	}
	next := l.L * 9 / 10
	if next < l.cfg.Lmin {
		next = l.cfg.Lmin
	}
	l.L = next
	l.lastCut = &now
}

// reap expires every outstanding token whose deadline is <= now, in ascending
// seq order, running cut once per expired token.
func (l *Limiter) reap(now int64) {
	i := 0
	for i < len(l.tokens) && l.tokens[i].expires <= now {
		l.tokenExamined++
		l.n--
		l.timedOut[l.tokens[i].seq] = struct{}{}
		l.cut(now)
		i++
	}
	if i > 0 {
		l.tokens = append(l.tokens[:0], l.tokens[i:]...)
	}
}

func (l *Limiter) tokenIndex(seq int64) int {
	// tokens is sorted by seq; binary search keeps the scan sub-linear.
	lo, hi := 0, len(l.tokens)
	for lo < hi {
		mid := (lo + hi) / 2
		if l.tokens[mid].seq < seq {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(l.tokens) && l.tokens[lo].seq == seq {
		return lo
	}
	return -1
}

func (l *Limiter) removeToken(idx int) {
	l.tokens = append(l.tokens[:idx], l.tokens[idx+1:]...)
}

func (l *Limiter) addSample(now, rtt int64) {
	l.evictWindow(now)
	l.samples = append(l.samples, sample{at: now, rtt: rtt})
	idx := len(l.samples) - 1
	for len(l.mind) > 0 {
		back := l.mind[len(l.mind)-1]
		l.windowExamined++
		if l.samples[back].rtt > rtt {
			l.mind = l.mind[:len(l.mind)-1]
			continue
		}
		break
	}
	l.mind = append(l.mind, idx)
}

// evictWindow drops samples with at+Wm <= now; a sample is in the window only
// while at+Wm > now.
func (l *Limiter) evictWindow(now int64) {
	drop := 0
	for drop < len(l.samples) && l.samples[drop].at+l.cfg.Wm <= now {
		l.windowExamined++
		drop++
	}
	if drop == 0 {
		return
	}
	l.samples = append(l.samples[:0], l.samples[drop:]...)
	for len(l.mind) > 0 && l.mind[0] < drop {
		l.mind = l.mind[1:]
	}
	for i := range l.mind {
		l.mind[i] -= drop
	}
}

func (l *Limiter) windowMin() int64 {
	return l.samples[l.mind[0]].rtt
}

func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}
