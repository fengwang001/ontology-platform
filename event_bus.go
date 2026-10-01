package eventbus

import (
	"errors"
	"runtime"
	"sort"
	"sync"
)

var (
	ErrEmptyTopic            = errors.New("eventbus: topic must not be empty")
	ErrDuplicateSubscription = errors.New("eventbus: duplicate handler subscription for topic")
	ErrSubscriptionNotFound  = errors.New("eventbus: subscription not found")
	ErrQueueFull             = errors.New("eventbus: pending event queue is full")
	ErrNoDispatch            = errors.New("eventbus: stop propagation called outside a dispatched handler")
	ErrNilHandler            = errors.New("eventbus: handler must not be nil")
)

type Handler func(Event)

type Event struct {
	Topic   string
	Payload any

	sequence uint64
	token    uint64
	bus      *Bus
}

func (event Event) Sequence() uint64 { return event.sequence }

func (event Event) StopPropagation() error {
	if event.bus == nil {
		return ErrNoDispatch
	}
	return event.bus.stopPropagation(event.sequence, event.token)
}

type ErrorRecord struct {
	HandlerID string
	Sequence  uint64
}

type Bus struct {
	mu            sync.Mutex
	maxQueue      int
	nextSequence  uint64
	nextOrder     uint64
	subscriptions map[string][]subscription
	pending       []queuedEvent
	dispatching   bool
	dispatcherID  uint64
	currentEvent  uint64
	currentToken  uint64
	nextToken     uint64
	stopped       bool
	errorRecords  []ErrorRecord
}

type subscription struct {
	id       string
	priority int
	order    uint64
	handler  Handler
}

type queuedEvent struct {
	event Event
}

func New(maxQueue int) *Bus {
	if maxQueue < 0 {
		panic("eventbus: maxQueue must not be negative")
	}
	return &Bus{
		maxQueue:      maxQueue,
		subscriptions: make(map[string][]subscription),
	}
}

func (bus *Bus) Subscribe(topic string, handlerID string, priority int, handler Handler) error {
	if topic == "" {
		return ErrEmptyTopic
	}
	if handler == nil {
		return ErrNilHandler
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()

	for _, candidate := range bus.subscriptions[topic] {
		if candidate.id == handlerID {
			return ErrDuplicateSubscription
		}
	}

	bus.nextOrder++
	bus.subscriptions[topic] = append(bus.subscriptions[topic], subscription{
		id:       handlerID,
		priority: priority,
		order:    bus.nextOrder,
		handler:  handler,
	})
	return nil
}

func (bus *Bus) Unsubscribe(topic string, handlerID string) error {
	if topic == "" {
		return ErrEmptyTopic
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()

	entries := bus.subscriptions[topic]
	for index, candidate := range entries {
		if candidate.id == handlerID {
			bus.subscriptions[topic] = append(entries[:index], entries[index+1:]...)
			if len(bus.subscriptions[topic]) == 0 {
				delete(bus.subscriptions, topic)
			}
			return nil
		}
	}

	return ErrSubscriptionNotFound
}

func (bus *Bus) Publish(topic string, payload any) (sequence uint64, dispatched uint64, err error) {
	if topic == "" {
		return 0, 0, ErrEmptyTopic
	}

	publisherGoroutine := goroutineID()

	bus.mu.Lock()
	if bus.dispatching && len(bus.pending) >= bus.maxQueue {
		bus.mu.Unlock()
		return 0, 0, ErrQueueFull
	}
	bus.nextSequence++
	sequence = bus.nextSequence
	bus.nextToken++
	event := Event{
		Topic:    topic,
		Payload:  payload,
		sequence: sequence,
		token:    bus.nextToken,
		bus:      bus,
	}
	bus.pending = append(bus.pending, queuedEvent{event: event})
	shouldDispatch := !bus.dispatching
	if shouldDispatch {
		bus.dispatching = true
		bus.dispatcherID = publisherGoroutine
	}
	bus.mu.Unlock()

	if !shouldDispatch {
		return sequence, 0, nil
	}

	dispatched = bus.drain()
	return sequence, dispatched, nil
}

func (bus *Bus) StopPropagation() error {
	return bus.stopPropagation(0, 0)
}

func (bus *Bus) stopPropagation(sequence uint64, token uint64) error {
	bus.mu.Lock()
	defer bus.mu.Unlock()

	if !bus.dispatching || bus.dispatcherID != goroutineID() || bus.currentEvent == 0 {
		return ErrNoDispatch
	}
	if sequence != bus.currentEvent || token != bus.currentToken {
		return ErrNoDispatch
	}
	bus.stopped = true
	return nil
}

func (bus *Bus) Errors() []ErrorRecord {
	bus.mu.Lock()
	defer bus.mu.Unlock()

	records := make([]ErrorRecord, len(bus.errorRecords))
	copy(records, bus.errorRecords)
	return records
}

func (bus *Bus) drain() uint64 {
	var dispatched uint64

	for {
		bus.mu.Lock()
		if len(bus.pending) == 0 {
			bus.dispatching = false
			bus.dispatcherID = 0
			bus.currentEvent = 0
			bus.currentToken = 0
			bus.stopped = false
			bus.mu.Unlock()
			return dispatched
		}

		next := bus.pending[0]
		bus.pending = bus.pending[1:]
		handlers := bus.snapshotHandlersLocked(next.event.Topic)
		bus.currentEvent = next.event.sequence
		bus.currentToken = next.event.token
		bus.stopped = false
		bus.mu.Unlock()

		for _, handler := range handlers {
			bus.mu.Lock()
			stop := bus.stopped
			active := bus.isSubscribedLocked(next.event.Topic, handler.order)
			bus.mu.Unlock()
			if stop {
				break
			}
			if !active {
				continue
			}

			bus.invokeHandler(next.event, handler)
		}
		dispatched++
	}
}

func (bus *Bus) snapshotHandlersLocked(topic string) []subscription {
	entries := bus.subscriptions[topic]
	if len(entries) == 0 {
		return nil
	}

	snapshot := make([]subscription, len(entries))
	copy(snapshot, entries)
	sort.SliceStable(snapshot, func(left int, right int) bool {
		if snapshot[left].priority != snapshot[right].priority {
			return snapshot[left].priority > snapshot[right].priority
		}
		return snapshot[left].order < snapshot[right].order
	})
	return snapshot
}

func (bus *Bus) isSubscribedLocked(topic string, order uint64) bool {
	for _, candidate := range bus.subscriptions[topic] {
		if candidate.order == order {
			return true
		}
	}
	return false
}

func (bus *Bus) invokeHandler(event Event, handler subscription) {
	defer func() {
		if recovered := recover(); recovered != nil {
			bus.mu.Lock()
			bus.errorRecords = append(bus.errorRecords, ErrorRecord{
				HandlerID: handler.id,
				Sequence:  event.sequence,
			})
			bus.mu.Unlock()
		}
	}()

	handler.handler(event)
}

func goroutineID() uint64 {
	var buffer [64]byte
	size := runtime.Stack(buffer[:], false)
	var id uint64
	for index := len("goroutine "); index < size; index++ {
		if buffer[index] < '0' || buffer[index] > '9' {
			break
		}
		id = id*10 + uint64(buffer[index]-'0')
	}
	return id
}
