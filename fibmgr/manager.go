package fibmgr

import "sync"

// Manager keeps the control plane routes and the aggregated data plane.
// All methods are safe for concurrent use; concurrent calls behave as if
// executed in some serial order, and reads always observe both planes as
// of the same completed update.
type Manager struct {
	mu       sync.RWMutex
	capacity int

	routes map[Prefix]NextHop // control plane
	root   *node              // trie backing both planes

	intern map[string]uint32 // nexthop string -> color id
	names  []string          // color id - 2 -> nexthop string

	// transaction scratch (valid only while a write is in flight)
	undo           *undoLog
	batchTop       *node
	batchTopParent *node
	batchTopDepth  int
}

// New creates a manager whose data plane may hold at most capacity
// entries. It panics on a negative capacity.
func New(capacity int) *Manager {
	if capacity < 0 {
		panic("fibmgr: negative capacity")
	}
	return &Manager{
		capacity: capacity,
		routes:   make(map[Prefix]NextHop),
		intern:   make(map[string]uint32),
	}
}

// Capacity returns the current capacity limit.
func (m *Manager) Capacity() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.capacity
}

// Write installs or overwrites one route.
func (m *Manager) Write(p Prefix, nh NextHop) error {
	return m.Update(Write(p, nh))
}

// Withdraw removes one route; it fails with ErrNotFound if the prefix
// has no route.
func (m *Manager) Withdraw(p Prefix) error {
	return m.Update(Withdraw(p))
}

// Update applies a batch of writes and withdraws in order. The batch is
// all-or-nothing: on any error no state changes.
func (m *Manager) Update(ops ...Op) error {
	// Phase 1: parameter validation (highest priority).
	for _, op := range ops {
		if !op.valid() {
			return ErrInvalidArgument
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.beginBatch()
	for _, op := range ops {
		var err error
		switch op.Kind {
		case OpWrite:
			err = m.applyWrite(op.Prefix, op.Nexthop)
		case OpWithdraw:
			err = m.applyWithdraw(op.Prefix)
		}
		if err != nil {
			m.rollback()
			return err
		}
	}

	// Phase 3: capacity check (lowest priority).
	if m.count() > m.capacity {
		m.rollback()
		return ErrCapacityExceeded
	}

	// Commit: repair data plane entries top-down from the shallowest
	// changed node.
	if m.batchTop != nil {
		if m.batchTopParent != nil {
			m.fixup(m.batchTopParent, m.batchTopParent.inh)
		} else {
			m.fixup(m.root, colorNone)
		}
	}
	m.undo = nil
	return nil
}

// Lookup returns the query outcome of one address on the control plane
// and on the data plane, observed atomically.
func (m *Manager) Lookup(addr uint32) (control, data PlaneResult) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lookupControl(addr), m.lookupData(addr)
}

// EntryCount returns the current number of data plane entries, which is
// the minimum achievable for the current control plane.
func (m *Manager) EntryCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.count()
}

// Entries lists all data plane entries ordered by start address, then by
// prefix length for equal start addresses.
func (m *Manager) Entries() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Entry
	m.collectEntries(&out)
	return out
}

// SetCapacity changes the capacity limit. If the new limit is below the
// current minimum entry count it fails with ErrCapacityExceeded and
// nothing changes. Adjusting the limit never changes any route.
func (m *Manager) SetCapacity(c int) error {
	if c < 0 {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c < m.count() {
		return ErrCapacityExceeded
	}
	m.capacity = c
	return nil
}

// undoLog captures enough pre-mutation state to roll a batch back.
type undoLog struct {
	nodes   map[*node]node
	routes  map[Prefix]routeUndo
	oldRoot *node
}

type routeUndo struct {
	old     NextHop
	existed bool
}

func (m *Manager) beginBatch() {
	m.undo = &undoLog{
		nodes:   make(map[*node]node),
		routes:  make(map[Prefix]routeUndo),
		oldRoot: m.root,
	}
	m.batchTop = nil
	m.batchTopParent = nil
	m.batchTopDepth = 0
}

func (u *undoLog) saveNode(v *node) {
	if _, ok := u.nodes[v]; !ok {
		u.nodes[v] = *v
	}
}

func (m *Manager) saveRoute(p Prefix) {
	if _, ok := m.undo.routes[p]; !ok {
		old, existed := m.routes[p]
		m.undo.routes[p] = routeUndo{old: old, existed: existed}
	}
}

func (m *Manager) rollback() {
	for v, snap := range m.undo.nodes {
		*v = snap
	}
	for p, ru := range m.undo.routes {
		if ru.existed {
			m.routes[p] = ru.old
		} else {
			delete(m.routes, p)
		}
	}
	m.root = m.undo.oldRoot
	m.undo = nil
	m.batchTop = nil
	m.batchTopParent = nil
}

// walk descends from the root to the node for p, creating missing nodes
// and refreshing the effective control color of every node on the path.
// It returns the path (stack[i] has depth i, the last element is the
// parent of the target), the target node, and the control color
// inherited by the target from routes above it.
func (m *Manager) walk(p Prefix) (stack []*node, target *node, inh uint32) {
	if m.root == nil {
		m.root = &node{}
	}
	v := m.root
	inh = colorNone
	for d := 0; d < p.Len; d++ {
		m.setEffective(v, inh)
		inh = v.e
		stack = append(stack, v)
		b := bit(p.Addr, d)
		if v.child[b] == nil {
			m.undo.saveNode(v)
			v.child[b] = &node{}
		}
		v = v.child[b]
	}
	return stack, v, inh
}

func (m *Manager) setEffective(v *node, inh uint32) {
	e := inh
	if v.hasRoute {
		e = m.colorOf(v.route)
	}
	if v.e != e {
		m.undo.saveNode(v)
		v.e = e
	}
}

func (m *Manager) applyWrite(p Prefix, nh NextHop) error {
	if old, ok := m.routes[p]; ok && old == nh {
		return nil // identical write: a successful no-op
	}
	stack, target, inh := m.walk(p)
	m.saveRoute(p)
	m.routes[p] = nh
	m.undo.saveNode(target)
	target.hasRoute = true
	target.route = nh
	m.recomputeSubtree(target, parentOf(stack), inh, p.Len)
	m.recomputeAncestors(stack)
	return nil
}

func (m *Manager) applyWithdraw(p Prefix) error {
	if _, ok := m.routes[p]; !ok {
		return ErrNotFound
	}
	stack, target, inh := m.walk(p)
	m.saveRoute(p)
	delete(m.routes, p)
	m.undo.saveNode(target)
	target.hasRoute = false
	target.route = NextHop{}
	m.recomputeSubtree(target, parentOf(stack), inh, p.Len)
	m.recomputeAncestors(stack)
	return nil
}

func parentOf(stack []*node) *node {
	if len(stack) == 0 {
		return nil
	}
	return stack[len(stack)-1]
}

type errInvalid string

func (e errInvalid) Error() string { return string(e) }
