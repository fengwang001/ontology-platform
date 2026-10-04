// Package node holds the cluster state: nodes, indexes and the placement
// of every shard copy. All state access must hold the cluster lock.
package node

import (
	"errors"
	"sort"
	"sync"
)

// Sentinel errors, ordered by the required rejection precedence.
var (
	ErrInvalidArg    = errors.New("node: invalid argument")
	ErrAlreadyExists = errors.New("node: already exists")
	ErrNotFound      = errors.New("node: not found")
	ErrShardMissing  = errors.New("node: shard does not exist")
)

// CopyKey identifies one copy of a shard.
type CopyKey struct {
	Index   string
	Shard   int
	Replica int // 0-based replica number; meaningful only when Primary is false
	Primary bool
	Size    int64
}

type copyID struct {
	index   string
	shard   int
	replica int
	primary bool
}

func (k CopyKey) id() copyID {
	return copyID{index: k.Index, shard: k.Shard, replica: k.Replica, primary: k.Primary}
}

// NodeView is a read-only snapshot of a node.
type NodeView struct {
	ID      string
	Zone    string
	Total   int64
	Other   int64
	Exclude bool
	Copies  []CopyKey
}

// IndexView is a read-only snapshot of an index.
type IndexView struct {
	Name string
	S    int
	R    int
	Size int64
}

type nodeState struct {
	id      string
	zone    string
	total   int64
	other   int64
	exclude bool
	copies  map[copyID]int64 // copy id -> size
}

type indexState struct {
	s    int
	r    int
	size int64
}

// Cluster is the mutable cluster state.
type Cluster struct {
	L, H  int
	mu    sync.RWMutex
	nodes map[string]*nodeState
	idxs  map[string]*indexState
}

// NewCluster creates a cluster with low/high watermark percentages.
func NewCluster(lowPct, highPct int) (*Cluster, error) {
	if lowPct < 1 || highPct < lowPct || highPct > 100 {
		return nil, ErrInvalidArg
	}
	return &Cluster{
		L:     lowPct,
		H:     highPct,
		nodes: map[string]*nodeState{},
		idxs:  map[string]*indexState{},
	}, nil
}

func (c *Cluster) Lock()    { c.mu.Lock() }
func (c *Cluster) Unlock()  { c.mu.Unlock() }
func (c *Cluster) RLock()   { c.mu.RLock() }
func (c *Cluster) RUnlock() { c.mu.RUnlock() }

func validName(s string) bool {
	n := len([]byte(s))
	return n >= 1 && n <= 64
}

// AddNode registers a node.
// Public operations acquire the cluster lock themselves; code already holding
// the lock (the allocator) uses the Locked variants.
func (c *Cluster) AddNode(id, zone string, total int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.AddNodeLocked(id, zone, total)
}

// AddNodeLocked is AddNode for callers already holding the write lock.
func (c *Cluster) AddNodeLocked(id, zone string, total int64) error {
	if !validName(id) || !validName(zone) || total < 1 || total > 1e12 {
		return ErrInvalidArg
	}
	if _, ok := c.nodes[id]; ok {
		return ErrAlreadyExists
	}
	c.nodes[id] = &nodeState{id: id, zone: zone, total: total, copies: map[copyID]int64{}}
	return nil
}

// SetOther sets the non-shard usage of a node.
func (c *Cluster) SetOther(id string, v int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.SetOtherLocked(id, v)
}

// SetOtherLocked is SetOther for callers already holding the write lock.
func (c *Cluster) SetOtherLocked(id string, v int64) error {
	if v < 0 || v > 1e12 {
		return ErrInvalidArg
	}
	n, ok := c.nodes[id]
	if !ok {
		return ErrNotFound
	}
	n.other = v
	return nil
}

// SetExclude toggles the exclusion flag of a node.
func (c *Cluster) SetExclude(id string, on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.SetExcludeLocked(id, on)
}

// SetExcludeLocked is SetExclude for callers already holding the write lock.
func (c *Cluster) SetExcludeLocked(id string, on bool) error {
	n, ok := c.nodes[id]
	if !ok {
		return ErrNotFound
	}
	n.exclude = on
	return nil
}

// CreateIndex registers an index; all primary and replica copies start unassigned.
func (c *Cluster) CreateIndex(name string, shards, replicas int, size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.CreateIndexLocked(name, shards, replicas, size)
}

// CreateIndexLocked is CreateIndex for callers already holding the write lock.
func (c *Cluster) CreateIndexLocked(name string, shards, replicas int, size int64) error {
	if !validName(name) || shards < 1 || shards > 64 ||
		replicas < 0 || replicas > 5 || size < 1 || size > 1e12 {
		return ErrInvalidArg
	}
	if _, ok := c.idxs[name]; ok {
		return ErrAlreadyExists
	}
	c.idxs[name] = &indexState{s: shards, r: replicas, size: size}
	return nil
}

