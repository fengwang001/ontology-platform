package parking

import (
	"container/heap"
	"fmt"
	"sync"
)

type Manager struct {
	mu sync.Mutex

	cfg           Config
	now           int
	vehicles      map[string]*vehicle
	plates        map[string]*registration
	knownPlates   map[string]bool
	registrations map[int]*registration
	occupants     map[int]*vehicle
	publicFree    freeSet
	monthlyFree   freeSet
	shares        *scheduleIndex
	waiting       []string
	timers        timerHeap
	logs          []DecisionLog
	timerSeq      int
}

type vehicle struct {
	plate        string
	spot         int
	entered      int
	monthly      bool
	waiting      bool
	public       bool
	evictOwner   string
	evictStart   int
	evictVersion int
	freeEviction int
	overtime     []intervalMinutes
	overtimeOpen bool
	shareVersion int
}

type timerKind int

const (
	timerShareEnd timerKind = iota + 1
	timerEviction
	timerLeaseEnd
	timerGraceEnd
)

type timerEvent struct {
	at      int
	kind    timerKind
	plate   string
	spotID  int
	version int
	seq     int
}

type timerHeap []timerEvent

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	if h[i].kind != h[j].kind {
		return h[i].kind < h[j].kind
	}
	if h[i].plate != h[j].plate {
		return h[i].plate < h[j].plate
	}
	return h[i].seq < h[j].seq
}
func (h timerHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *timerHeap) Push(x any)   { *h = append(*h, x.(timerEvent)) }
func (h *timerHeap) Pop() any {
	old := *h
	tail := len(old) - 1
	item := old[tail]
	*h = old[:tail]
	return item
}

func NewManager(cfg Config) (*Manager, error) {
	if !cfg.valid() {
		return nil, ErrInvalidArgument
	}
	publicIDs := make([]int, 0, cfg.PublicSpots)
	for id := 1; id <= cfg.PublicSpots; id++ {
		publicIDs = append(publicIDs, id)
	}
	monthlyIDs := make([]int, 0, cfg.MonthlySpots)
	for id := cfg.PublicSpots + 1; id <= cfg.PublicSpots+cfg.MonthlySpots; id++ {
		monthlyIDs = append(monthlyIDs, id)
	}
	return &Manager{
		cfg:           cfg,
		vehicles:      make(map[string]*vehicle),
		plates:        make(map[string]*registration),
		knownPlates:   make(map[string]bool),
		registrations: make(map[int]*registration),
		occupants:     make(map[int]*vehicle),
		publicFree:    newFreeSet(publicIDs...),
		monthlyFree:   newFreeSet(monthlyIDs...),
		shares:        newScheduleIndex(),
	}, nil
}

func (m *Manager) Logs() []DecisionLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DecisionLog, len(m.logs))
	copy(out, m.logs)
	return out
}

func (m *Manager) record(action string, now int, plate, reason string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordLocked(action, now, plate, reason, err)
}

func (m *Manager) recordLocked(action string, now int, plate, reason string, err error) {
	m.logs = append(m.logs, DecisionLog{
		Now:      now,
		Action:   action,
		Plate:    plate,
		Reason:   reason,
		Rejected: err != nil,
	})
}

func (m *Manager) checkClock(now int) error {
	if now < m.now {
		return ErrClockRollback
	}
	return nil
}

func (m *Manager) pushTimer(kind timerKind, at int, plate string, spotID, version int) {
	m.timerSeq++
	heap.Push(&m.timers, timerEvent{
		at: at, kind: kind, plate: plate, spotID: spotID,
		version: version, seq: m.timerSeq,
	})
}

func validateInterval(interval Interval) error {
	if interval.StartMinute < 0 || interval.StartMinute >= minutesPerDay ||
		interval.EndMinute < 0 || interval.EndMinute >= minutesPerDay {
		return ErrInvalidArgument
	}
	if interval.StartMinute == interval.EndMinute {
		return ErrInvalidArgument
	}
	return nil
}

func isDayBoundary(value int) bool { return value >= 0 && value%minutesPerDay == 0 }

func validateLeaseDays(plate string, start, end int) error {
	if plate == "" || !isDayBoundary(start) || !isDayBoundary(end) || start >= end {
		return ErrInvalidArgument
	}
	return nil
}

var _ = fmt.Sprintf
