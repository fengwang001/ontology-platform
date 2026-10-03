package ontology

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrUnknownRep = errors.New("unknown replica")
	ErrStale      = errors.New("operation is older than stable watermark")
	ErrDuplicate  = errors.New("duplicate operation")
	ErrAckRegress = errors.New("ack watermark regressed")
)

type Op struct {
	TS     int64
	Rep    string
	Node   int64
	Parent int64
	Name   string
}

type Key struct {
	TS  int64
	Rep string
}

type LogEntry struct {
	Key       Key
	Effective bool
}

type ApplyResult struct {
	Effective bool
	Changed   []Key
}

type nodeState struct {
	parent int64
	name   string
}

type treeState struct {
	nodes map[int64]nodeState
}

type Replayer struct {
	mu          sync.RWMutex
	replicas    map[string]struct{}
	acks        map[string]int64
	stable      int64
	base        treeState
	log         []logItem
	redone      int64
	parentSteps int64
}

type logItem struct {
	op        Op
	effective bool
}

func New(replicas []string) *Replayer {
	if len(replicas) == 0 {
		panic("replicas must not be empty")
	}
	replicaSet := make(map[string]struct{}, len(replicas))
	acks := make(map[string]int64, len(replicas))
	for _, replica := range replicas {
		if replica == "" {
			panic("replica name must not be empty")
		}
		if _, exists := replicaSet[replica]; exists {
			panic("replica names must be unique")
		}
		replicaSet[replica] = struct{}{}
		acks[replica] = 0
	}
	return &Replayer{
		replicas: replicaSet,
		acks:     acks,
		base:     newTree(),
	}
}

func (r *Replayer) Apply(op Op) (ApplyResult, error) {
	if err := validateOp(op); err != nil {
		return ApplyResult{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.replicas[op.Rep]; !ok {
		return ApplyResult{}, ErrUnknownRep
	}
	if op.TS <= r.stable {
		return ApplyResult{}, ErrStale
	}

	key := keyOf(op)
	insertAt := sort.Search(len(r.log), func(i int) bool {
		return compareKey(keyOf(r.log[i].op), key) >= 0
	})
	if insertAt < len(r.log) && compareKey(keyOf(r.log[insertAt].op), key) == 0 {
		return ApplyResult{}, ErrDuplicate
	}

	oldEffective := make(map[Key]bool, len(r.log)-insertAt)
	for _, item := range r.log[insertAt:] {
		oldEffective[keyOf(item.op)] = item.effective
	}
	r.redone += int64(len(r.log) - insertAt)

	replayTree := cloneTree(r.base)
	for i := 0; i < insertAt; i++ {
		applyToTree(replayTree, r.log[i].op, &r.parentSteps)
	}

	newLog := make([]logItem, 0, len(r.log)+1)
	newLog = append(newLog, r.log[:insertAt]...)
	newLog = append(newLog, logItem{op: op})
	newLog = append(newLog, r.log[insertAt:]...)

	result := ApplyResult{Changed: []Key{}}
	for i := insertAt; i < len(newLog); i++ {
		item := &newLog[i]
		effective := applyToTree(replayTree, item.op, &r.parentSteps)
		item.effective = effective
		itemKey := keyOf(item.op)
		wasEffective, existed := oldEffective[itemKey]
		if existed && wasEffective != effective {
			result.Changed = append(result.Changed, itemKey)
		}
	}

	r.log = newLog
	result.Effective = newLog[insertAt].effective
	return result, nil
}

func (r *Replayer) Ack(rep string, t int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.replicas[rep]; !ok {
		return 0, ErrUnknownRep
	}
	if t < r.acks[rep] {
		return 0, ErrAckRegress
	}

	r.acks[rep] = t
	nextStable := int64(-1)
	for replica := range r.replicas {
		ack := r.acks[replica]
		if nextStable < 0 || ack < nextStable {
			nextStable = ack
		}
	}
	if nextStable <= r.stable {
		return 0, nil
	}

	foldCount := 0
	for foldCount < len(r.log) && r.log[foldCount].op.TS <= nextStable {
		applyToTree(r.base, r.log[foldCount].op, &r.parentSteps)
		foldCount++
	}
	r.log = append([]logItem(nil), r.log[foldCount:]...)
	r.stable = nextStable
	return foldCount, nil
}

func (r *Replayer) Parent(node int64) (int64, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	current := r.currentTree()
	state, ok := current.nodes[node]
	if !ok {
		return 0, "", false
	}
	if node == 0 {
		return 0, "", true
	}
	if node == 1 {
		return 1, "", true
	}
	return state.parent, state.name, true
}

func (r *Replayer) InTrash(node int64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	current := r.currentTree()
	if node == 0 {
		return false
	}
	state, ok := current.nodes[node]
	if !ok {
		return false
	}
	parent := state.parent
	for {
		if parent == 1 {
			return true
		}
		if parent == 0 {
			return false
		}
		state, exists := current.nodes[parent]
		if !exists {
			return false
		}
		parent = state.parent
	}
}

func (r *Replayer) Log() []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := make([]LogEntry, len(r.log))
	for i, item := range r.log {
		entries[i] = LogEntry{Key: keyOf(item.op), Effective: item.effective}
	}
	return entries
}

func (r *Replayer) Stable() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.stable
}

func newTree() treeState {
	return treeState{nodes: map[int64]nodeState{
		0: {parent: 0},
		1: {parent: 1},
	}}
}

func cloneTree(source treeState) treeState {
	nodes := make(map[int64]nodeState, len(source.nodes))
	for node, state := range source.nodes {
		nodes[node] = state
	}
	return treeState{nodes: nodes}
}

func applyToTree(tree treeState, op Op, parentSteps *int64) bool {
	parentState, parentExists := tree.nodes[op.Parent]
	if !parentExists {
		return false
	}

	current := op.Parent
	var steps int64
	for {
		if steps == int64(len(tree.nodes)) {
			panic("parent chain traversal exceeded tree size")
		}
		steps++
		if parentSteps != nil {
			*parentSteps++
		}
		if current == op.Node {
			return false
		}
		if current == 0 || current == 1 {
			break
		}
		current = parentState.parent
		parentState, parentExists = tree.nodes[current]
		if !parentExists {
			return false
		}
	}

	tree.nodes[op.Node] = nodeState{parent: op.Parent, name: op.Name}
	return true
}

func (r *Replayer) currentTree() treeState {
	tree := cloneTree(r.base)
	for _, item := range r.log {
		applyToTree(tree, item.op, nil)
	}
	return tree
}

func validateOp(op Op) error {
	if op.TS < 1 || op.Node < 2 || op.Parent < 0 || op.Name == "" || containsSlash(op.Name) {
		return errors.New("invalid operation")
	}
	return nil
}

func containsSlash(value string) bool {
	for _, r := range value {
		if r == '/' {
			return true
		}
	}
	return false
}

func keyOf(op Op) Key {
	return Key{TS: op.TS, Rep: op.Rep}
}

func compareKey(left, right Key) int {
	if left.TS < right.TS {
		return -1
	}
	if left.TS > right.TS {
		return 1
	}
	if left.Rep < right.Rep {
		return -1
	}
	if left.Rep > right.Rep {
		return 1
	}
	return 0
}
