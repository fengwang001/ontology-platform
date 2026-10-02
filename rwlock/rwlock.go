// Package rwlock implements a ZooKeeper-style sequential ephemeral-node
// read/write lock recipe as a deterministic coordination model.
package rwlock

import (
	"errors"
	"sort"
	"sync"
)

// Kind is the request kind of a lock child node.
type Kind rune

const (
	Read  Kind = 'R'
	Write Kind = 'W'
)

func (k Kind) valid() bool { return k == Read || k == Write }

// Sentinel errors, returned in the documented precedence order.
var (
	ErrInvalid   = errors.New("rwlock: invalid argument")
	ErrNoSession = errors.New("rwlock: session does not exist")
	ErrExpired   = errors.New("rwlock: session expired")
	ErrFull      = errors.New("rwlock: lock is full")
	ErrNoNode    = errors.New("rwlock: node not found")
	ErrNotOwner  = errors.New("rwlock: not the owner of the node")
)

// Grant is one grant event produced while processing a deletion.
type Grant struct {
	Lock     string
	Seq      int64
	Kind     Kind
	Session  int64
	DeleteZX int64
}

// Result reports the outcome of a mutating operation.
type Result struct {
	Seq      int64
	ZXID     int64
	Held     bool
	Watching int64 // watched child sequence; -1 when held
	WS       int64 // watch registration sequence; 0 when held
	Events   []Grant
}

// Node describes one live lock child.
type Node struct {
	Seq     int64
	Kind    Kind
	Session int64
	Held    bool
	Watches int64 // number of waiters currently watching this node
}

// Coordinator is the concurrency-safe lock coordination model.
type Coordinator struct {
	mu  sync.Mutex
	C   int64
	sid int64
	zx  int64
	ws  int64

	sessions map[int64]bool // sid -> alive
	locks    map[string]*lockState
}

// New creates a Coordinator with per-lock child capacity c (1..1e6).
func New(c int64) *Coordinator {
	if c < 1 || c > 1_000_000 {
		panic("rwlock: capacity must be in [1, 1000000]")
	}
	return &Coordinator{
		C:        c,
		sessions: map[int64]bool{},
		locks:    map[string]*lockState{},
	}
}

// lockState holds all live children of one named lock plus its sequence
// counter. The child index lets ownership/session removals find nodes
// without scanning the AVL tree.
type lockState struct {
	cs   int64
	tree tree
	byS  map[int64]*child

	// watchers maps watched child sequence to waiters currently watching
	// it, kept sorted by ascending watch-registration sequence (ws).
	watchers map[int64][]*child

	// lastReevals / lastProbes record the cost of the most recent deletion
	// round: number of waiter re-evaluations and tree probes per evaluation.
	lastReevals int
	lastProbes  []int

	// maxEvalProbes / maxEvalN record the largest evaluation cost seen and
	// the child count at that moment, across creates and deletions.
	maxEvalProbes int
	maxEvalN      int
}

func newLockState() *lockState {
	return &lockState{
		watchers: map[int64][]*child{},
		byS:      map[int64]*child{},
	}
}

func (co *Coordinator) lock(name string) *lockState {
	l := co.locks[name]
	if l == nil {
		l = newLockState()
		co.locks[name] = l
	}
	return l
}

