package ontology

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

type Op struct {
	TS     int64
	Rep    string
	Node   int
	Parent int
	Name   string
}

type Key struct {
	TS  int64
	Rep string
}

type LogEntry struct {
	Key       Key
	Op        Op
	Effective bool
}

type ApplyResult struct {
	Effective bool
	Changed   []Key
}

type Replayer struct {
	mu sync.RWMutex

	replicas map[string]struct{}
	acks     map[string]int64
	stable   int64
	log      []logEntry

	parent map[int]int
	name   map[int]string

	baselineParent map[int]int
	baselineName   map[int]string

	redone      int
	parentSteps int
}

type logEntry struct {
	op        Op
	effective bool
}

var (
	ErrUnknownRep = errors.New("unknown replica")
	ErrStale      = errors.New("operation is older than stable watermark")
	ErrDuplicate  = errors.New("duplicate operation")
	ErrAckRegress = errors.New("ack watermark cannot regress")
)

func NewReplayer(replicas []string) (*Replayer, error) {
	if len(replicas) == 0 {
		return nil, errors.New("replica set must not be empty")
	}

	replicaSet := make(map[string]struct{}, len(replicas))
	acks := make(map[string]int64, len(replicas))
	for _, replica := range replicas {
		if replica == "" {
			return nil, errors.New("replica name must not be empty")
		}
		if _, exists := replicaSet[replica]; exists {
			return nil, errors.New("replica names must be distinct")
		}
		replicaSet[replica] = struct{}{}
		acks[replica] = 0
	}

	baselineParent := map[int]int{0: 0, 1: 1}
	baselineName := map[int]string{}

	return &Replayer{
		replicas:       replicaSet,
		acks:           acks,
		parent:         cloneParentMap(baselineParent),
		name:           cloneNameMap(baselineName),
		baselineParent: baselineParent,
		baselineName:   baselineName,
	}, nil
}

func (r *Replayer) Apply(op Op) (ApplyResult, error) {
	if !validOp(op) {
		return ApplyResult{}, errors.New("invalid operation")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.replicas[op.Rep]; !exists {
		return ApplyResult{}, ErrUnknownRep
	}
	if op.TS <= r.stable {
		return ApplyResult{}, ErrStale
	}

	key := keyOf(op)
	pos, exists := r.findLogPos(key)
	if exists {
		return ApplyResult{}, ErrDuplicate
	}

	oldEffective := make(map[Key]bool, len(r.log)-pos)
	for _, entry := range r.log[pos:] {
		oldEffective[keyOf(entry.op)] = entry.effective
	}
	redoneCount := len(r.log) - pos

	r.resetToBaseline()
	r.replayRange(0, pos)
	r.log = append(r.log, logEntry{})
	copy(r.log[pos+1:], r.log[pos:])
	r.log[pos] = logEntry{op: op}
	r.replayFrom(pos)

	changed := make([]Key, 0)
	newEntryEffective := false
	for idx, entry := range r.log[pos:] {
		entryKey := keyOf(entry.op)
		if idx == 0 {
			newEntryEffective = entry.effective
			continue
		}
		if oldEffective[entryKey] != entry.effective {
			changed = append(changed, entryKey)
		}
	}
	sort.Slice(changed, func(i, j int) bool { return lessKey(changed[i], changed[j]) })

	r.redone += redoneCount

	return ApplyResult{Effective: newEntryEffective, Changed: changed}, nil
}

func (r *Replayer) Ack(rep string, t int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.replicas[rep]; !exists {
		return 0, ErrUnknownRep
	}
	if t < r.acks[rep] {
		return 0, ErrAckRegress
	}

	r.acks[rep] = t
	newStable := int64(-1)
	for _, ack := range r.acks {
		if newStable < 0 || ack < newStable {
			newStable = ack
		}
	}
	if newStable <= r.stable {
		return 0, nil
	}

	foldEnd := 0
	for foldEnd < len(r.log) && r.log[foldEnd].op.TS <= newStable {
		foldEnd++
	}

	r.resetToBaseline()
	for _, entry := range r.log[:foldEnd] {
		r.applyEntry(entry.op)
	}
	r.baselineParent = cloneParentMap(r.parent)
	r.baselineName = cloneNameMap(r.name)

	r.log = append([]logEntry(nil), r.log[foldEnd:]...)
	r.stable = newStable
	r.replayFrom(0)

	return foldEnd, nil
}

func (r *Replayer) Parent(node int) (parent int, name string, exists bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	parent, exists = r.parent[node]
	if node == 0 || node == 1 {
		return parent, "", true
	}
	return parent, r.name[node], exists
}

func (r *Replayer) InTrash(node int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if _, exists := r.parent[node]; !exists {
		return false
	}

	current := node
	for {
		if current == 1 {
			return true
		}
		if current == 0 {
			return false
		}
		next, exists := r.parent[current]
		if !exists {
			return false
		}
		current = next
	}
}

func (r *Replayer) Log() []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := make([]LogEntry, len(r.log))
	for i, entry := range r.log {
		entries[i] = LogEntry{Key: keyOf(entry.op), Op: entry.op, Effective: entry.effective}
	}
	return entries
}

func (r *Replayer) Stable() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.stable
}

func (r *Replayer) findLogPos(key Key) (int, bool) {
	pos := sort.Search(len(r.log), func(i int) bool {
		return !lessKey(keyOf(r.log[i].op), key)
	})
	if pos < len(r.log) && keyOf(r.log[pos].op) == key {
		return pos, true
	}
	return pos, false
}

func (r *Replayer) replayFrom(start int) {
	r.replayRange(start, len(r.log))
}

func (r *Replayer) replayRange(start, end int) {
	for i := start; i < end; i++ {
		r.log[i].effective = r.applyEntry(r.log[i].op)
	}
}

func (r *Replayer) applyEntry(op Op) bool {
	if _, exists := r.parent[op.Parent]; !exists {
		return false
	}

	steps := 0
	current := op.Parent
	nodeCount := len(r.parent)
	for {
		steps++
		if steps > nodeCount {
			panic("parent chain traversal exceeded current tree size")
		}
		if current == op.Node {
			r.parentSteps += steps
			return false
		}
		if current == 0 || current == 1 {
			break
		}
		next, exists := r.parent[current]
		if !exists {
			break
		}
		current = next
	}
	r.parentSteps += steps

	r.parent[op.Node] = op.Parent
	r.name[op.Node] = op.Name
	return true
}

func (r *Replayer) resetToBaseline() {
	r.parent = cloneParentMap(r.baselineParent)
	r.name = cloneNameMap(r.baselineName)
}

func validOp(op Op) bool {
	return op.TS >= 1 &&
		op.Node >= 2 &&
		op.Parent >= 0 &&
		op.Name != "" &&
		!strings.Contains(op.Name, "/")
}

func keyOf(op Op) Key {
	return Key{TS: op.TS, Rep: op.Rep}
}

func lessKey(a, b Key) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	return a.Rep < b.Rep
}

func cloneParentMap(source map[int]int) map[int]int {
	result := make(map[int]int, len(source))
	for node, parent := range source {
		result[node] = parent
	}
	return result
}

func cloneNameMap(source map[int]string) map[int]string {
	result := make(map[int]string, len(source))
	for node, name := range source {
		result[node] = name
	}
	return result
}
