package ontology

import (
	"container/heap"
	"errors"
	"sync"
)

var (
	ErrInvalidOffset       = errors.New("invalid timezone offset")
	ErrInvalidQuietStart   = errors.New("invalid quiet start")
	ErrInvalidQuietEnd     = errors.New("invalid quiet end")
	ErrInvalidMergeWindow  = errors.New("invalid merge window")
	ErrInvalidDailyCap     = errors.New("invalid daily cap")
	ErrInvalidQueueLimit   = errors.New("invalid pending batch limit")
	ErrEmptyKey            = errors.New("empty notification key")
	ErrInvalidPriority     = errors.New("invalid priority")
	ErrInvalidTime         = errors.New("invalid time")
	ErrClockMovedBackwards = errors.New("clock moved backwards")
	ErrQueueFull           = errors.New("notification queue is full")
)

type Delivery struct {
	Key   []byte
	Count int64
	At    int64
}

type Scheduler struct {
	off    int64
	qs     int64
	qe     int64
	window int64
	cap    int64
	limit  int

	mu      sync.Mutex
	lastNow int64
	seq     int64
	batches *batchHeap
	byKey   map[string]*batch
	sent    map[int64]int64

	dueProbes int
}

func NewScheduler(off, qs, qe, window, dailyCap int64, queueLimit int) (*Scheduler, error) {
	switch {
	case off < -720 || off > 840:
		return nil, ErrInvalidOffset
	case qs < 0 || qs > 1439:
		return nil, ErrInvalidQuietStart
	case qe < 0 || qe > 1439:
		return nil, ErrInvalidQuietEnd
	case window < 0 || window > 1440:
		return nil, ErrInvalidMergeWindow
	case dailyCap < 1 || dailyCap > 1000:
		return nil, ErrInvalidDailyCap
	case queueLimit < 1 || queueLimit > 100000:
		return nil, ErrInvalidQueueLimit
	}

	batches := make(batchHeap, 0)
	heap.Init(&batches)
	return &Scheduler{
		off:     off,
		qs:      qs,
		qe:      qe,
		window:  window,
		cap:     dailyCap,
		limit:   queueLimit,
		batches: &batches,
		byKey:   make(map[string]*batch),
		sent:    make(map[int64]int64),
	}, nil
}

func (s *Scheduler) Submit(key []byte, prio, now int64) ([]Delivery, error) {
	if len(key) == 0 {
		return nil, ErrEmptyKey
	}
	if prio < 0 || prio > 2 {
		return nil, ErrInvalidPriority
	}
	if err := s.checkTime(now); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.lastNow {
		return nil, ErrClockMovedBackwards
	}

	keyString := string(key)
	preflightBatches := len(*s.batches)
	if preflightBatches >= s.limit {
		if prio == 1 {
			return nil, ErrQueueFull
		}
		if prio == 0 {
			existing := s.byKey[keyString]
			if existing == nil {
				return nil, ErrQueueFull
			}
			if existing.f <= now {
				s.dueProbes = 0
				delivered, rollback := s.processDueWithJournal(now)
				if s.byKey[keyString] == existing {
					s.lastNow = now
					existing.count++
					return delivered, nil
				}
				rollback()
				s.dueProbes = 0
				return nil, ErrQueueFull
			}
		}
	}

	s.dueProbes = 0
	delivered := s.processDue(now)
	s.lastNow = now

	keyCopy := append([]byte(nil), key...)
	switch prio {
	case 2:
		day := s.localDay(now)
		s.sent[day]++
		delivered = append(delivered, Delivery{Key: keyCopy, Count: 1, At: now})
	case 1:
		s.addBatch(keyCopy, 1, s.shifted(now), false)
	case 0:
		if existing, exists := s.byKey[keyString]; exists {
			existing.count++
		} else {
			s.addBatch(keyCopy, 1, s.shifted(now+s.window), true)
		}
	}

	return delivered, nil
}

