// Package norm maintains the current key/value table and folds each upstream
// batch into a retract-style changelog: only each touched key's before/after
// states matter, and a batch either commits wholly or (on rejection) leaves no
// trace. It depends only on rfold.
package norm

import (
	"errors"
	"ontology/rfold"
	"sync"
)

// ErrTooManyKeys is returned when the live key count after a whole batch
// would exceed maxKeys (intermediate states inside the batch do not count).
var ErrTooManyKeys = errors.New("norm: live key count after batch exceeds maxKeys")

type cell struct {
	val int64
	ok  bool
}

// Normalizer is the in-memory folded table plus changelog. Use New.
type Normalizer struct {
	mu      sync.Mutex
	maxKeys int
	cur     map[string]int64
	log     []rfold.Change
	// lastChecked is an unexported complexity probe: keys inspected while
	// producing the most recent Apply output. Unreachable from exported API.
	lastChecked int
}

// New creates a Normalizer that rejects any committed batch whose live key
// count exceeds maxKeys.
func New(maxKeys int) *Normalizer {
	return &Normalizer{maxKeys: maxKeys, cur: map[string]int64{}}
}

// Apply folds one batch atomically; every rejection leaves cur and log untouched.
func (n *Normalizer) Apply(batch []rfold.Op) ([]rfold.Change, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	order := make([]string, 0, len(batch)) // first-appearance order
	before := make(map[string]cell, len(batch))
	after := make(map[string]cell, len(batch))
	liveTouched := 0
	for _, op := range batch {
		if err := rfold.Validate(op); err != nil {
			return nil, err
		}
		st, seen := after[op.Key]
		if !seen {
			v, bok := n.cur[op.Key]
			before[op.Key], st = cell{v, bok}, cell{v, bok}
			order = append(order, op.Key)
			if bok {
				liveTouched++
			}
		}
		if op.Kind == rfold.OpUpsert {
			st = cell{op.Val, true}
		} else {
			st = cell{0, false}
		}
		after[op.Key] = st
	}
	liveAfterTouched := 0
	for _, k := range order {
		if after[k].ok {
			liveAfterTouched++
		}
	}
	if len(n.cur)-liveTouched+liveAfterTouched > n.maxKeys {
		return nil, ErrTooManyKeys
	}

	out := make([]rfold.Change, 0, len(order)*2)
	for _, k := range order { // only touched keys are inspected -> O(touched)
		b, a := before[k], after[k]
		out = append(out, rfold.Diff(k, b.val, b.ok, a.val, a.ok)...)
	}
	n.lastChecked = len(order)
	for _, k := range order {
		if a := after[k]; a.ok {
			n.cur[k] = a.val
		} else {
			delete(n.cur, k)
		}
	}
	n.log = append(n.log, out...)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// Log returns a copy of every change emitted so far, in emission order.
func (n *Normalizer) Log() []rfold.Change {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]rfold.Change(nil), n.log...)
}

// Snapshot returns a copy of the current live table.
func (n *Normalizer) Snapshot() map[string]int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := make(map[string]int64, len(n.cur))
	for k, v := range n.cur {
		s[k] = v
	}
	return s
}

// lastCheckedOf is package-internal so white-box tests can read the counter.
func lastCheckedOf(n *Normalizer) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastChecked
}

// naiveApply replays accepted ops directly, the reference semantics.
func naiveApply(t map[string]int64, b []rfold.Op) {
	for _, o := range b {
		if o.Kind == rfold.OpUpsert {
			t[o.Key] = o.Val
		} else {
			delete(t, o.Key)
		}
	}
}

// replayChk applies a changelog from the empty table and reports whether
// every prefix was applicable: each - must hit the exact current value and
// each + must hit a free key.
func replayChk(lg []rfold.Change) (map[string]int64, bool) {
	t := map[string]int64{}
	for _, c := range lg {
		v, ok := t[c.Key]
		if (c.Kind == rfold.ChgRetract && (!ok || v != c.Val)) ||
			(c.Kind == rfold.ChgInsert && ok) {
			return t, false
		}
		delete(t, c.Key)
		if c.Kind == rfold.ChgInsert {
			t[c.Key] = c.Val
		}
	}
	return t, true
}
