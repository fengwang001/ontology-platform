package layout

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// ModeKind selects whether a dimension is fixed or derived from content.
type ModeKind int

const (
	ModeFixed ModeKind = iota + 1
	ModeContent
)

// Mode is one dimension's sizing rule. For ModeFixed, Value is used.
type Mode struct {
	Kind  ModeKind
	Value int
}

// Size is a resolved, non-negative width/height pair.
type Size struct{ W, H int }

// node is the internal mutable box-tree node.
type node struct {
	id        int64
	width     Mode
	height    Mode
	pad       int // uniform padding applied on both sides of each axis
	isolated  bool
	parent    *node
	children  []*node // ordered
	selfDirty bool
	subDirty  bool
	size      Size // last committed size
}

// Tree is an ordered box tree with dirty-marker based invalidation.
type Tree struct {
	mu         recMutex
	nodes      map[int64]*node
	root       *node
	bounds     map[*node]struct{} // non-root dirty boundaries pending reflow
	commits    int
	committing bool
	reflowN    int // number of nodes recomputed in the last commit (for perf checks)
	log        Logger
	onCommit   func() // test hook: invoked while a commit is running
}

// NewTree creates a tree whose root has the given id, content sizing and
// zero padding. The root is always a layout boundary.
func NewTree(rootID int64) *Tree {
	if rootID <= 0 {
		return nil
	}
	r := &node{
		id:     rootID,
		width:  Mode{Kind: ModeContent},
		height: Mode{Kind: ModeContent},
	}
	t := &Tree{
		nodes:  map[int64]*node{},
		bounds: map[*node]struct{}{},
		log:    StdLogger{},
	}
	t.nodes[rootID] = r
	t.root = r
	t.relayoutFrom(r)
	return t
}

// relayoutFrom recomputes sizes of the subtree rooted at n bottom-up. It is
// used only for fresh initialization, where every node is "new".
func (t *Tree) relayoutFrom(n *node) {
	for _, c := range n.children {
		t.relayoutFrom(c)
	}
	n.size = computeSize(n)
}

// computeSize resolves a node's size from its modes, padding and children.
func computeSize(n *node) Size {
	measure := func(m Mode, along func(*node) int) int {
		if m.Kind == ModeFixed {
			return m.Value
		}
		sum := 2 * n.pad
		for _, c := range n.children {
			sum += along(c)
		}
		return sum
	}
	return Size{
		W: measure(n.width, func(c *node) int { return c.size.W }),
		H: measure(n.height, func(c *node) int { return c.size.H }),
	}
}

func (t *Tree) mustNode(id int64) (*node, error) {
	if n, ok := t.nodes[id]; ok {
		return n, nil
	}
	return nil, &LayoutError{Kind: KindNodeNotFound, Msg: "node not found: " + strconv.FormatInt(id, 10)}
}

func validateMode(m Mode) error {
	switch m.Kind {
	case ModeFixed:
		if m.Value < 0 {
			return &LayoutError{Kind: KindInvalidArgument, Msg: "negative fixed size"}
		}
	case ModeContent:
		if m.Value != 0 {
			return &LayoutError{Kind: KindInvalidArgument, Msg: "content mode must carry zero value"}
		}
	default:
		return &LayoutError{Kind: KindInvalidArgument, Msg: "unknown sizing mode"}
	}
	return nil
}

// recMutex is a mutex that is reentrant for the same goroutine. Reflow calls
// user-supplied measurement callbacks while holding it; such callbacks may
// safely inspect the tree. Nested commit() from the same goroutine is still
// rejected at the API layer rather than deadlocking.
type recMutex struct {
	sync.Mutex
	holder uint64
	depth  int
	cond   *sync.Cond
}

func (m *recMutex) Lock() {
	gid := goroutineID()
	if m.cond == nil {
		m.cond = sync.NewCond(&m.Mutex)
	}
	m.Mutex.Lock()
	for m.depth > 0 && m.holder != gid {
		m.cond.Wait()
	}
	if m.depth == 0 {
		m.holder = gid
	}
	m.depth++
	m.Mutex.Unlock()
}

func (m *recMutex) Unlock() {
	m.Mutex.Lock()
	m.depth--
	if m.depth == 0 {
		m.holder = 0
		m.cond.Broadcast()
	}
	m.Mutex.Unlock()
}

func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	s := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	idx := strings.IndexByte(s, ' ')
	if idx < 0 {
		return 0
	}
	id, _ := strconv.ParseUint(s[:idx], 10, 64)
	return id
}
