package splitdeque

import "errors"

var (
	ErrInvalidConfig   = errors.New("splitdeque: invalid configuration")
	ErrFull            = errors.New("splitdeque: deque is full")
	ErrInvalidArgument = errors.New("splitdeque: invalid argument")
)

type Stats struct {
	Pushed   int64
	Popped   int64
	Stolen   int64
	Misses   int64
	Releases int64
	Reclaims int64
}

type SplitDeque struct {
	cap int64
	sm  int64
	rv  int64
	f   int64

	buf []int64

	t  int64
	s  int64
	b  int64
	fl bool
	ag int64
	dm int64

	releaseMoves int64
	reclaimMoves int64
	stealCopies  int64

	stats Stats

	mu chan struct{}
}

func newLocked() chan struct{} {
	mu := make(chan struct{}, 1)
	mu <- struct{}{}
	return mu
}

// New constructs a split deque.
// cap is the total capacity Cap, sm the shared limit Sm,
// rv the private reservation Rv and f the request freshness F.
// It returns ErrInvalidConfig when any parameter is out of range.
func New(cap, sm, rv, f int64) (*SplitDeque, error) {
	if cap < 1 || cap > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	if sm < 1 || sm > cap {
		return nil, ErrInvalidConfig
	}
	if rv < 0 || rv > cap {
		return nil, ErrInvalidConfig
	}
	if f < 1 || f > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	return &SplitDeque{
		cap: cap,
		sm:  sm,
		rv:  rv,
		f:   f,
		buf: make([]int64, cap),
		mu:  newLocked(),
	}, nil
}

// releaseCheck runs the release rule while the caller holds the lock.
// It is a no-op unless a steal request is pending.
func (d *SplitDeque) releaseCheck() {
	if !d.fl {
		return
	}
	private := d.b - d.s
	shared := d.s - d.t
	r := private / 2
	if d.dm > r {
		r = d.dm
	}
	if limit := d.sm - shared; limit < r {
		r = limit
	}
	if limit := private - d.rv; limit < r {
		r = limit
	}
	if r < 0 {
		r = 0
	}
	if r >= 1 {
		d.s += r
		d.fl = false
		d.ag = 0
		d.dm = 0
		d.stats.Releases++
		return
	}
	d.ag++
	if d.ag == d.f {
		d.fl = false
		d.ag = 0
		d.dm = 0
	}
}

// Push appends x at the private back, then runs the release check.
func (d *SplitDeque) Push(x int64) error {
	d.lock()
	defer d.unlock()
	if d.b-d.t == d.cap {
		return ErrFull
	}
	d.buf[d.b%d.cap] = x
	d.b++
	d.stats.Pushed++
	d.releaseCheck()
	return nil
}

// Pop removes and returns the newest private element, running the release
// check first. When the private region is empty it reclaims ceil(|S|/2)
// newest shared elements before popping. The boolean is false only when
// both regions are empty.
func (d *SplitDeque) Pop() (int64, bool) {
	d.lock()
	defer d.unlock()
	d.releaseCheck()
	if d.b-d.s > 0 {
		x := d.buf[(d.b-1)%d.cap]
		d.b--
		d.stats.Popped++
		return x, true
	}
	if d.s-d.t == 0 {
		return 0, false
	}
	g := (d.s - d.t + 1) / 2
	d.s -= g
	d.stats.Reclaims++
	x := d.buf[(d.b-1)%d.cap]
	d.b--
	d.stats.Popped++
	return x, true
}

// Steal removes the k = min(m, |S|) oldest shared elements in ascending
// index order and returns a freshly allocated copy.
func (d *SplitDeque) Steal(m int64) ([]int64, error) {
	if m < 1 || m > d.cap {
		return nil, ErrInvalidArgument
	}
	d.lock()
	defer d.unlock()
	shared := d.s - d.t
	k := m
	if k > shared {
		k = shared
	}
	out := make([]int64, k)
	for i := int64(0); i < k; i++ {
		out[i] = d.buf[(d.t+i)%d.cap]
	}
	d.t += k
	d.stats.Stolen += k
	d.stealCopies += k
	if k < m {
		deficit := m - k
		d.stats.Misses++
		if !d.fl {
			d.fl = true
			d.dm = deficit
		} else if deficit > d.dm {
			d.dm = deficit
		}
		d.ag = 0
	}
	return out, nil
}

// Stats returns cumulative operation counters.
func (d *SplitDeque) Stats() Stats {
	d.lock()
	defer d.unlock()
	return d.stats
}

// MoveCounts exposes the element traffic counters used to prove that
// release and reclaim move the split point only: both values stay zero,
// while stealCopies equals the total number of copied elements.
func (d *SplitDeque) MoveCounts() (releaseMoves, reclaimMoves, stealCopies int64) {
	d.lock()
	defer d.unlock()
	return d.releaseMoves, d.reclaimMoves, d.stealCopies
}

func (d *SplitDeque) lock() {
	<-d.mu
}

func (d *SplitDeque) unlock() {
	d.mu <- struct{}{}
}
