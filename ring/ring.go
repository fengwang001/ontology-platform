package ring

import (
	"errors"
	"sync"

	"ontology/change"
	"ontology/seen"
)

type WriteOutcome int

const (
	WriteApplied WriteOutcome = iota
	WriteLost
)

type DeliverOutcome int

const (
	DeliverApplied DeliverOutcome = iota
	DeliverLost
	DeliverDup
)

var (
	ErrInvalidArgument = errors.New("ring: invalid argument")
	ErrNotWritable     = errors.New("ring: site is not writable")
	ErrClockRollback   = errors.New("ring: clock moved backwards")
	ErrDown            = errors.New("ring: link is down")
	ErrEmpty           = errors.New("ring: link queue is empty")
)

type site struct {
	table  *change.Table
	clock  int64
	writeQ int64
}

type link struct {
	up    bool
	queue []change.Change
}

type Ring struct {
	mu      sync.Mutex
	n       int
	writers map[int]bool
	sites   []*site
	floors  *seen.Table
	links   [][]link
	stats   Stats
}

type Stats struct {
	Enqueued  int64
	Forwarded int64
	Dequeued  int64
	Dup       int64
}

func New(n int, writers []int) (*Ring, error) {
	if n < 3 || n > 8 || len(writers) == 0 {
		return nil, ErrInvalidArgument
	}
	writable := make(map[int]bool, len(writers))
	for _, writer := range writers {
		if writer < 1 || writer > n {
			return nil, ErrInvalidArgument
		}
		writable[writer] = true
	}
	sites := make([]*site, n+1)
	for idx := 1; idx <= n; idx++ {
		sites[idx] = &site{table: change.NewTable()}
	}
	links := make([][]link, n+1)
	for from := 1; from <= n; from++ {
		links[from] = make([]link, n+1)
		for to := 1; to <= n; to++ {
			if adjacent(n, from, to) {
				links[from][to].up = true
			}
		}
	}
	return &Ring{
		n:       n,
		writers: writable,
		sites:   sites,
		floors:  seen.New(n),
		links:   links,
	}, nil
}

func (r *Ring) Write(site int, key []byte, op change.Op, now int64) (WriteOutcome, error) {
	if err := r.validateSite(site); err != nil {
		return 0, err
	}
	if !validKey(key) || !validOp(op) || now < 0 || now > 1_000_000_000_000 {
		return 0, ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.writers[site] {
		return 0, ErrNotWritable
	}
	writer := r.sites[site]
	if now < writer.clock {
		return 0, ErrClockRollback
	}
	writer.writeQ++
	seq := writer.writeQ
	writer.clock = now
	r.floors.Set(site, site, seq)
	c := change.Change{
		Origin: site,
		Seq:    seq,
		TS:     now,
		Key:    append([]byte(nil), key...),
		Op:     cloneOp(op),
	}
	outcome := WriteLost
	if writer.table.Apply(c) {
		outcome = WriteApplied
	}
	for _, neighbor := range neighbors(r.n, site) {
		r.enqueue(site, neighbor, c)
		r.stats.Enqueued++
	}
	return outcome, nil
}

func (r *Ring) Deliver(from, to int) (DeliverOutcome, error) {
	if err := r.validateAdjacent(from, to); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.links[from][to].up {
		return 0, ErrDown
	}
	queue := &r.links[from][to].queue
	if len(*queue) == 0 {
		return 0, ErrEmpty
	}
	c := (*queue)[0]
	*queue = (*queue)[1:]
	r.stats.Dequeued++
	if !r.floors.Observe(to, c.Origin, c.Seq) {
		r.stats.Dup++
		return DeliverDup, nil
	}
	outcome := DeliverLost
	if r.sites[to].table.Apply(c) {
		outcome = DeliverApplied
	}
	other := otherNeighbor(r.n, to, from)
	r.enqueue(to, other, c)
	r.stats.Forwarded++
	r.stats.Enqueued++
	return outcome, nil
}

func (r *Ring) SetLink(from, to int, up bool) error {
	if err := r.validateAdjacent(from, to); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.links[from][to].up = up
	return nil
}

func (r *Ring) Floor(site, origin int) (int64, error) {
	if err := r.validateSite(site); err != nil {
		return 0, err
	}
	if err := r.validateSite(origin); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.floors.Floor(site, origin), nil
}

func (r *Ring) Get(site int, key []byte) (change.Entry, bool, error) {
	if err := r.validateSite(site); err != nil {
		return change.Entry{}, false, err
	}
	if !validKey(key) {
		return change.Entry{}, false, ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.sites[site].table.Get(key)
	return entry, ok, nil
}

func (r *Ring) Pending(from, to int) ([]change.Change, error) {
	if err := r.validateAdjacent(from, to); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneChanges(r.links[from][to].queue), nil
}

func (r *Ring) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

func (r *Ring) validateSite(site int) error {
	if site < 1 || site > r.n {
		return ErrInvalidArgument
	}
	return nil
}

func (r *Ring) validateAdjacent(from, to int) error {
	if err := r.validateSite(from); err != nil {
		return err
	}
	if err := r.validateSite(to); err != nil {
		return err
	}
	if !adjacent(r.n, from, to) {
		return ErrInvalidArgument
	}
	return nil
}

func (r *Ring) enqueue(from, to int, c change.Change) {
	clone := c
	clone.Key = append([]byte(nil), c.Key...)
	clone.Op = cloneOp(c.Op)
	r.links[from][to].queue = append(r.links[from][to].queue, clone)
}

func adjacent(n, from, to int) bool {
	if from == to {
		return false
	}
	return to == from%n+1 || from == to%n+1
}

func neighbors(n, site int) [2]int {
	return [2]int{site%n + 1, (site-2+n)%n + 1}
}

func otherNeighbor(n, site, except int) int {
	for _, neighbor := range neighbors(n, site) {
		if neighbor != except {
			return neighbor
		}
	}
	panic("ring: missing other neighbor")
}

func validKey(key []byte) bool {
	return len(key) > 0 && len(key) <= 32
}

func validOp(op change.Op) bool {
	switch op.(type) {
	case change.Put, change.Delete:
		return true
	default:
		return false
	}
}

func cloneOp(op change.Op) change.Op {
	switch value := op.(type) {
	case change.Put:
		return change.Put{Value: value.Value}
	case change.Delete:
		return change.Del
	default:
		panic("ring: invalid operation")
	}
}

func cloneChanges(changes []change.Change) []change.Change {
	cloned := make([]change.Change, len(changes))
	for idx, c := range changes {
		cloned[idx] = c
		cloned[idx].Key = append([]byte(nil), c.Key...)
		cloned[idx].Op = cloneOp(c.Op)
	}
	return cloned
}
