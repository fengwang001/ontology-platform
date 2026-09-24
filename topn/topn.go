// Package topn maintains live rows and the materialised Top-N boundary.
package topn

import (
	"errors"
	"sync"

	"ontology/rank"
)

// Change is one changelog entry: Entering true is +, false is -.
type Change struct {
	Key      string
	Score    int64
	Entering bool
}

// Op is one upstream change: Add true is +, false is -.
type Op struct {
	Key   string
	Score int64
	Add   bool
}

// Four distinct decidable sentinel errors.
var (
	ErrInvalidArgs  = errors.New("topn: require 0 < N <= maxRows")
	ErrDuplicateKey = errors.New("topn: key already live")
	ErrMissingRow   = errors.New("topn: row not live or score mismatch")
	ErrTooManyRows  = errors.New("topn: live rows would exceed maxRows")
)

// View holds ordered live rows, the N boundary, the emitted log and a cap.
type View struct {
	mu         sync.RWMutex
	idx        *rank.Index
	keys       map[string]int64
	n, maxRows int
	log        []Change
}

// New builds a view; n must be positive and maxRows at least n.
func New(n, maxRows int) (*View, error) {
	if n <= 0 || maxRows < n {
		return nil, ErrInvalidArgs
	}
	return &View{idx: rank.NewIndex(), keys: map[string]int64{}, n: n, maxRows: maxRows}, nil
}

// top returns the first min(n, live) rows in ranking order (a copy).
func (v *View) top() []rank.Row {
	t := make([]rank.Row, min(v.n, v.idx.Len()))
	for i := range t {
		t[i] = v.idx.At(i)
	}
	return t
}

// diff returns leaving (-) then entering (+) entries between two tops.
func diff(old, next []rank.Row) []Change {
	st := map[string]uint8{}
	for _, r := range old {
		st[r.Key] = 1
	}
	for _, r := range next {
		if st[r.Key] == 1 {
			st[r.Key] = 3
		} else {
			st[r.Key] = 2
		}
	}
	var out []Change
	for _, r := range old {
		if st[r.Key] == 1 {
			out = append(out, Change{Key: r.Key, Score: r.Score})
		}
	}
	for _, r := range next {
		if st[r.Key] == 2 {
			out = append(out, Change{Key: r.Key, Score: r.Score, Entering: true})
		}
	}
	return out
}

// one validates then applies a single op, returning its changelog.
func (v *View) one(op Op) ([]Change, error) {
	if op.Add {
		if _, ok := v.keys[op.Key]; ok {
			return nil, ErrDuplicateKey
		}
		if len(v.keys) >= v.maxRows {
			return nil, ErrTooManyRows
		}
	} else if s, ok := v.keys[op.Key]; !ok || s != op.Score {
		return nil, ErrMissingRow
	}
	old := v.top()
	r := rank.Row{Key: op.Key, Score: op.Score}
	if op.Add {
		v.idx.Insert(r)
		v.keys[r.Key] = r.Score
	} else {
		v.idx.Delete(r)
		delete(v.keys, r.Key)
	}
	em := diff(old, v.top())
	v.log = append(v.log, em...)
	return em, nil
}

// Apply runs a batch; later ops see earlier ops, any rejection rolls all back.
func (v *View) Apply(ops []Op) (out []Change, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	saved := len(v.log)
	var done []Op
	for _, op := range ops {
		em, e := v.one(op)
		if e != nil { // inverse replay restores both the set and rank order
			for j := len(done) - 1; j >= 0; j-- {
				u := done[j]
				r := rank.Row{Key: u.Key, Score: u.Score}
				if u.Add {
					v.idx.Delete(r)
					delete(v.keys, r.Key)
				} else {
					v.idx.Insert(r)
					v.keys[r.Key] = r.Score
				}
			}
			v.log = v.log[:saved]
			return nil, e
		}
		done, out = append(done, op), append(out, em...)
	}
	return out, nil
}

func (v *View) Top() []rank.Row {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.top()
}

func (v *View) Live() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.keys)
}
