package coordinator

import (
	"errors"
	"sync"
)

var (
	ErrParam       = errors.New("coordinator: invalid parameter")
	ErrTerm        = errors.New("coordinator: invalid term")
	ErrRange       = errors.New("coordinator: index out of range")
	ErrCompacted   = errors.New("coordinator: index compacted")
	ErrNoProgress  = errors.New("coordinator: no snapshot progress")
	ErrExists      = errors.New("coordinator: peer already exists")
	ErrUnknownPeer = errors.New("coordinator: unknown peer")
	ErrInFlight    = errors.New("coordinator: snapshot already in flight")
	ErrNotNeeded   = errors.New("coordinator: snapshot not needed")
	ErrNotInFlight = errors.New("coordinator: snapshot not in flight")
)

type Plan struct {
	Kind      PlanKind
	Snapshot  int
	SnapTerm  int
	PrevIndex int
	PrevTerm  int
	From      int
	To        int
}

type PlanKind int

const (
	AppendEntries PlanKind = iota
	Installing
	NeedSnapshot
)

type Coordinator struct {
	mu        sync.Mutex
	tail      int
	threshold int
	lag       int
	last      int
	commit    int
	applied   int
	snapIndex int
	snapTerm  int
	baseIndex int
	baseTerm  int
	entries   []int
	peers     map[string]*peer
	removed   int
}

type peer struct {
	match        int
	next         int
	snapshot     int
	snapshotTerm int
	hasSnapshot  bool
}

func New(tail int, threshold int, lag int) (*Coordinator, error) {
	if tail < 0 || threshold < 1 || lag < 0 {
		return nil, ErrParam
	}
	return &Coordinator{
		tail:      tail,
		threshold: threshold,
		lag:       lag,
		peers:     make(map[string]*peer),
	}, nil
}

func (c *Coordinator) Append(term int) (int, error) {
	if term < 1 {
		return 0, ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.last > 0 {
		lastTerm, err := c.termAtLocked(c.last)
		if err != nil {
			return 0, err
		}
		if term < lastTerm {
			return 0, ErrTerm
		}
	}
	c.entries = append(c.entries, term)
	c.last++
	return c.last, nil
}

func (c *Coordinator) Commit(idx int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if idx < c.commit || idx > c.last {
		return ErrRange
	}
	c.commit = idx
	return nil
}

func (c *Coordinator) Apply(idx int) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if idx < c.applied || idx > c.commit {
		return false, ErrRange
	}

	c.applied = idx
	if idx-c.snapIndex < c.threshold {
		return false, nil
	}
	c.takeSnapshotLocked()
	return true, nil
}

func (c *Coordinator) Snapshot() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.applied <= c.snapIndex {
		return ErrNoProgress
	}
	c.takeSnapshotLocked()
	return nil
}

func (c *Coordinator) Compact() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.compactLocked()
}

func (c *Coordinator) TermAt(idx int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.termAtLocked(idx)
}

func (c *Coordinator) takeSnapshotLocked() {
	term, err := c.termAtLocked(c.applied)
	if err != nil {
		panic(err)
	}
	c.snapIndex = c.applied
	c.snapTerm = term
	c.compactLocked()
}

func (c *Coordinator) compactLocked() {
	cut := c.snapIndex - c.tail
	if cut < 0 {
		cut = 0
	}

	for _, follower := range c.peers {
		if follower.hasSnapshot {
			if follower.snapshot < cut {
				cut = follower.snapshot
			}
			continue
		}
		if c.last-follower.match <= c.lag {
			if follower.match < cut {
				cut = follower.match
			}
		}
	}

	if cut <= c.baseIndex {
		return
	}

	term, err := c.termAtLocked(cut)
	if err != nil {
		panic(err)
	}
	previousBase := c.baseIndex
	c.baseTerm = term
	c.baseIndex = cut
	c.entries = c.entries[cut-previousBase:]
	c.removed += cut - previousBase
}

func (c *Coordinator) termAtLocked(idx int) (int, error) {
	if idx == c.baseIndex {
		return c.baseTerm, nil
	}
	if idx < c.baseIndex {
		return 0, ErrCompacted
	}
	if idx > c.last {
		return 0, ErrRange
	}
	return c.entries[idx-c.baseIndex-1], nil
}

func (c *Coordinator) AddPeer(id string) error {
	if id == "" {
		return ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.peers[id]; ok {
		return ErrExists
	}
	c.peers[id] = &peer{next: c.last + 1}
	return nil
}

func (c *Coordinator) Ack(id string, match int) error {
	if id == "" {
		return ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	follower, ok := c.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if match < follower.match || match > c.last {
		return ErrRange
	}
	follower.match = match
	follower.next = match + 1
	return nil
}

func (c *Coordinator) Retreat(id string, next int) error {
	if id == "" {
		return ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	follower, ok := c.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if next <= follower.match || next > follower.next {
		return ErrRange
	}
	follower.next = next
	return nil
}

func (c *Coordinator) DropPeer(id string) error {
	if id == "" {
		return ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.peers[id]; !ok {
		return ErrUnknownPeer
	}
	delete(c.peers, id)
	c.compactLocked()
	return nil
}

func (c *Coordinator) Plan(id string) (Plan, error) {
	if id == "" {
		return Plan{}, ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	follower, ok := c.peers[id]
	if !ok {
		return Plan{}, ErrUnknownPeer
	}
	return c.planLocked(follower), nil
}

func (c *Coordinator) StartSnapshot(id string) (int, int, error) {
	if id == "" {
		return 0, 0, ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	follower, ok := c.peers[id]
	if !ok {
		return 0, 0, ErrUnknownPeer
	}
	if follower.hasSnapshot {
		return 0, 0, ErrInFlight
	}
	if c.planLocked(follower).Kind != NeedSnapshot {
		return 0, 0, ErrNotNeeded
	}
	follower.hasSnapshot = true
	follower.snapshot = c.snapIndex
	follower.snapshotTerm = c.snapTerm
	return follower.snapshot, follower.snapshotTerm, nil
}

func (c *Coordinator) FinishSnapshot(id string) error {
	if id == "" {
		return ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	follower, ok := c.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if !follower.hasSnapshot {
		return ErrNotInFlight
	}
	snapshot := follower.snapshot
	follower.hasSnapshot = false
	follower.snapshot = 0
	follower.snapshotTerm = 0
	if snapshot > follower.match {
		follower.match = snapshot
	}
	follower.next = follower.match + 1
	c.compactLocked()
	return nil
}

func (c *Coordinator) AbortSnapshot(id string) error {
	if id == "" {
		return ErrParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	follower, ok := c.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if !follower.hasSnapshot {
		return ErrNotInFlight
	}
	follower.hasSnapshot = false
	follower.snapshot = 0
	follower.snapshotTerm = 0
	c.compactLocked()
	return nil
}

func (c *Coordinator) planLocked(follower *peer) Plan {
	if follower.hasSnapshot {
		return Plan{
			Kind:     Installing,
			Snapshot: follower.snapshot,
			SnapTerm: follower.snapshotTerm,
		}
	}
	if follower.next <= c.baseIndex {
		return Plan{
			Kind:     NeedSnapshot,
			Snapshot: c.snapIndex,
			SnapTerm: c.snapTerm,
		}
	}

	prevIndex := follower.next - 1
	prevTerm, err := c.termAtLocked(prevIndex)
	if err != nil {
		panic(err)
	}
	return Plan{
		Kind:      AppendEntries,
		PrevIndex: prevIndex,
		PrevTerm:  prevTerm,
		From:      follower.next,
		To:        c.last,
	}
}