func removeWatcher(list []*child, c *child) []*child {
	for i, w := range list {
		if w == c {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

// registerWatch assigns a fresh global ws to waiter c and appends it, in ws
// order, to the watcher list of child target.
func (co *Coordinator) registerWatch(l *lockState, c, target *child) {
	c.watch = target.seq
	c.ws = co.ws + 1
	co.ws = c.ws
	list := l.watchers[target.seq]
	i := sort.Search(len(list), func(i int) bool { return list[i].ws >= c.ws })
	list = append(list, nil)
	copy(list[i+1:], list[i:])
	list[i] = c
	l.watchers[target.seq] = list
}

// evaluate applies the W/R hold-or-watch rule to one child. It must be
// called only when the child is not already holding. A hold consumes no ws.
func (co *Coordinator) evaluate(l *lockState, c *child) (held bool, probes int) {
	l.tree.probes = 0
	if c.kind == Write {
		prev := l.tree.predecessor(l.tree.root, c.seq)
		if prev == nil {
			c.held = true
			c.watch = -1
			c.ws = 0
			probes = l.tree.probes
			breakEval(l, probes)
			return true, probes
		}
		co.registerWatch(l, c, prev)
	} else {
		wseq := l.tree.maxWBelow(l.tree.root, c.seq)
		if wseq < 0 {
			c.held = true
			c.watch = -1
			c.ws = 0
			probes = l.tree.probes
			breakEval(l, probes)
			return true, probes
		}
		target := l.tree.find(l.tree.root, wseq)
		co.registerWatch(l, c, target)
	}
	probes = l.tree.probes
	breakEval(l, probes)
	return false, probes
}

func breakEval(l *lockState, probes int) {
	if probes > l.maxEvalProbes {
		l.maxEvalProbes = probes
		l.maxEvalN = l.tree.size()
	}
}

// deleteChild performs one deletion at the given already-claimed zxid and
// re-evaluates every waiter watching that node in ascending ws order. The
// re-evaluation list is snapshotted before the deletion so a waiter that
// re-registers onto an older node is not revisited in this round.
func (co *Coordinator) deleteChild(l *lockState, victim *child, zx int64, lockName string, events []Grant) []Grant {
	waiting := l.watchers[victim.seq]
	delete(l.watchers, victim.seq)

	l.tree.root = l.tree.remove(l.tree.root, victim.seq)
	delete(l.byS, victim.seq)

	l.lastReevals = len(waiting)
	l.lastProbes = make([]int, 0, len(waiting))
	for _, w := range waiting {
		if !co.sessions[w.session] {
			// Its session expired and watches were revoked first; kept as a
			// defensive guard and must never count as a notification.
			continue
		}
		held, probes := co.evaluate(l, w)
		l.lastProbes = append(l.lastProbes, probes)
		if held {
			events = append(events, Grant{
				Lock:     lockName,
				Seq:      w.seq,
				Kind:     w.kind,
				Session:  w.session,
				DeleteZX: zx,
			})
		}
	}
	return events
}

// Open creates a new live session; session ids start at 1.
func (co *Coordinator) Open() int64 {
	co.mu.Lock()
	defer co.mu.Unlock()
	co.sid++
	co.sessions[co.sid] = true
	return co.sid
}

// Create appends a sequential ephemeral child and evaluates it.
func (co *Coordinator) Create(sid int64, name string, kind Kind) (Result, error) {
	if name == "" || !kind.valid() {
		return Result{}, ErrInvalid
	}
	co.mu.Lock()
	defer co.mu.Unlock()

	alive, ok := co.sessions[sid]
	if !ok {
		return Result{}, ErrNoSession
	}
	if !alive {
		return Result{}, ErrExpired
	}
	l := co.lock(name)
	if l.tree.size() >= int(co.C) {
		return Result{}, ErrFull
	}

	c := &child{seq: l.cs, kind: kind, session: sid, watch: -1}
	l.cs++
	co.zx++
	c.zxid = co.zx
	l.tree.root = l.tree.insert(l.tree.root, c)
	l.byS[c.seq] = c

	res := Result{Seq: c.seq, ZXID: c.zxid, Watching: -1}
	held, _ := co.evaluate(l, c)
	res.Held = held
	if !held {
		res.Watching, res.WS = c.watch, c.ws
	}
	return res, nil
}

// Release deletes an owned child and re-evaluates its watchers.
func (co *Coordinator) Release(sid int64, name string, kind Kind, n int64) ([]Grant, error) {
	if name == "" || !kind.valid() || n < 0 {
		return nil, ErrInvalid
	}
	co.mu.Lock()
	defer co.mu.Unlock()

	alive, ok := co.sessions[sid]
	if !ok {
		return nil, ErrNoSession
	}
	if !alive {
		return nil, ErrExpired
	}
	l := co.locks[name]
	if l == nil {
		return nil, ErrNoNode
	}
	c := l.byS[n]
	if c == nil || c.kind != kind {
		return nil, ErrNoNode
	}
	if c.session != sid {
		return nil, ErrNotOwner
	}

	co.zx++
	zx := co.zx
	if !c.held {
		list := l.watchers[c.watch]
		l.watchers[c.watch] = removeWatcher(list, c)
	}
	events := co.deleteChild(l, c, zx, name, nil)
	return events, nil
}

// Expire marks a session expired and removes all of its children.
func (co *Coordinator) Expire(sid int64) ([]Grant, error) {
	co.mu.Lock()
	defer co.mu.Unlock()

	alive, ok := co.sessions[sid]
	if !ok {
		return nil, ErrNoSession
	}
	if !alive {
		return nil, ErrExpired
	}

	// 1) mark expired; 2) revoke this session's own watches. Revocation
	// happens before any deletion, so its waiting children never observe
	// (and are never granted by) the session's own deletions.
	co.sessions[sid] = false
	type own struct {
		name string
		l    *lockState
		c    *child
	}
	var victims []own
	for name, l := range co.locks {
		for seq, list := range l.watchers {
			kept := list[:0]
			for _, w := range list {
				if w.session == sid {
					w.watch = -1
					w.ws = 0
					continue
				}
				kept = append(kept, w)
			}
			if len(kept) == 0 {
				delete(l.watchers, seq)
			} else {
				l.watchers[seq] = kept
			}
		}
		// Collect the session's children; deletion order is creation-zxid
		// ascending, which equals global child-creation order.
		for _, c := range l.byS {
			if c.session == sid {
				victims = append(victims, own{name, l, c})
			}
		}
	}
	sort.Slice(victims, func(i, j int) bool {
		if victims[i].c.zxid != victims[j].c.zxid {
			return victims[i].c.zxid < victims[j].c.zxid
		}
		if victims[i].name != victims[j].name {
			return victims[i].name < victims[j].name
		}
		return victims[i].c.seq < victims[j].c.seq
	})

	var events []Grant
	for _, v := range victims {
		co.zx++
		events = co.deleteChild(v.l, v.c, co.zx, v.name, events)
	}
	return events, nil
}

// Holders returns currently holding children of a lock by ascending sequence.
func (co *Coordinator) Holders(name string) []Node {
	co.mu.Lock()
	defer co.mu.Unlock()
	l := co.locks[name]
	if l == nil {
		return nil
	}
	var out []Node
	for _, c := range l.tree.inorder(l.tree.root, nil) {
		if c.held {
			out = append(out, nodeView(l, c))
		}
	}
	return out
}

// Children returns all live children of a lock by ascending sequence.
func (co *Coordinator) Children(name string) []Node {
	co.mu.Lock()
	defer co.mu.Unlock()
	l := co.locks[name]
	if l == nil {
		return nil
	}
	all := l.tree.inorder(l.tree.root, nil)
	out := make([]Node, 0, len(all))
	for _, c := range all {
		out = append(out, nodeView(l, c))
	}
	return out
}

func nodeView(l *lockState, c *child) Node {
	return Node{
		Seq:     c.seq,
		Kind:    c.kind,
		Session: c.session,
		Held:    c.held,
		Watches: int64(len(l.watchers[c.seq])),
	}
}
