package queue

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

const maxValue = 1_000_000_000

var (
	ErrInvalidServerCount     = errors.New("invalid server count")
	ErrInvalidCustomer        = errors.New("invalid customer")
	ErrDuplicateID            = errors.New("duplicate customer id")
	ErrArrivalBeforeWatermark = errors.New("arrival time is not after watermark")
	ErrInvalidTargetTime      = errors.New("invalid target time")
	ErrCustomerNotFound       = errors.New("customer not found")
)

const (
	StatusNotArrived = "not_arrived"
	StatusWaiting    = "waiting"
	StatusServing    = "serving"
	StatusServed     = "served"
	StatusAbandoned  = "abandoned"
)

type Customer struct {
	ID       int64
	Arrive   int64
	Service  int64
	Patience int64
}

type ServiceRecord struct {
	Server int
	Start  int64
	End    int64
	Wait   int64
}

type Outcome struct {
	Status      string
	Service     ServiceRecord
	AbandonTime int64
}

type Statistics struct {
	Served       int64
	Abandoned    int64
	TotalWaiting int64
	MaxQueueLen  int64
}

type Simulator struct {
	mu          sync.Mutex
	servers     int
	watermark   int64
	customers   map[int64]*customerState
	arrivals    map[int64]map[int64]struct{}
	completions map[int64]map[int]struct{}
	eventRefs   map[int64]int
	poppedRefs  map[int64]int
	events      timeHeap
	queue       []*customerState
	customerAt  map[int]int64
	busy        []bool
	stats       Statistics
}

type customerState struct {
	customer     Customer
	status       string
	server       int
	start        int64
	end          int64
	abandonTime  int64
	abandonEvent bool
}

type timeHeap []int64

func (h timeHeap) Len() int           { return len(h) }
func (h timeHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h timeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *timeHeap) Push(value any) {
	*h = append(*h, value.(int64))
}

