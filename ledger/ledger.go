package ledger

import (
	"errors"

	"ontology/tree"
)

var (
	ErrUnknownNode = errors.New("ledger: unknown node")
	ErrOverMax     = errors.New("ledger: allocation exceeds node Max")
)

// Task is one running task placed on a leaf.
type Task struct {
	ID    string
	Leaf  string
	Size  int64
	Start int64 // monotonic sequence; lower means started earlier
}

type nodeState struct {
	used  int64
	tasks []Task
}

// Ledger keeps committed occupancy. Parent used == sum(children used).
type Ledger struct {
	tr       *tree.Tree
	nodes    map[string]*nodeState
	byTask   map[string]Task
	borrower map[string]bool // leaves with used > Min
	seq      int64
}

func New(tr *tree.Tree) *Ledger {
	l := &Ledger{tr: tr, nodes: map[string]*nodeState{}, byTask: map[string]Task{}, borrower: map[string]bool{}}
	return l
}

func (l *Ledger) st(id string) *nodeState {
	s, ok := l.nodes[id]
	if !ok {
		s = &nodeState{}
		l.nodes[id] = s
	}
	return s
}

func (l *Ledger) Used(id string) int64 { return l.st(id).used }

func (l *Ledger) Task(id string) (Task, bool) {
	t, ok := l.byTask[id]
	return t, ok
}

// TasksOn returns the tasks committed on a leaf in start order.
func (l *Ledger) TasksOn(leaf string) []Task {
	return append([]Task(nil), l.st(leaf).tasks...)
}

// Borrowers returns leaves whose occupancy exceeds their own Min.
func (l *Ledger) Borrowers() []string {
	out := make([]string, 0, len(l.borrower))
	for id := range l.borrower {
		out = append(out, id)
	}
	return out
}

func (l *Ledger) IsBorrower(leaf string) bool { return l.borrower[leaf] }

// Borrowed is used-Min for a borrowing leaf.
func (l *Ledger) Borrowed(leaf string) int64 {
	n, ok := l.tr.Node(leaf)
	if !ok {
		return 0
	}
	b := l.st(leaf).used - n.Min
	if b < 0 {
		return 0
	}
	return b
}

// Place commits a task, climbing ancestors. It refuses Max violations.
func (l *Ledger) Place(leaf, taskID string, size int64) error {
	if _, ok := l.tr.Node(leaf); !ok || !l.tr.IsLeaf(leaf) {
		return ErrUnknownNode
	}
	if _, dup := l.byTask[taskID]; dup {
		return errors.New("ledger: duplicate task")
	}
	for id := leaf; id != ""; {
		n, _ := l.tr.Node(id)
		if l.st(id).used+size > n.Max {
			return ErrOverMax
		}
		id = n.Parent
	}
	l.seq++
	t := Task{ID: taskID, Leaf: leaf, Size: size, Start: l.seq}
	l.st(leaf).tasks = append(l.st(leaf).tasks, t)
	for id := leaf; id != ""; {
		l.st(id).used += size
		id = parent(l.tr, id)
	}
	l.byTask[taskID] = t
	l.refreshBorrower(leaf)
	return nil
}

// Remove drops a task from its leaf (normal finish or forced reclaim).
func (l *Ledger) Remove(taskID string) (Task, bool) {
	t, ok := l.byTask[taskID]
	if !ok {
		return Task{}, false
	}
	s := l.st(t.Leaf)
	for i, x := range s.tasks {
		if x.ID == taskID {
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
			break
		}
	}
	for id := t.Leaf; id != ""; {
		l.st(id).used -= t.Size
		id = parent(l.tr, id)
	}
	delete(l.byTask, taskID)
	l.refreshBorrower(t.Leaf)
	return t, true
}

func (l *Ledger) refreshBorrower(leaf string) {
	n, _ := l.tr.Node(leaf)
	if l.st(leaf).used > n.Min {
		l.borrower[leaf] = true
	} else {
		delete(l.borrower, leaf)
	}
}

func parent(tr *tree.Tree, id string) string {
	n, ok := tr.Node(id)
	if !ok {
		return ""
	}
	return n.Parent
}

// Check verifies parent-sum equality, Max bounds and borrower-index truth.
func (l *Ledger) Check() error {
	for _, kid := range l.tr.Leaves() {
		id := kid
		for {
			n, ok := l.tr.Node(id)
			if !ok {
				return ErrUnknownNode
			}
			if l.st(id).used > n.Max || l.st(id).used < 0 {
				return ErrOverMax
			}
			p := n.Parent
			if p == "" {
				break
			}
			var sum int64
			for _, c := range l.tr.Children(p) {
				sum += l.st(c).used
			}
			if sum != l.st(p).used {
				return errors.New("ledger: parent usage != sum(children)")
			}
			id = p
		}
	}
	for id := range l.borrower {
		n, _ := l.tr.Node(id)
		if l.st(id).used <= n.Min {
			return errors.New("ledger: stale borrower index")
		}
	}
	return nil
}

// TotalUsed is occupancy summed at the root.
func (l *Ledger) TotalUsed() int64 { return l.st(l.tr.Root()).used }