func sortedNodeIDs(c *Cluster) []string {
	ids := make([]string, 0, len(c.nodes))
	for id := range c.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Nodes returns node snapshots in id byte order.
func (c *Cluster) Nodes() []NodeView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.NodesLocked()
}

// NodesLocked is Nodes for callers already holding a lock.
func (c *Cluster) NodesLocked() []NodeView {
	out := make([]NodeView, 0, len(c.nodes))
	for _, id := range sortedNodeIDs(c) {
		n := c.nodes[id]
		keys := make([]CopyKey, 0, len(n.copies))
		for k, sz := range n.copies {
			keys = append(keys, CopyKey{Index: k.index, Shard: k.shard, Replica: k.replica, Primary: k.primary, Size: sz})
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Index != keys[j].Index {
				return keys[i].Index < keys[j].Index
			}
			if keys[i].Shard != keys[j].Shard {
				return keys[i].Shard < keys[j].Shard
			}
			if keys[i].Primary != keys[j].Primary {
				return keys[i].Primary
			}
			return keys[i].Replica < keys[j].Replica
		})
		out = append(out, NodeView{
			ID:      n.id,
			Zone:    n.zone,
			Total:   n.total,
			Other:   n.other,
			Exclude: n.exclude,
			Copies:  keys,
		})
	}
	return out
}

// Indexes returns index snapshots in name byte order.
func (c *Cluster) Indexes() []IndexView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.IndexesLocked()
}

// IndexesLocked is Indexes for callers already holding a lock.
func (c *Cluster) IndexesLocked() []IndexView {
	names := make([]string, 0, len(c.idxs))
	for name := range c.idxs {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]IndexView, 0, len(names))
	for _, name := range names {
		ix := c.idxs[name]
		out = append(out, IndexView{Name: name, S: ix.s, R: ix.r, Size: ix.size})
	}
	return out
}

// Index returns the index definition; ok is false when absent.
func (c *Cluster) Index(name string) (IndexView, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.IndexLocked(name)
}

// IndexLocked is Index for callers already holding a lock.
func (c *Cluster) IndexLocked(name string) (IndexView, bool) {
	ix, ok := c.idxs[name]
	if !ok {
		return IndexView{}, false
	}
	return IndexView{Name: name, S: ix.s, R: ix.r, Size: ix.size}, true
}

// Used is non-shard usage plus the sizes of all copies on the node.
func (c *Cluster) Used(id string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.UsedLocked(id)
}

// UsedLocked is Used for callers already holding a lock.
func (c *Cluster) UsedLocked(id string) int64 {
	n := c.nodes[id]
	used := n.other
	for _, sz := range n.copies {
		used += sz
	}
	return used
}

// CopyCount is the number of copies on the node.
func (c *Cluster) CopyCount(id string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.CopyCountLocked(id)
}

// CopyCountLocked is CopyCount for callers already holding a lock.
func (c *Cluster) CopyCountLocked(id string) int {
	return len(c.nodes[id].copies)
}

// Placement returns the node holding the given copy, if assigned.
func (c *Cluster) Placement(k CopyKey) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PlacementLocked(k)
}

// PlacementLocked is Placement for callers already holding a lock.
func (c *Cluster) PlacementLocked(k CopyKey) (string, bool) {
	for id, n := range c.nodes {
		if _, ok := n.copies[k.id()]; ok {
			return id, true
		}
	}
	return "", false
}

// ZoneCopies counts copies of shard (idx,s) currently in zone, across all nodes,
// excluded nodes included.
func (c *Cluster) ZoneCopies(zone, idx string, s int) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ZoneCopiesLocked(zone, idx, s)
}

// ZoneCopiesLocked is ZoneCopies for callers already holding a lock.
func (c *Cluster) ZoneCopiesLocked(zone, idx string, s int) int {
	count := 0
	for _, n := range c.nodes {
		if n.zone != zone {
			continue
		}
		for k := range n.copies {
			if k.index == idx && k.shard == s {
				count++
			}
		}
	}
	return count
}

// ZoneCount is the number of distinct zones over all nodes, excluded ones included.
func (c *Cluster) ZoneCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ZoneCountLocked()
}

// ZoneCountLocked is ZoneCount for callers already holding a lock.
func (c *Cluster) ZoneCountLocked() int {
	zones := map[string]struct{}{}
	for _, n := range c.nodes {
		zones[n.zone] = struct{}{}
	}
	return len(zones)
}

// Put assigns a copy to the node. Caller must hold the write lock and ensure
// no duplicate placement of the same copy.
func (c *Cluster) PutLocked(id string, k CopyKey) {
	c.nodes[id].copies[k.id()] = k.Size
}

// Remove takes a copy off the node; a missing copy is a no-op.
func (c *Cluster) RemoveLocked(id string, k CopyKey) {
	delete(c.nodes[id].copies, k.id())
}
