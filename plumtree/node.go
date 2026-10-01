package plumtree

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrEmptyNodeID        = errors.New("node id must not be empty")
	ErrNoNeighbors        = errors.New("neighbors must not be empty")
	ErrDuplicateNeighbor  = errors.New("neighbors must be distinct")
	ErrEmptyNeighborID    = errors.New("neighbor id must not be empty")
	ErrNeighborIsSelf     = errors.New("neighbor id must not equal self id")
	ErrInvalidT1          = errors.New("T1 must be positive")
	ErrInvalidT2          = errors.New("T2 must be positive")
	ErrClockBackwards     = errors.New("clock moved backwards")
	ErrEmptyMessageID     = errors.New("message id must not be empty")
	ErrUnknownNeighbor    = errors.New("from must be a neighbor")
	ErrMessageAlreadySeen = errors.New("message id already seen")
)

type MessageType string

const (
	Gossip MessageType = "GOSSIP"
	IHave  MessageType = "IHAVE"
	Prune  MessageType = "PRUNE"
	Graft  MessageType = "GRAFT"
)

type Message struct {
	Target  string
	Type    MessageType
	ID      string
	Payload string
}

type Node struct {
	mu             sync.Mutex
	self           string
	neighbors      map[string]struct{}
	eager          map[string]struct{}
	lazy           map[string]struct{}
	seen           map[string]string
	advisories     map[string][]string
	deadlines      map[string]int64
	t1             int64
	t2             int64
	lastNow        int64
	haveClockEntry bool
}

func NewNode(self string, neighbors []string, t1 int64, t2 int64) (*Node, error) {
	if self == "" {
		return nil, ErrEmptyNodeID
	}
	if len(neighbors) == 0 {
		return nil, ErrNoNeighbors
	}

	neighborSet := make(map[string]struct{}, len(neighbors))
	for _, neighbor := range neighbors {
		if neighbor == "" {
			return nil, ErrEmptyNeighborID
		}
		if neighbor == self {
			return nil, ErrNeighborIsSelf
		}
		if _, exists := neighborSet[neighbor]; exists {
			return nil, ErrDuplicateNeighbor
		}
		neighborSet[neighbor] = struct{}{}
	}
	if t1 <= 0 {
		return nil, ErrInvalidT1
	}
	if t2 <= 0 {
		return nil, ErrInvalidT2
	}

	return &Node{
		self:       self,
		neighbors:  neighborSet,
		eager:      cloneSet(neighborSet),
		lazy:       map[string]struct{}{},
		seen:       map[string]string{},
		advisories: map[string][]string{},
		deadlines:  map[string]int64{},
		t1:         t1,
		t2:         t2,
	}, nil
}

func (n *Node) Broadcast(now int64, id, payload string) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if err := n.validateTimedCall(now); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrEmptyMessageID
	}
	if _, exists := n.seen[id]; exists {
		return nil, ErrMessageAlreadySeen
	}

	n.lastNow = now
	n.haveClockEntry = true
	n.seen[id] = payload

	messages := make([]Message, 0, len(n.eager)+len(n.lazy))
	for _, neighbor := range sorted(n.eager) {
		messages = append(messages, Message{Target: neighbor, Type: Gossip, ID: id, Payload: payload})
	}
	for _, neighbor := range sorted(n.lazy) {
		messages = append(messages, Message{Target: neighbor, Type: IHave, ID: id})
	}
	return messages, nil
}

func (n *Node) OnGossip(now int64, from, id, payload string) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if err := n.validateTimedCall(now); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrEmptyMessageID
	}
	if err := n.validateNeighbor(from); err != nil {
		return nil, err
	}

	n.lastNow = now
	n.haveClockEntry = true

	if _, exists := n.seen[id]; exists {
		delete(n.eager, from)
		n.lazy[from] = struct{}{}
		return []Message{{Target: from, Type: Prune}}, nil
	}

	n.seen[id] = payload
	n.removeAdvisory(id)

	messages := make([]Message, 0, len(n.eager)+len(n.lazy))
	for _, neighbor := range sortedExcept(n.eager, from) {
		messages = append(messages, Message{Target: neighbor, Type: Gossip, ID: id, Payload: payload})
	}
	for _, neighbor := range sortedExcept(n.lazy, from) {
		messages = append(messages, Message{Target: neighbor, Type: IHave, ID: id})
	}

	delete(n.lazy, from)
	n.eager[from] = struct{}{}
	return messages, nil
}

func (n *Node) OnIHave(now int64, from, id string) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if err := n.validateTimedCall(now); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrEmptyMessageID
	}
	if err := n.validateNeighbor(from); err != nil {
		return nil, err
	}

	n.lastNow = now
	n.haveClockEntry = true

	if _, exists := n.seen[id]; exists {
		return []Message{}, nil
	}

	for _, adviser := range n.advisories[id] {
		if adviser == from {
			return []Message{}, nil
		}
	}

	n.advisories[id] = append(n.advisories[id], from)
	if _, hasTimer := n.deadlines[id]; !hasTimer {
		n.deadlines[id] = now + n.t1
	}
	return []Message{}, nil
}

func (n *Node) OnPrune(now int64, from string) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if err := n.validateTimedCall(now); err != nil {
		return nil, err
	}
	if err := n.validateNeighbor(from); err != nil {
		return nil, err
	}

	n.lastNow = now
	n.haveClockEntry = true
	delete(n.eager, from)
	n.lazy[from] = struct{}{}
	return []Message{}, nil
}

func (n *Node) OnGraft(now int64, from, id string) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if err := n.validateTimedCall(now); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrEmptyMessageID
	}
	if err := n.validateNeighbor(from); err != nil {
		return nil, err
	}

	n.lastNow = now
	n.haveClockEntry = true
	delete(n.lazy, from)
	n.eager[from] = struct{}{}

	if payload, exists := n.seen[id]; exists {
		return []Message{{Target: from, Type: Gossip, ID: id, Payload: payload}}, nil
	}
	return []Message{}, nil
}

func (n *Node) Tick(now int64) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if err := n.validateTimedCall(now); err != nil {
		return nil, err
	}
	n.lastNow = now
	n.haveClockEntry = true

	ids := make([]string, 0, len(n.deadlines))
	for id, deadline := range n.deadlines {
		if deadline <= now {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	messages := []Message{}
	for _, id := range ids {
		advisers := n.advisories[id]
		if len(advisers) == 0 {
			n.removeAdvisory(id)
			continue
		}

		target := advisers[0]
		n.advisories[id] = advisers[1:]
		delete(n.lazy, target)
		n.eager[target] = struct{}{}
		messages = append(messages, Message{Target: target, Type: Graft, ID: id})

		if len(n.advisories[id]) == 0 {
			n.removeAdvisory(id)
		} else {
			n.deadlines[id] = now + n.t2
		}
	}
	return messages, nil
}

func (n *Node) validateTimedCall(now int64) error {
	if n.haveClockEntry && now < n.lastNow {
		return ErrClockBackwards
	}
	return nil
}

func (n *Node) validateNeighbor(neighbor string) error {
	if _, exists := n.neighbors[neighbor]; !exists {
		return ErrUnknownNeighbor
	}
	return nil
}

func (n *Node) removeAdvisory(id string) {
	delete(n.advisories, id)
	delete(n.deadlines, id)
}

func cloneSet(source map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(source))
	for value := range source {
		result[value] = struct{}{}
	}
	return result
}

func sorted(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedExcept(values map[string]struct{}, excluded string) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if value != excluded {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
