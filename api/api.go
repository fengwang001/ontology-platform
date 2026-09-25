// Package api is the public spilling-transaction facade (depends on replay).
package api

import (
	"errors"
	"sort"
	"sync"

	"ontology/buf"
	"ontology/replay"
)

// The four rejection errors are distinct sentinels.
var ErrBadLimit = errors.New("api: memLimit must be positive")   // memLimit <= 0
var ErrEmptyKey = errors.New("api: key must not be empty")       // empty key
var ErrInvalidOp = errors.New("api: op must be Set or Del")      // bad op
var ErrClosed = errors.New("api: transaction already committed") // after Commit
type Op = buf.Op

const (
	Set = buf.Set
	Del = buf.Del
)

type KV = replay.KV
type Txn struct {
	mu        sync.RWMutex
	mem       *buf.Buffer
	disk      *replay.Store
	committed bool
	view      map[string]string
}

func New(memLimit int) (*Txn, error) {
	if memLimit <= 0 {
		return nil, ErrBadLimit
	}
	return &Txn{mem: buf.NewBuffer(memLimit), disk: replay.New()}, nil
}
func (t *Txn) Mutate(op Op, key, val string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.committed:
		return ErrClosed
	case op != Set && op != Del:
		return ErrInvalidOp
	case key == "":
		return ErrEmptyKey
	}
	if blk := t.mem.Append(buf.Change{Op: op, Key: key, Val: val}); blk != nil {
		t.disk.Spill(blk)
	}
	return nil
}

func (t *Txn) Commit() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.committed {
		return
	}
	t.view = map[string]string{}
	for _, p := range t.disk.Replay(t.mem.Pending()) {
		t.view[p.Key] = p.Val
	}
	t.committed = true
}

func (t *Txn) View() []KV {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]KV, 0, len(t.view))
	for k, v := range t.view {
		out = append(out, KV{Key: k, Val: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
func (t *Txn) Get(key string) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	v, ok := t.view[key]
	return v, ok
}
func (t *Txn) Spilled() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.disk.Blocks()
}

func (t *Txn) SelfCheck() error {
	tx, _ := New(3)
	ops := []Op{Set, Set, Set, Del, Set, Set, Del, Set}
	keys := []string{"a", "b", "c", "b", "d", "a", "c", "e"}
	vals := []string{"1", "2", "3", "", "4", "9", "", "5"}
	ref := map[string]string{} // naive per-change reference
	for i := range ops {
		if ops[i] == Del {
			delete(ref, keys[i])
		} else {
			ref[keys[i]] = vals[i]
		}
		if err := tx.Mutate(ops[i], keys[i], vals[i]); err != nil {
			return err
		}
	}
	tx.Commit()
	got := map[string]string{}
	for _, p := range tx.View() {
		got[p.Key] = p.Val
	}
	if tx.Spilled() != 2 || len(got) != len(ref) { // invariants 1-3
		return errors.New("selfcheck: spill count or view size wrong")
	}
	for k, v := range ref { // invariant 1: equal to the naive map
		if got[k] != v {
			return errors.New("selfcheck: view differs from naive reference")
		}
	}
	for _, k := range []string{"b", "c"} { // invariant 2: Del never lost
		if _, ok := got[k]; ok {
			return errors.New("selfcheck: deleted key survived")
		}
	}
	if _, err := New(0); !errors.Is(err, ErrBadLimit) { // invariant 4: bad limit
		return errors.New("selfcheck: ErrBadLimit not signalled")
	}
	live, _ := New(3) // live txn: invalid op and empty key reject, leave no trace
	live.Mutate(Set, "x", "1")
	lb := live.Spilled()
	if !errors.Is(live.Mutate(Op(99), "z", "1"), ErrInvalidOp) ||
		!errors.Is(live.Mutate(Set, "", "1"), ErrEmptyKey) {
		return errors.New("selfcheck: rejection error mismatch")
	}
	if live.Spilled() != lb || live.Mutate(Set, "y", "2") != nil {
		return errors.New("selfcheck: rejected Mutate left a trace")
	}
	n := tx.Spilled() // committed txn: Mutate is ErrClosed and leaves no trace
	if err := tx.Mutate(Set, "z", "1"); !errors.Is(err, ErrClosed) {
		return errors.New("selfcheck: ErrClosed not signalled")
	}
	if tx.Spilled() != n || len(tx.View()) != len(got) {
		return errors.New("selfcheck: post-commit Mutate left a trace")
	}
	if _, ok := tx.Get("a"); !ok {
		return errors.New("selfcheck: txn unusable after rejection")
	}
	return nil
}
