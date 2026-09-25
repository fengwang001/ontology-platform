// Package api is the public, concurrency-safe entry point; depends on rmq only.
package api

import (
	"errors"
	"math"
	"sync"

	"ontology/rmq"
)

// Four distinct, decidable sentinel failures (re-exported from rmq).
var (
	ErrEmptyInput, ErrInvalidRange           = rmq.ErrEmptyInput, rmq.ErrInvalidRange
	ErrRangeOutOfBounds, ErrIndexOutOfBounds = rmq.ErrRangeOutOfBounds, rmq.ErrIndexOutOfBounds
)

// RMQ is a range-minimum structure supporting point updates.
type RMQ struct {
	mu   sync.RWMutex
	core *rmq.Structure
	arr  []int64 // mirror of the logical array, used by SelfCheck
}

// New builds from arr; an empty slice is rejected and leaves no state.
func New(arr []int64) (*RMQ, error) {
	s, err := rmq.Build(arr)
	if err != nil {
		return nil, err
	}
	return &RMQ{core: s, arr: append([]int64(nil), arr...)}, nil
}

// Query returns the minimum over the half-open [l, r); [l,l) is MaxInt64.
func (q *RMQ) Query(l, r int) (int64, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.core.Query(l, r)
}

// Update sets position i to v and propagates to the root; the mirror is
// touched only after the core accepted the operation.
func (q *RMQ) Update(i int, v int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.core.Update(i, v); err != nil {
		return err
	}
	q.arr[i] = v
	return nil
}

// Size reports the logical array length.
func (q *RMQ) Size() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.core.Size()
}

// Built-in arrays: n=1, n=8 and two non-power-of-two lengths.
var selfCheckArrays = [][]int64{
	{5},
	{5, 2, 8, 1, 9, 3, 7, 4},
	{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5}, // n=11 -> 16 leaves
	{-1, -2, 0, 7, -100, 4},           // n=6 -> 8 leaves
}

type minQuerier interface{ Query(l, r int) (int64, error) }

// SelfCheck verifies the four invariants on the receiver and on built-in
// arrays/operation sequences, the empty rejection and the O(log n) bound.
func (q *RMQ) SelfCheck() error {
	q.mu.RLock()
	err := exhaustivelyEqual(q.core, q.arr) // invariants 1 & 3 on receiver
	q.mu.RUnlock()
	if err != nil {
		return err
	}
	for _, base := range selfCheckArrays {
		r, err := New(base)
		if err != nil {
			return err
		}
		mirror := append([]int64(nil), base...)
		if err := exhaustivelyEqual(r, mirror); err != nil { // invariant 1
			return err
		}
		for _, u := range [][2]int64{{0, 10}, {int64(len(mirror) / 2), 0}, {int64(len(mirror) - 1), -7}} {
			if err := r.Update(int(u[0]), u[1]); err != nil { // invariant 2
				return err
			}
			mirror[u[0]] = u[1]
			if err := exhaustivelyEqual(r, mirror); err != nil {
				return err
			}
		}
		if err := rejectAndStayClean(r, mirror); err != nil { // invariant 4
			return err
		}
	}
	if _, err := New(nil); !errors.Is(err, ErrEmptyInput) {
		return errors.New("api: empty array was not rejected with ErrEmptyInput")
	}
	return rmq.VerifyLogAccess()
}

// exhaustivelyEqual compares every interval [l, r) against a naive scan.
func exhaustivelyEqual(r minQuerier, arr []int64) error {
	for l := 0; l <= len(arr); l++ {
		for rr := l; rr <= len(arr); rr++ {
			got, err := r.Query(l, rr)
			if err != nil || got != naiveMin(arr, l, rr) {
				return errors.New("api: SelfCheck found a query mismatch")
			}
		}
	}
	return nil
}

// rejectAndStayClean fires the four distinct failures, then proves state
// is unchanged by re-running the exhaustive comparison.
func rejectAndStayClean(r *RMQ, arr []int64) error {
	n := len(arr)
	cases := []struct {
		want error
		fn   func() error
	}{
		{ErrInvalidRange, func() error { _, e := r.Query(1, 0); return e }},
		{ErrRangeOutOfBounds, func() error { _, e := r.Query(-1, 0); return e }},
		{ErrRangeOutOfBounds, func() error { _, e := r.Query(0, n+1); return e }},
		{ErrIndexOutOfBounds, func() error { return r.Update(n, 0) }},
		{ErrIndexOutOfBounds, func() error { return r.Update(-1, 0) }},
	}
	for _, c := range cases {
		if !errors.Is(c.fn(), c.want) {
			return errors.New("api: a rejected op returned the wrong sentinel")
		}
	}
	return exhaustivelyEqual(r, arr)
}

func naiveMin(arr []int64, l, r int) int64 {
	m := int64(math.MaxInt64) // empty interval [l,l): +Inf identity
	for _, v := range arr[l:r] {
		if v < m {
			m = v
		}
	}
	return m
}
