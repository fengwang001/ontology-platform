package queue

import (
	"container/heap"
	"container/list"
	"math/bits"
	"sync"
)

const maxCustomerValue int64 = 1_000_000_000

type ErrorCode string

const (
	ErrInvalidServers    ErrorCode = "invalid_servers"
	ErrInvalidID         ErrorCode = "invalid_id"
	ErrInvalidArrival    ErrorCode = "invalid_arrival"
	ErrInvalidService    ErrorCode = "invalid_service"
	ErrInvalidPatience   ErrorCode = "invalid_patience"
	ErrDuplicateID       ErrorCode = "duplicate_id"
	ErrLateArrival       ErrorCode = "late_arrival"
	ErrInvalidTargetTime ErrorCode = "invalid_target_time"
	ErrTargetBeforeWater ErrorCode = "target_before_water"
	ErrCustomerNotFound  ErrorCode = "customer_not_found"
)

type OperationError struct {
	Code ErrorCode
}

func (e *OperationError) Error() string {
	return string(e.Code)
}

type Status string

const (
	NotArrived Status = "not_arrived"
	Waiting    Status = "waiting"
	Serving    Status = "serving"
	Served     Status = "served"
	Abandoned  Status = "abandoned"
)

type Customer struct {
	ID       int64
	Arrive   int64
	Service  int64
	Patience int64
}

type Outcome struct {
	Status      Status
	Server      int
	Start       int64
	End         int64
	Wait        int64
	AbandonTime int64
}

type Stats struct {
	Served       int
	Abandoned    int
	TotalWait    int64
	MaxQueueSize int
}

func NewSimulator(servers int) (*Simulator, error) {
	if servers < 1 || servers > 64 {
		return nil, &OperationError{Code: ErrInvalidServers}
	}

	return &Simulator{
		servers:   servers,
		free:      ^uint64(0) >> (64 - servers),
		customers: make(map[int64]*entry),
		queue:     list.New(),
		watermark: -1,
	}, nil
}

func (s *Simulator) Add(id, arrive, service, patience int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id < 1 {
		return &OperationError{Code: ErrInvalidID}
	}
	if arrive < 0 || arrive > maxCustomerValue {
		return &OperationError{Code: ErrInvalidArrival}
	}
	if service < 1 || service > maxCustomerValue {
		return &OperationError{Code: ErrInvalidService}
	}
	if patience < 0 || patience > maxCustomerValue {
		return &OperationError{Code: ErrInvalidPatience}
	}
	if _, exists := s.customers[id]; exists {
		return &OperationError{Code: ErrDuplicateID}
	}
	if arrive <= s.watermark {
		return &OperationError{Code: ErrLateArrival}
	}

	customer := &entry{
		Customer: Customer{ID: id, Arrive: arrive, Service: service, Patience: patience},
		status:   NotArrived,
	}
	s.customers[id] = customer
	heap.Push(&s.arrivals, customer)
	return nil
}

func (s *Simulator) AdvanceTo(t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t < 0 {
		return &OperationError{Code: ErrInvalidTargetTime}
	}
	if t < s.watermark {
		return &OperationError{Code: ErrTargetBeforeWater}
	}
	if t == s.watermark {
		return nil
	}

	for {
		next, ok := s.nextEventTime()
		if !ok || next > t {
			break
		}
		s.processTime(next)
	}
	s.watermark = t
	s.recordQueueSize()
	return nil
}

func (s *Simulator) Outcome(id int64) (Outcome, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	customer, ok := s.customers[id]
	if !ok {
		return Outcome{}, &OperationError{Code: ErrCustomerNotFound}
	}

	return Outcome{
		Status:      customer.status,
		Server:      customer.server,
		Start:       customer.start,
		End:         customer.end,
		Wait:        customer.wait,
		AbandonTime: customer.abandonTime,
	}, nil
}

func (s *Simulator) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return Stats{
		Served:       s.served,
		Abandoned:    s.abandoned,
		TotalWait:    s.totalWait,
		MaxQueueSize: s.maxQueueSize,
	}
}

type Simulator struct {
	mu           sync.RWMutex
	servers      int
	free         uint64
	customers    map[int64]*entry
	arrivals     arrivalHeap
	completions  completionHeap
	abandonments abandonmentHeap
	queue        *list.List
	watermark    int64
	served       int
	abandoned    int
	totalWait    int64
	maxQueueSize int
}

type entry struct {
	Customer
	status       Status
	server       int
	start        int64
	end          int64
	wait         int64
	abandonTime  int64
	queueElement *list.Element
}

func (customer *entry) deadline() int64 {
	return customer.Arrive + customer.Patience
}

