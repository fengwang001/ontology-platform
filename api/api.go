// Package api is the public face of the double-ended priority queue.
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/dpq"
)

// ErrSelfCheck marks a SelfCheck invariant failure (match with errors.Is).
var ErrSelfCheck = errors.New("api: self-check failed")

// PQ is a concurrency-safe double-ended priority queue.
type PQ struct{ q dpq.Queue }

func New() *PQ { return &PQ{} }

func (p *PQ) Push(v int64)             { p.q.Push(v) }
func (p *PQ) Min() (int64, bool)       { return p.q.Min() }
func (p *PQ) Max() (int64, bool)       { return p.q.Max() }
func (p *PQ) DeleteMin() (int64, bool) { return p.q.DeleteMin() }
func (p *PQ) DeleteMax() (int64, bool) { return p.q.DeleteMax() }
func (p *PQ) Len() int                 { return p.q.Len() }

// SelfCheck runs built-in operation sequences over a fresh queue and
// verifies the four invariants: naive-model agreement, heap structure,
// length conservation, and no trace left by failed empty-queue calls.
func (p *PQ) SelfCheck() error {
	q, r := New(), rand.New(rand.NewSource(1))
	var model []int64
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrSelfCheck, fmt.Sprintf(format, args...))
	}
	for step := 0; step < 4000; step++ {
		switch r.Intn(6) {
		case 0, 1, 2: // negatives and duplicates included
			v := int64(r.Intn(41) - 20)
			q.Push(v)
			model = append(model, v)
		case 3:
			got, ok := q.DeleteMin()
			want, wok := peek(&model, true)
			if got != want || ok != wok {
				return fail("step %d: DeleteMin=(%d,%v), want (%d,%v)", step, got, ok, want, wok)
			}
			drop(&model, true)
		case 4:
			got, ok := q.DeleteMax()
			want, wok := peek(&model, false)
			if got != want || ok != wok {
				return fail("step %d: DeleteMax=(%d,%v), want (%d,%v)", step, got, ok, want, wok)
			}
			drop(&model, false)
		case 5:
			if err := checkMinMax(q, model); err != nil {
				return fail("step %d: %v", step, err)
			}
		}
		if q.Len() != len(model) {
			return fail("step %d: Len=%d, want %d", step, q.Len(), len(model))
		}
		if err := q.q.Check(); err != nil {
			return fail("step %d: %v", step, err)
		}
	}
	return nil
}

// checkMinMax compares Min/Max against a naive scan of the model.
func checkMinMax(q *PQ, model []int64) error {
	mn, ok1 := q.Min()
	mx, ok2 := q.Max()
	if len(model) == 0 {
		if ok1 || ok2 {
			return errors.New("empty queue reports a value")
		}
		return nil
	}
	wmn, wmx := model[0], model[0]
	for _, v := range model[1:] {
		wmn, wmx = min(wmn, v), max(wmx, v)
	}
	if !ok1 || !ok2 || mn != wmn || mx != wmx {
		return fmt.Errorf("Min/Max=(%d,%d), want (%d,%d)", mn, mx, wmn, wmx)
	}
	return nil
}

// extremeIndex returns the index of the min (or max) value in model.
func extremeIndex(model []int64, minWanted bool) int {
	k := 0
	for i, v := range model {
		if minWanted && v < model[k] || !minWanted && v > model[k] {
			k = i
		}
	}
	return k
}

// peek returns the model's extreme value without removing it.
func peek(model *[]int64, minWanted bool) (int64, bool) {
	if len(*model) == 0 {
		return 0, false
	}
	return (*model)[extremeIndex(*model, minWanted)], true
}

// drop removes the model's extreme value.
func drop(model *[]int64, minWanted bool) {
	if len(*model) == 0 {
		return
	}
	k := extremeIndex(*model, minWanted)
	s := *model
	*model = append(s[:k:k], s[k+1:]...)
}
