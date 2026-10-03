package scheduler

import (
	"container/heap"
	"errors"
	"sync"
)

type Reason string

const (
	ReasonInvalidArgument Reason = "invalid argument"
	ReasonClockRollback   Reason = "clock rollback"
	ReasonQueueFull       Reason = "queue full"
)

type Config struct {
	OffsetMinutes int
	QuietStart    int
	QuietEnd      int
	MergeWindow   int
	DailyCap      int
	MaxPending    int
}

type Delivery struct {
	Key   string
	Count int
	At    int64
}

type batch struct {
	key    string
	count  int
	fireAt int64
	seq    uint64
	normal bool
}

type Scheduler struct {
	off int
	qs  int
	qe  int
	g   int
	cap int
	max int

	lastNow int64
	nextSeq uint64
	pending []*batch
	normal  map[string]*batch
	sent    map[int64]int

	batchExamined int64
	batchDeferred int64

	mu sync.Mutex
}

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRollback   = errors.New("clock rollback")
	ErrQueueFull       = errors.New("queue full")
)

func NewScheduler(config Config) (*Scheduler, error) {
	if config.OffsetMinutes < -720 || config.OffsetMinutes > 840 ||
		config.QuietStart < 0 || config.QuietStart > 1439 ||
		config.QuietEnd < 0 || config.QuietEnd > 1439 ||
		config.MergeWindow < 0 || config.MergeWindow > 1440 ||
		config.DailyCap < 1 || config.DailyCap > 1000 ||
		config.MaxPending < 1 || config.MaxPending > 100000 {
		return nil, ErrInvalidArgument
	}

	return &Scheduler{
		off:     config.OffsetMinutes,
		qs:      config.QuietStart,
		qe:      config.QuietEnd,
		g:       config.MergeWindow,
		cap:     config.DailyCap,
		max:     config.MaxPending,
		nextSeq: 1,
		normal:  make(map[string]*batch),
		sent:    make(map[int64]int),
	}, nil
}

func New(config Config) (*Scheduler, error) { return NewScheduler(config) }

func (s *Scheduler) Submit(key string, priority int, now int64) ([]Delivery, error) {
	if key == "" || priority < 0 || priority > 2 || now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.lastNow {
		return nil, ErrClockRollback
	}

	needsBatch := priority == 1 || (priority == 0 && s.normal[key] == nil)
	if needsBatch && len(s.pending) >= s.max {
		return nil, ErrQueueFull
	}

	s.batchExamined = 0
	s.batchDeferred = 0
	deliveries := s.processDue(now)

	switch priority {
	case 2:
		day := localDay(now, s.off)
		s.sent[day]++
		deliveries = append(deliveries, Delivery{Key: key, Count: 1, At: now})
	case 1:
		s.pushBatch(&batch{
			key:    key,
			count:  1,
			fireAt: s.shift(now),
			seq:    s.nextSeq,
		})
		s.nextSeq++
	case 0:
		if existing := s.normal[key]; existing != nil {
			existing.count++
			break
		}
		s.pushBatch(&batch{
			key:    key,
			count:  1,
			fireAt: s.shift(now + int64(s.g)),
			seq:    s.nextSeq,
			normal: true,
		})
		s.nextSeq++
	}

	s.lastNow = now
	return deliveries, nil
}

func (s *Scheduler) Poll(now int64) ([]Delivery, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.lastNow {
		return nil, ErrClockRollback
	}

	s.batchExamined = 0
	s.batchDeferred = 0
	deliveries := s.processDue(now)
	s.lastNow = now
	return deliveries, nil
}

func (s *Scheduler) processDue(now int64) []Delivery {
	var deliveries []Delivery
	queue := s.heap()

	for len(*queue) > 0 {
		s.batchExamined++
		current := (*queue)[0]
		if current.fireAt > now {
			break
		}

		heap.Pop(queue)
		day := localDay(current.fireAt, s.off)
		if s.sent[day] < s.cap {
			if current.normal {
				delete(s.normal, current.key)
			}
			s.sent[day]++
			deliveries = append(deliveries, Delivery{
				Key:   current.key,
				Count: current.count,
				At:    current.fireAt,
			})
			continue
		}

		current.fireAt = s.shift(s.nextDayStart(day))
		heap.Push(queue, current)
		s.batchDeferred++
	}

	if len(*queue) == 0 {
		s.batchExamined++
	}
	return deliveries
}

func (s *Scheduler) pushBatch(item *batch) {
	heap.Push(s.heap(), item)
	if item.normal {
		s.normal[item.key] = item
	}
}