func (s *Scheduler) Poll(now int64) ([]Delivery, error) {
	if err := s.checkTime(now); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.lastNow {
		return nil, ErrClockMovedBackwards
	}

	s.dueProbes = 0
	delivered := s.processDue(now)
	s.lastNow = now
	return delivered, nil
}

func (s *Scheduler) localMinute(t int64) int64 {
	minute := (t + s.off) % 1440
	if minute < 0 {
		minute += 1440
	}
	return minute
}

func (s *Scheduler) localDay(t int64) int64 {
	shifted := t + s.off
	minute := shifted % 1440
	if minute < 0 {
		minute += 1440
	}
	return (shifted - minute) / 1440
}

func (s *Scheduler) shifted(t int64) int64 {
	minute := s.localMinute(t)
	quiet := false
	if s.qs < s.qe {
		quiet = minute >= s.qs && minute < s.qe
	} else if s.qs > s.qe {
		quiet = minute >= s.qs || minute < s.qe
	}
	if !quiet {
		return t
	}

	wait := (s.qe - minute) % 1440
	if wait < 0 {
		wait += 1440
	}
	return t + wait
}

func (s *Scheduler) processDue(now int64) []Delivery {
	delivered, _ := s.processDueWithJournal(now)
	return delivered
}

type dueChange struct {
	batch     *batch
	day       int64
	oldF      int64
	delivered bool
	mergeable bool
}

func (s *Scheduler) processDueWithJournal(now int64) ([]Delivery, func()) {
	var delivered []Delivery
	var changes []dueChange

	for {
		if len(*s.batches) == 0 {
			s.dueProbes++
			break
		}
		s.dueProbes++
		current := (*s.batches)[0]
		if current.f > now {
			break
		}

		current = heap.Pop(s.batches).(*batch)
		day := s.localDay(current.f)
		oldF := current.f
		if s.sent[day] < s.cap {
			mergeable := s.byKey[string(current.key)] == current
			if mergeable {
				delete(s.byKey, string(current.key))
			}
			s.sent[day]++
			delivered = append(delivered, Delivery{
				Key:   append([]byte(nil), current.key...),
				Count: current.count,
				At:    current.f,
			})
			changes = append(changes, dueChange{
				batch:     current,
				day:       day,
				oldF:      oldF,
				delivered: true,
				mergeable: mergeable,
			})
			continue
		}

		current.f = s.shifted((day+1)*1440 - s.off)
		heap.Push(s.batches, current)
		changes = append(changes, dueChange{batch: current, oldF: oldF, day: day})
	}

	rollback := func() {
		for i := len(changes) - 1; i >= 0; i-- {
			change := changes[i]
			if change.delivered {
				s.sent[change.day]--
				change.batch.f = change.oldF
				heap.Push(s.batches, change.batch)
				if change.mergeable {
					s.byKey[string(change.batch.key)] = change.batch
				}
				continue
			}

			change.batch.f = change.oldF
			heap.Fix(s.batches, change.batch.index)
		}
	}

	return delivered, rollback
}

func (s *Scheduler) checkTime(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidTime
	}
	return nil
}

func (s *Scheduler) addBatch(key []byte, count int64, f int64, mergeable bool) {
	s.seq++
	batch := &batch{
		key:   key,
		count: count,
		f:     f,
		seq:   s.seq,
	}
	heap.Push(s.batches, batch)
	if mergeable {
		s.byKey[string(key)] = batch
	}
}

type batch struct {
	key   []byte
	count int64
	f     int64
	seq   int64
	index int
}

type batchHeap []*batch

func (h batchHeap) Len() int { return len(h) }

func (h batchHeap) Less(i, j int) bool {
	if h[i].f != h[j].f {
		return h[i].f < h[j].f
	}
	return h[i].seq < h[j].seq
}

func (h batchHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *batchHeap) Push(x any) {
	b := x.(*batch)
	b.index = len(*h)
	*h = append(*h, b)
}

func (h *batchHeap) Pop() any {
	old := *h
	n := len(old)
	b := old[n-1]
	old[n-1] = nil
	b.index = -1
	*h = old[:n-1]
	return b
}

var _ heap.Interface = (*batchHeap)(nil)
