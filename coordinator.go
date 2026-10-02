package ontology

import (
	"errors"
	"sync"
)

var (
	ErrParam       = errors.New("invalid parameter")
	ErrTerm        = errors.New("invalid term")
	ErrRange       = errors.New("index out of range")
	ErrCompacted   = errors.New("index compacted")
	ErrNoProgress  = errors.New("snapshot has no new progress")
	ErrExists      = errors.New("peer already exists")
	ErrUnknownPeer = errors.New("unknown peer")
	ErrInFlight    = errors.New("snapshot already in flight")
	ErrNotNeeded   = errors.New("snapshot is not needed")
	ErrNotInFlight = errors.New("no snapshot in flight")
)

const (
	PlanAppend       PlanKind = "append"
	PlanNeedSnapshot PlanKind = "need-snapshot"
	PlanInstalling   PlanKind = "installing"
)

type PlanKind string

type Plan struct {
	Kind      PlanKind
	PrevIndex int
	PrevTerm  int
	From      int
	To        int
	SnapIndex int
	SnapTerm  int
}

type Coordinator struct {
	mu        sync.Mutex
	retain    int
	threshold int
	lag       int

	entries   []int
	last      int
	commit    int
	applied   int
	snapIndex int
	snapTerm  int
	baseIndex int
	baseTerm  int

	peers   map[string]*peer
	removed int
}

type peer struct {
	match        int
	next         int
	snapshot     int
	snapshotTerm int
	hasSnapshot  bool
}

func New(T, Thr, L int) (*Coordinator, error) {
	if T < 0 || Thr < 1 || L < 0 {
		return nil, ErrParam
	}

	return &Coordinator{
		retain:    T,
		threshold: Thr,
		lag:       L,
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

	if err := c.takeSnapshotLocked(); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Coordinator) Snapshot() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.applied <= c.snapIndex {
		return ErrNoProgress
	}

	return c.takeSnapshotLocked()
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

	p, ok := c.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if match < p.match || match > c.last {
		return ErrRange
	}

	p.match = match
	p.next = match + 1
	return nil
}

func (c *Coordinator) Retreat(id string, next int) error {
	if id == "" {
		return ErrParam
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if next <= p.match || next > p.next {
		return ErrRange
	}

	p.next = next
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

	p, ok := c.peers[id]
	if !ok {
		return Plan{}, ErrUnknownPeer
	}

	return c.planLocked(p), nil
}

func (c *Coordinator) StartSnapshot(id string) (int, int, error) {
	if id == "" {
		return 0, 0, ErrParam
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.peers[id]
	if !ok {
		return 0, 0, ErrUnknownPeer
	}
	if p.hasSnapshot {
		return 0, 0, ErrInFlight
	}
	if c.planLocked(p).Kind != PlanNeedSnapshot {
		return 0, 0, ErrNotNeeded
	}

	p.hasSnapshot = true
	p.snapshot = c.snapIndex
	p.snapshotTerm = c.snapTerm
	return p.snapshot, p.snapshotTerm, nil
}

func (c *Coordinator) FinishSnapshot(id string) error {
	if id == "" {
		return ErrParam
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if !p.hasSnapshot {
		return ErrNotInFlight
	}

	snapshot := p.snapshot
	p.hasSnapshot = false
	p.snapshot = 0
	p.snapshotTerm = 0
	if snapshot > p.match {
		p.match = snapshot
	}
	p.next = p.match + 1
	c.compactLocked()
	return nil
}

func (c *Coordinator) AbortSnapshot(id string) error {
	if id == "" {
		return ErrParam
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.peers[id]
	if !ok {
		return ErrUnknownPeer
	}
	if !p.hasSnapshot {
		return ErrNotInFlight
	}

	p.hasSnapshot = false
	p.snapshot = 0
	p.snapshotTerm = 0
	c.compactLocked()
	return nil
}

func (c *Coordinator) Removed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.removed
}

func (c *Coordinator) takeSnapshotLocked() error {
	term, err := c.termAtLocked(c.applied)
	if err != nil {
		return err
	}

	c.snapIndex = c.applied
	c.snapTerm = term
	c.compactLocked()
	return nil
}

func (c *Coordinator) compactLocked() {
	cut := c.snapIndex - c.retain
	if cut < 0 {
		cut = 0
	}

	for _, p := range c.peers {
		if p.hasSnapshot {
			if p.snapshot < cut {
				cut = p.snapshot
			}
			continue
		}
		if c.last-p.match <= c.lag {
			if p.match < cut {
				cut = p.match
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

	count := cut - c.baseIndex
	c.entries = c.entries[count:]
	c.baseIndex = cut
	c.baseTerm = term
	c.removed += count
}

func (c *Coordinator) termAtLocked(idx int) (int, error) {
	if idx < c.baseIndex {
		return 0, ErrCompacted
	}
	if idx == c.baseIndex {
		return c.baseTerm, nil
	}
	if idx > c.last {
		return 0, ErrRange
	}
	return c.entries[idx-c.baseIndex-1], nil
}

func (c *Coordinator) planLocked(p *peer) Plan {
	if p.hasSnapshot {
		return Plan{
			Kind:      PlanInstalling,
			SnapIndex: p.snapshot,
			SnapTerm:  p.snapshotTerm,
		}
	}

	if p.next <= c.baseIndex {
		return Plan{
			Kind:      PlanNeedSnapshot,
			SnapIndex: c.snapIndex,
			SnapTerm:  c.snapTerm,
		}
	}

	prevIndex := p.next - 1
	prevTerm, err := c.termAtLocked(prevIndex)
	if err != nil {
		panic(err)
	}

	return Plan{
		Kind:      PlanAppend,
		PrevIndex: prevIndex,
		PrevTerm:  prevTerm,
		From:      p.next,
		To:        c.last,
	}
}
