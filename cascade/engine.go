package cascade

import (
	"sort"
	"sync"
)

// Result is the committed outcome of one deletion request.
type Result struct {
	Deleted []string
	Nulled  []string
	Plan    *Plan
}

// Entry is one structured audit record.
type Entry struct {
	Root    string
	Deleted []string
	Steps   []Step
	Reject  *CascadeError
}

// Engine serializes mutating operations against one store.
type Engine struct {
	mu    sync.Mutex
	store *MemoryStore
	cfg   *Config
	log   []Entry
	seq   []seqOp
}

// seqOp records the actual global order of serialized mutations. It exists
// so tests can replay the observed order against a fresh engine and check
// serializability; it carries no production decision logic.
type seqOp struct {
	kind   string // "delete" or "add"
	id     string
	link   Link
	commit bool
}

func NewEngine(store *MemoryStore, cfg *Config) *Engine {
	return &Engine{store: store, cfg: cfg}
}

// Delete executes the full cascade and orphan cleanup as one atomic unit.
//
// Serializability: e.mu is held across snapshot, plan and commit, so the
// request is one critical section. A concurrent AddLink is wholly before
// or wholly after it; keep-alive checks never observe a half-committed
// link. The committed history is therefore equivalent to some global serial
// order of the operations.
//
// Atomicity without rollback: planning mutates only local sets on an
// immutable snapshot. On restrict/undefined errors the store is never
// touched; on success a single commit applies the whole plan.
func (e *Engine) Delete(root string) (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	snap := e.store.snapshot()
	p, err := plan(e.cfg, snap, root)
	if err != nil {
		var steps []Step
		if p != nil {
			steps = p.Steps
		}
		e.log = append(e.log, Entry{Root: root, Steps: steps, Reject: err})
		e.seq = append(e.seq, seqOp{kind: "delete", id: root, commit: false})
		return nil, err
	}
	e.store.commit(p)

	nulled := []string{}
	for _, l := range snap.links {
		if p.RemovedLinks[l.ID] && !p.Deleted[l.Src] && !p.Deleted[l.Dst] {
			nulled = append(nulled, l.ID)
		}
	}
	sort.Strings(nulled)
	e.log = append(e.log, Entry{Root: root, Deleted: sortedSet(p.Deleted), Steps: p.Steps})
	e.seq = append(e.seq, seqOp{kind: "delete", id: root, commit: true})
	return &Result{Deleted: sortedSet(p.Deleted), Nulled: nulled, Plan: p}, nil
}

// AddLink creates endpoints implicitly and is serialized against deletions.
func (e *Engine) AddLink(l Link) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.cfg.types[l.Type]; !ok {
		e.seq = append(e.seq, seqOp{kind: "add", link: l, commit: false})
		return &CascadeError{Code: ErrUndefinedLinkType, Detail: l.Type}
	}
	err := e.store.AddLink(l)
	e.seq = append(e.seq, seqOp{kind: "add", link: l, commit: err == nil})
	return err
}

// Log returns a defensive copy of the structured audit log.
func (e *Engine) Log() []Entry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Entry, len(e.log))
	copy(out, e.log)
	return out
}
