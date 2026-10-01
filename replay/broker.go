package replay

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrQueueNotFound      = errors.New("queue not found")
	ErrQueueAlreadyExists = errors.New("queue already exists")
	ErrQueueFull          = errors.New("queue full")
	ErrQueueEmpty         = errors.New("queue empty")
	ErrNoDeadEntries      = errors.New("no dead-letter entries")
	ErrReplayLimitReached = errors.New("replay limit reached")
)

type Message struct {
	OSeq    int
	Payload string
	Replays int
}

type DeadEntry struct {
	Message
	DSeq int
}

type queueState struct {
	messages []*Message
	dead     []*DeadEntry
}

type Broker struct {
	mu         sync.RWMutex
	capacity   int
	maxReplays int
	nextOSeq   int
	nextDSeq   int
	queues     map[string]*queueState
}

func NewBroker(capacity, maxReplays int, queueNames ...string) (*Broker, error) {
	if capacity < 1 {
		return nil, ErrInvalidArgument
	}
	if maxReplays < 0 {
		return nil, ErrInvalidArgument
	}

	broker := &Broker{
		capacity:   capacity,
		maxReplays: maxReplays,
		nextOSeq:   1,
		nextDSeq:   1,
		queues:     make(map[string]*queueState),
	}
	for _, name := range queueNames {
		if err := broker.AddQueue(name); err != nil {
			return nil, err
		}
	}
	return broker, nil
}

func (b *Broker) AddQueue(name string) error {
	if name == "" {
		return ErrInvalidArgument
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.queues[name]; ok {
		return ErrQueueAlreadyExists
	}
	b.queues[name] = &queueState{}
	return nil
}

func (b *Broker) Enqueue(queue, payload string) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	state, err := b.queueState(queue)
	if err != nil {
		return 0, err
	}
	if len(state.messages) >= b.capacity {
		return 0, ErrQueueFull
	}

	oseq := b.nextOSeq
	b.nextOSeq++
	state.messages = append(state.messages, &Message{
		OSeq:    oseq,
		Payload: payload,
	})
	return oseq, nil
}

func (b *Broker) Dequeue(queue string) (Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	state, err := b.queueState(queue)
	if err != nil {
		return Message{}, err
	}
	if len(state.messages) == 0 {
		return Message{}, ErrQueueEmpty
	}

	message := state.messages[0]
	state.messages = state.messages[1:]
	return *message, nil
}

func (b *Broker) SendToDeadLetter(queue string) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	state, err := b.queueState(queue)
	if err != nil {
		return 0, err
	}
	if len(state.messages) == 0 {
		return 0, ErrQueueEmpty
	}

	message := state.messages[0]
	state.messages = state.messages[1:]

	dseq := b.nextDSeq
	b.nextDSeq++
	state.dead = append(state.dead, &DeadEntry{
		Message: *message,
		DSeq:    dseq,
	})
	return dseq, nil
}

func (b *Broker) Replay(queue string, max int) (int, error) {
	if max < 1 || queue == "" {
		return 0, ErrInvalidArgument
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	state, err := b.queueState(queue)
	if err != nil {
		return 0, err
	}
	if len(state.dead) == 0 {
		return 0, ErrNoDeadEntries
	}

	candidates := make([]*DeadEntry, 0, len(state.dead))
	for _, entry := range state.dead {
		if entry.Replays < b.maxReplays {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		return 0, ErrReplayLimitReached
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].OSeq < candidates[j].OSeq
	})

	if len(state.messages) >= b.capacity {
		return 0, ErrQueueFull
	}

	replayed := 0
	for _, entry := range candidates {
		if replayed >= max || len(state.messages) >= b.capacity {
			break
		}

		entry.Replays++
		state.messages = append(state.messages, &entry.Message)
		state.removeDead(entry)
		replayed++
	}
	return replayed, nil
}

func (b *Broker) Snapshot() (map[string][]Message, map[string][]DeadEntry) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	queues := make(map[string][]Message, len(b.queues))
	dead := make(map[string][]DeadEntry, len(b.queues))
	for name, state := range b.queues {
		queues[name] = copyMessages(state.messages)
		dead[name] = copyDeadEntries(state.dead)
	}
	return queues, dead
}

func (b *Broker) queueState(name string) (*queueState, error) {
	if name == "" {
		return nil, ErrInvalidArgument
	}
	state, ok := b.queues[name]
	if !ok {
		return nil, ErrQueueNotFound
	}
	return state, nil
}

func (q *queueState) removeDead(target *DeadEntry) {
	for i, entry := range q.dead {
		if entry == target {
			q.dead = append(q.dead[:i], q.dead[i+1:]...)
			return
		}
	}
}

func copyMessages(messages []*Message) []Message {
	result := make([]Message, len(messages))
	for i, message := range messages {
		result[i] = *message
	}
	return result
}

func copyDeadEntries(entries []*DeadEntry) []DeadEntry {
	result := make([]DeadEntry, len(entries))
	for i, entry := range entries {
		result[i] = *entry
	}
	return result
}
