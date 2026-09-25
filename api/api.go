// Package api is the public face of the reliable-broadcast receiver.
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/rb"
)

// Decidable, mutually distinct rejection reasons.
var (
	ErrEmptySender = rb.ErrEmptySender
	ErrNegativeSeq = rb.ErrNegativeSeq
	ErrBufferFull  = rb.ErrBufferFull
)

// API is the external handle to the receiver.
type API struct{ r *rb.Receiver }

// New returns an API with the given per-sender reorder-buffer limit.
func New(maxBuffered int) *API { return &API{r: rb.New(maxBuffered)} }

// Deliver applies one arrival of (sender, seq, data).
func (a *API) Deliver(sender string, seq int64, data any) error {
	return a.r.Deliver(sender, seq, data)
}

// Delivered returns the sender's delivered log, ascending seq from 0.
func (a *API) Delivered(sender string) []any { return a.r.Delivered(sender) }

// Dup returns the sender's duplicate arrival count.
func (a *API) Dup(sender string) int { return a.r.Dup(sender) }

// naive is the reference: dedup arrivals, sort by seq ascending.
func naive(seqs []int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, s := range seqs {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for i := 1; i < len(out); i++ { // insertion sort, stdlib only
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// SelfCheck verifies the four invariants on built-in arrival
// sequences, using a fresh internal receiver (user state untouched).
func (a *API) SelfCheck() error {
	// 1+2: exactly-once and FIFO on the seven-step sequence.
	r := rb.New(16)
	for _, s := range []int64{0, 2, 1, 2, 3, 0, 4} {
		if err := r.Deliver("S", s, s); err != nil {
			return fmt.Errorf("selfcheck seven-step: %w", err)
		}
	}
	for i, v := range r.Delivered("S") {
		if v.(int64) != int64(i) { // log must be 0,1,2,3,4
			return fmt.Errorf("selfcheck fifo: log[%d]=%v", i, v)
		}
	}
	if d := r.Dup("S"); d != 2 {
		return fmt.Errorf("selfcheck exactly-once: dups=%d, want 2", d)
	}
	// 3: shuffled 0..n-1 plus random duplicates matches naive.
	rnd := rand.New(rand.NewSource(1))
	for _, n := range []int{10, 200, 2000} {
		r2 := rb.New(n)
		arr := make([]int64, 0, 2*n)
		for _, p := range rnd.Perm(n) {
			arr = append(arr, int64(p))
		}
		for i := 0; i < n; i++ {
			arr = append(arr, int64(rnd.Intn(n)))
		}
		rnd.Shuffle(len(arr), func(i, j int) { arr[i], arr[j] = arr[j], arr[i] })
		for _, s := range arr {
			if err := r2.Deliver("R", s, s); err != nil {
				return fmt.Errorf("selfcheck naive n=%d: %w", n, err)
			}
		}
		want := naive(arr)
		got := r2.Delivered("R")
		if len(got) != len(want) {
			return fmt.Errorf("selfcheck naive n=%d: len %d != %d", n, len(got), len(want))
		}
		for i := range want {
			if got[i].(int64) != want[i] {
				return fmt.Errorf("selfcheck naive n=%d: idx %d", n, i)
			}
		}
	}
	// 4: rejections are distinct, decidable and leave no trace.
	r3 := rb.New(1)
	if err := r3.Deliver("F", 0, 0); err != nil { // log [0]
		return fmt.Errorf("selfcheck setup: %w", err)
	}
	if err := r3.Deliver("F", 3, 3); err != nil { // buffer {3}, now full
		return fmt.Errorf("selfcheck setup: %w", err)
	}
	cases := []struct {
		sender string
		seq    int64
		want   error
	}{{"", 0, ErrEmptySender}, {"F", -1, ErrNegativeSeq}, {"F", 5, ErrBufferFull}}
	for _, c := range cases {
		if err := r3.Deliver(c.sender, c.seq, nil); !errors.Is(err, c.want) {
			return fmt.Errorf("selfcheck reject %+v: %v", c, err)
		}
	}
	if len(r3.Delivered("F")) != 1 || r3.Dup("F") != 0 || len(r3.Delivered("")) != 0 {
		return errors.New("selfcheck: rejected delivery changed state")
	}
	// Still usable: 1,2 cascade the surviving buffered 3; delivering 4
	// must not cascade a stashed 5 (the rejected entry left no trace).
	for _, s := range []int64{1, 2, 4} {
		if err := r3.Deliver("F", s, s); err != nil {
			return fmt.Errorf("selfcheck reuse: %w", err)
		}
	}
	if got := r3.Delivered("F"); len(got) != 5 {
		return fmt.Errorf("selfcheck trace: log len=%d, want 5", len(got))
	}
	return nil
}