func (s *Simulator) nextEventTime() (int64, bool) {
	var next int64
	found := false

	if len(s.arrivals) > 0 {
		next = s.arrivals[0].Arrive
		found = true
	}
	if len(s.completions) > 0 {
		end := s.completions[0].end
		if !found || end < next {
			next = end
			found = true
		}
	}

	s.pruneAbandonments()
	if len(s.abandonments) > 0 {
		deadline := s.abandonments[0].deadline()
		if !found || deadline < next {
			next = deadline
			found = true
		}
	}

	return next, found
}

func (s *Simulator) processTime(now int64) {
	s.watermark = now
	s.completeServices(now)
	s.startQueuedCustomers()
	s.processArrivals(now)
	s.abandonCustomers(now)
	s.recordQueueSize()
}

func (s *Simulator) completeServices(now int64) {
	for len(s.completions) > 0 && s.completions[0].end == now {
		customer := heap.Pop(&s.completions).(*entry)
		if customer.status != Serving || customer.end != now {
			continue
		}
		customer.status = Served
		s.served++
		s.totalWait += customer.wait
		s.free |= uint64(1) << (customer.server - 1)
	}
}

func (s *Simulator) startQueuedCustomers() {
	for s.free != 0 && s.queue.Len() > 0 {
		element := s.queue.Front()
		customer := element.Value.(*entry)
		s.queue.Remove(element)
		customer.queueElement = nil
		s.startService(customer)
	}
}

func (s *Simulator) processArrivals(now int64) {
	for len(s.arrivals) > 0 && s.arrivals[0].Arrive == now {
		customer := heap.Pop(&s.arrivals).(*entry)
		if customer.status != NotArrived {
			continue
		}
		if s.free != 0 {
			s.startService(customer)
		} else {
			customer.status = Waiting
			customer.queueElement = s.queue.PushBack(customer)
			heap.Push(&s.abandonments, customer)
		}
	}
}

func (s *Simulator) pruneAbandonments() {
	for len(s.abandonments) > 0 && s.abandonments[0].status != Waiting {
		heap.Pop(&s.abandonments)
	}
}

func (s *Simulator) abandonCustomers(now int64) {
	s.pruneAbandonments()
	pending := make(map[*entry]bool)
	for len(s.abandonments) > 0 && s.abandonments[0].deadline() <= now {
		customer := heap.Pop(&s.abandonments).(*entry)
		if customer.status == Waiting {
			pending[customer] = true
		}
	}

	element := s.queue.Front()
	for element != nil {
		next := element.Next()
		customer := element.Value.(*entry)
		if pending[customer] {
			s.queue.Remove(element)
			customer.queueElement = nil
			customer.status = Abandoned
			customer.abandonTime = customer.deadline()
			s.abandoned++
		}
		element = next
	}
}

func (s *Simulator) startService(customer *entry) {
	index := bits.TrailingZeros64(s.free)
	server := index + 1
	s.free &^= uint64(1) << index

	customer.status = Serving
	customer.server = server
	customer.start = s.watermark
	customer.end = customer.start + customer.Service
	customer.wait = customer.start - customer.Arrive
	heap.Push(&s.completions, customer)
}

func (s *Simulator) recordQueueSize() {
	if size := s.queue.Len(); size > s.maxQueueSize {
		s.maxQueueSize = size
	}
}

type arrivalHeap []*entry

func (h arrivalHeap) Len() int { return len(h) }

func (h arrivalHeap) Less(i, j int) bool {
	if h[i].Arrive != h[j].Arrive {
		return h[i].Arrive < h[j].Arrive
	}
	return h[i].ID < h[j].ID
}

func (h arrivalHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *arrivalHeap) Push(value any) { *h = append(*h, value.(*entry)) }

func (h *arrivalHeap) Pop() any {
	old := *h
	tail := len(old) - 1
	value := old[tail]
	*h = old[:tail]
	return value
}

type completionHeap []*entry

func (h completionHeap) Len() int { return len(h) }

func (h completionHeap) Less(i, j int) bool {
	if h[i].end != h[j].end {
		return h[i].end < h[j].end
	}
	if h[i].server != h[j].server {
		return h[i].server < h[j].server
	}
	return h[i].ID < h[j].ID
}

func (h completionHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *completionHeap) Push(value any) { *h = append(*h, value.(*entry)) }

func (h *completionHeap) Pop() any {
	old := *h
	tail := len(old) - 1
	value := old[tail]
	*h = old[:tail]
	return value
}

type abandonmentHeap []*entry

func (h abandonmentHeap) Len() int { return len(h) }

func (h abandonmentHeap) Less(i, j int) bool {
	if h[i].deadline() != h[j].deadline() {
		return h[i].deadline() < h[j].deadline()
	}
	return h[i].ID < h[j].ID
}

func (h abandonmentHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *abandonmentHeap) Push(value any) { *h = append(*h, value.(*entry)) }

func (h *abandonmentHeap) Pop() any {
	old := *h
	tail := len(old) - 1
	value := old[tail]
	*h = old[:tail]
	return value
}