func (h *timeHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

func New(servers int) (*Simulator, error) {
	if servers < 1 || servers > 64 {
		return nil, ErrInvalidServerCount
	}

	s := &Simulator{
		servers:     servers,
		watermark:   -1,
		customers:   make(map[int64]*customerState),
		arrivals:    make(map[int64]map[int64]struct{}),
		completions: make(map[int64]map[int]struct{}),
		eventRefs:   make(map[int64]int),
		poppedRefs:  make(map[int64]int),
		customerAt:  make(map[int]int64),
		busy:        make([]bool, servers+1),
	}
	heap.Init(&s.events)
	return s, nil
}

func (s *Simulator) Add(customer Customer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if customer.ID < 1 || customer.Arrive < 0 || customer.Service < 1 || customer.Patience < 0 ||
		customer.Arrive > maxValue || customer.Service > maxValue || customer.Patience > maxValue {
		return ErrInvalidCustomer
	}
	if _, exists := s.customers[customer.ID]; exists {
		return ErrDuplicateID
	}
	if customer.Arrive <= s.watermark {
		return ErrArrivalBeforeWatermark
	}

	state := &customerState{customer: customer, status: StatusNotArrived}
	s.customers[customer.ID] = state

	if ids, exists := s.arrivals[customer.Arrive]; exists {
		ids[customer.ID] = struct{}{}
	} else {
		s.arrivals[customer.Arrive] = map[int64]struct{}{customer.ID: {}}
		s.addEvent(customer.Arrive)
	}
	return nil
}

func (s *Simulator) AdvanceTo(t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t < 0 || t < s.watermark {
		return ErrInvalidTargetTime
	}
	if t == s.watermark {
		return nil
	}

	for len(s.events) > 0 && s.events[0] <= t {
		when := heap.Pop(&s.events).(int64)
		refs := s.eventRefs[when]
		if refs == 0 {
			continue
		}
		delete(s.eventRefs, when)
		s.poppedRefs[when] = refs

		s.processCompletions(when)
		s.processArrivals(when)
		s.processWaitingAbandonments(when)

		if s.poppedRefs[when] != 0 {
			panic("queue: event reference count mismatch")
		}
		delete(s.poppedRefs, when)

		if length := int64(len(s.queue)); length > s.stats.MaxQueueLen {
			s.stats.MaxQueueLen = length
		}
	}

	s.watermark = t
	return nil
}

func (s *Simulator) Outcome(id int64) (Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, exists := s.customers[id]
	if !exists {
		return Outcome{}, ErrCustomerNotFound
	}

	result := Outcome{Status: state.status}
	if state.status == StatusServing || state.status == StatusServed {
		result.Service = ServiceRecord{
			Server: state.server,
			Start:  state.start,
			End:    state.end,
			Wait:   state.start - state.customer.Arrive,
		}
	}
	if state.status == StatusAbandoned {
		result.AbandonTime = state.abandonTime
	}
	return result, nil
}

func (s *Simulator) Stats() Statistics {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *Simulator) addEvent(when int64) {
	if s.eventRefs[when] == 0 {
		heap.Push(&s.events, when)
	}
	s.eventRefs[when]++
}

func (s *Simulator) releaseEvent(when int64) {
	if refs, popped := s.poppedRefs[when]; popped {
		if refs <= 1 {
			delete(s.poppedRefs, when)
		} else {
			s.poppedRefs[when] = refs - 1
		}
		return
	}
	if s.eventRefs[when] <= 1 {
		delete(s.eventRefs, when)
	} else {
		s.eventRefs[when]--
	}
}

func (s *Simulator) processCompletions(when int64) {
	completed := s.completions[when]
	for server := 1; server <= s.servers; server++ {
		if _, exists := completed[server]; !exists {
			continue
		}
		state := s.customers[s.customerAt[server]]
		state.status = StatusServed
		s.stats.Served++
		s.stats.TotalWaiting += state.start - state.customer.Arrive
		delete(s.customerAt, server)
		s.busy[server] = false
		s.releaseEvent(when)
	}

	for server := 1; server <= s.servers; server++ {
		if !s.busy[server] {
			s.startFromQueue(server, when)
		}
	}

	if len(completed) > 0 {
		delete(s.completions, when)
	}
}

func (s *Simulator) processArrivals(when int64) {
	idsByTime := s.arrivals[when]
	if len(idsByTime) == 0 {
		return
	}

	ids := make([]int64, 0, len(idsByTime))
	for id := range idsByTime {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		state := s.customers[id]
		server := s.firstIdleServer()
		if server == 0 {
			s.enqueueOrAbandon(state, when)
			continue
		}
		s.startServing(state, server, when)
	}

	delete(s.arrivals, when)
	s.releaseEvent(when)
}

func (s *Simulator) processWaitingAbandonments(when int64) {
	kept := s.queue[:0]
	for _, state := range s.queue {
		if state.abandonTime <= when {
			s.abandon(state)
			continue
		}
		kept = append(kept, state)
	}
	s.queue = kept
}

func (s *Simulator) startFromQueue(server int, when int64) {
	for len(s.queue) > 0 {
		state := s.queue[0]
		s.queue = s.queue[1:]
		if state.abandonTime < when {
			s.abandon(state)
			continue
		}
		s.startServing(state, server, when)
		return
	}
}

func (s *Simulator) startServing(state *customerState, server int, when int64) {
	state.status = StatusServing
	state.server = server
	state.start = when
	state.end = when + state.customer.Service
	s.busy[server] = true
	s.customerAt[server] = state.customer.ID
	if state.abandonEvent {
		s.releaseEvent(state.abandonTime)
		state.abandonEvent = false
	}

	if completed, exists := s.completions[state.end]; exists {
		completed[server] = struct{}{}
	} else {
		s.completions[state.end] = map[int]struct{}{server: {}}
	}
	s.addEvent(state.end)
}

func (s *Simulator) enqueueOrAbandon(state *customerState, when int64) {
	state.abandonTime = state.customer.Arrive + state.customer.Patience
	if state.abandonTime <= when {
		s.abandon(state)
		return
	}
	state.status = StatusWaiting
	state.abandonEvent = true
	s.addEvent(state.abandonTime)
	s.queue = append(s.queue, state)
}

func (s *Simulator) abandon(state *customerState) {
	state.status = StatusAbandoned
	state.abandonTime = state.customer.Arrive + state.customer.Patience
	s.stats.Abandoned++
	if state.abandonEvent {
		s.releaseEvent(state.abandonTime)
		state.abandonEvent = false
	}
}

func (s *Simulator) firstIdleServer() int {
	for server := 1; server <= s.servers; server++ {
		if !s.busy[server] {
			return server
		}
	}
	return 0
}
