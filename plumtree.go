package ontology

import (
	"errors"
	"sort"
	"sync"
)

const (
	Gossip MessageType = "GOSSIP"
	IHave  MessageType = "IHAVE"
	Prune  MessageType = "PRUNE"
	Graft  MessageType = "GRAFT"
)

type MessageType string

type Message struct {
	Target  string
	Type    MessageType
	ID      string
	Payload string
}

type Node struct {
	mu        sync.Mutex
	self      string
	neighbors map[string]struct{}
	eager     map[string]struct{}
	lazy      map[string]struct{}
	messages  map[string]string
	pending   map[string]*advertisement
	t1        int
	t2        int
	lastNow   int
}

type advertisement struct {
	deadline int
	sources  []string
}

var (
	ErrEmptySelf        = errors.New("self identifier must be non-empty")
	ErrNoNeighbors      = errors.New("neighbors must not be empty")
	ErrEmptyNeighbor    = errors.New("neighbor identifiers must be non-empty")
	ErrNeighborIsSelf   = errors.New("neighbor set must not contain self")
	ErrInvalidT1        = errors.New("T1 must be a positive integer")
	ErrInvalidT2        = errors.New("T2 must be a positive integer")
	ErrClockMovedBack   = errors.New("clock moved backwards")
	ErrEmptyID          = errors.New("message id must be non-empty")
	ErrUnknownNeighbor  = errors.New("sender is not a neighbor")
	ErrAlreadyDelivered = errors.New("message id already delivered")
)

func NewNode(self string, neighbors map[string]struct{}, t1, t2 int) (*Node, error) {
	if self == "" {
		return nil, ErrEmptySelf
	}
	if len(neighbors) == 0 {
		return nil, ErrNoNeighbors
	}
	for _, neighbor := range sortedSet(neighbors, "") {
		if neighbor == "" {
			return nil, ErrEmptyNeighbor
		}
		if neighbor == self {
			return nil, ErrNeighborIsSelf
		}
	}
	if t1 <= 0 {
		return nil, ErrInvalidT1
	}
	if t2 <= 0 {
		return nil, ErrInvalidT2
	}

	copiedNeighbors := make(map[string]struct{}, len(neighbors))
	eager := make(map[string]struct{}, len(neighbors))
	for neighbor := range neighbors {
		copiedNeighbors[neighbor] = struct{}{}
		eager[neighbor] = struct{}{}
	}

	return &Node{
		self:      self,
		neighbors: copiedNeighbors,
		eager:     eager,
		lazy:      make(map[string]struct{}),
		messages:  make(map[string]string),
		pending:   make(map[string]*advertisement),
		t1:        t1,
		t2:        t2,
	}, nil
}

func (n *Node) Broadcast(now int, id, payload string) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClockMovedBack
	}
	if id == "" {
		return nil, ErrEmptyID
	}
	if _, seen := n.messages[id]; seen {
		return nil, ErrAlreadyDelivered
	}

	n.lastNow = now
	n.messages[id] = payload

	messages := n.pushToEager(n.sortedEager(), id, payload)
	messages = append(messages, n.notifyLazy(n.sortedLazy(), id)...)
	return messages, nil
}

func (n *Node) OnGossip(now int, from, id, payload string) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClockMovedBack
	}
	if id == "" {
		return nil, ErrEmptyID
	}
	if _, ok := n.neighbors[from]; !ok {
		return nil, ErrUnknownNeighbor
	}

	n.lastNow = now

	if _, seen := n.messages[id]; seen {
		delete(n.eager, from)
		n.lazy[from] = struct{}{}
		return []Message{{Target: from, Type: Prune}}, nil
	}

	n.messages[id] = payload
	delete(n.pending, id)

	messages := n.pushToEager(n.sortedEagerExcept(from), id, payload)
	messages = append(messages, n.notifyLazy(n.sortedLazyExcept(from), id)...)
	delete(n.lazy, from)
	n.eager[from] = struct{}{}
	return messages, nil
}

func (n *Node) OnIHave(now int, from, id string) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClockMovedBack
	}
	if id == "" {
		return nil, ErrEmptyID
	}
	if _, ok := n.neighbors[from]; !ok {
		return nil, ErrUnknownNeighbor
	}

	n.lastNow = now
	if _, seen := n.messages[id]; seen {
		return []Message{}, nil
	}

	advert := n.pending[id]
	if advert == nil {
		advert = &advertisement{deadline: now + n.t1}
		n.pending[id] = advert
	}
	for _, source := range advert.sources {
		if source == from {
			return []Message{}, nil
		}
	}
	advert.sources = append(advert.sources, from)
	return []Message{}, nil
}

func (n *Node) OnPrune(now int, from string) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClockMovedBack
	}
	if _, ok := n.neighbors[from]; !ok {
		return nil, ErrUnknownNeighbor
	}

	n.lastNow = now
	delete(n.eager, from)
	n.lazy[from] = struct{}{}
	return []Message{}, nil
}

func (n *Node) OnGraft(now int, from, id string) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClockMovedBack
	}
	if id == "" {
		return nil, ErrEmptyID
	}
	if _, ok := n.neighbors[from]; !ok {
		return nil, ErrUnknownNeighbor
	}

	n.lastNow = now
	delete(n.lazy, from)
	n.eager[from] = struct{}{}

	if payload, seen := n.messages[id]; seen {
		return []Message{{Target: from, Type: Gossip, ID: id, Payload: payload}}, nil
	}
	return []Message{}, nil
}

func (n *Node) Tick(now int) ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClockMovedBack
	}
	n.lastNow = now

	ids := make([]string, 0, len(n.pending))
	for id, advert := range n.pending {
		if advert.deadline <= now {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	messages := make([]Message, 0, len(ids))
	for _, id := range ids {
		advert := n.pending[id]
		source := advert.sources[0]
		advert.sources = advert.sources[1:]
		delete(n.lazy, source)
		n.eager[source] = struct{}{}
		messages = append(messages, Message{Target: source, Type: Graft, ID: id})

		if len(advert.sources) == 0 {
			delete(n.pending, id)
		} else {
			advert.deadline = now + n.t2
		}
	}
	return messages, nil
}

func (n *Node) pushToEager(targets []string, id, payload string) []Message {
	messages := make([]Message, 0, len(targets))
	for _, target := range targets {
		messages = append(messages, Message{Target: target, Type: Gossip, ID: id, Payload: payload})
	}
	return messages
}

func (n *Node) notifyLazy(targets []string, id string) []Message {
	messages := make([]Message, 0, len(targets))
	for _, target := range targets {
		messages = append(messages, Message{Target: target, Type: IHave, ID: id})
	}
	return messages
}

func (n *Node) sortedEager() []string {
	return sortedSet(n.eager, "")
}

func (n *Node) sortedLazy() []string {
	return sortedSet(n.lazy, "")
}

func (n *Node) sortedEagerExcept(excluded string) []string {
	return sortedSet(n.eager, excluded)
}

func (n *Node) sortedLazyExcept(excluded string) []string {
	return sortedSet(n.lazy, excluded)
}

func sortedSet(set map[string]struct{}, excluded string) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		if excluded == "" || value != excluded {
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values
}
