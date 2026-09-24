// Package api is the public face of the idempotent sink.
package api

import (
	"fmt"
	"math/rand"

	"ontology/sink"
	"ontology/wm"
)

// Rec is one upstream log record.
type Rec = wm.Rec

var (
	ErrInvalidRecord     = wm.ErrInvalidRecord
	ErrOutOfOrder        = wm.ErrOutOfOrder
	ErrTooManyPartitions = sink.ErrTooManyPartitions
)

// Sink is the public handle.
type Sink struct{ s *sink.Sink }

// New creates a sink allowing up to maxPartitions partitions.
func New(maxPartitions int) *Sink { return &Sink{s: sink.New(maxPartitions)} }

func (s *Sink) Write(b []Rec) error     { return s.s.Write(b) } // atomic; rejected batches leave no trace
func (s *Sink) Restart()                { s.s.Restart() }       // rebuild from the persistent region
func (s *Sink) Table() map[string]int64 { return s.s.Table() }  // copy of the result table

// Watermark returns the high watermark of partition p, -1 if none.
func (s *Sink) Watermark(p int) int64 { return s.s.Watermark(p) }

// Duplicates returns the total number of dropped duplicate records.
func (s *Sink) Duplicates() int64 { return s.s.Duplicates() }

// SelfCheck verifies the four invariants on built-in batch sequences.
func (s *Sink) SelfCheck() error {
	if err := checkNaiveReference(); err != nil {
		return err
	}
	if err := checkPartitionIndependence(); err != nil {
		return err
	}
	return checkFailureAtomicity()
}

// checkNaiveReference covers invariants 1 and 3: random contiguous
// segments of fixed per-partition logs, with random Restarts, must match
// summing each distinct (partition, offset) exactly once.
func checkNaiveReference() error {
	rng := rand.New(rand.NewSource(7))
	sk := New(8)
	logs := make([][]Rec, 4)
	for p := range logs {
		off := int64(0)
		for i := 0; i < 30; i++ {
			logs[p] = append(logs[p], Rec{Partition: p, Offset: off,
				Key: fmt.Sprintf("k%d", (p+i)%3), Val: int64(p*10 + i)})
			off += int64(1 + (p+i)%3) // strictly increasing, holes allowed
		}
	}
	naive := map[string]int64{}
	seen := map[[2]int64]bool{}
	applied := make([]int, 4)
	for b := 0; b < 60; b++ {
		var batch []Rec
		for p := range logs {
			if rng.Intn(2) == 0 {
				continue
			}
			lo := rng.Intn(applied[p] + 1) // start no later than first unapplied
			hi := lo + rng.Intn(len(logs[p])-lo+1)
			for _, r := range logs[p][lo:hi] {
				batch = append(batch, r)
				key := [2]int64{int64(p), r.Offset}
				if !seen[key] {
					seen[key] = true
					naive[r.Key] += r.Val
				}
			}
			if hi > applied[p] {
				applied[p] = hi
			}
		}
		if err := sk.Write(batch); err != nil {
			return fmt.Errorf("selfcheck naive: write: %w", err)
		}
		if rng.Intn(3) == 0 {
			sk.Restart()
		}
	}
	if !equalTable(sk.Table(), naive) {
		return fmt.Errorf("selfcheck naive: table mismatch")
	}
	return nil
}

// checkPartitionIndependence covers invariant 2.
func checkPartitionIndependence() error {
	sk := New(4)
	if err := sk.Write([]Rec{{Partition: 0, Offset: 0, Key: "a", Val: 1},
		{Partition: 0, Offset: 5, Key: "a", Val: 2}}); err != nil {
		return err
	}
	if sk.Watermark(0) != 5 {
		return fmt.Errorf("selfcheck: watermark not advanced")
	}
	for p := 1; p < 4; p++ {
		if sk.Watermark(p) != -1 {
			return fmt.Errorf("selfcheck: partition %d watermark touched", p)
		}
	}
	return nil
}

// checkFailureAtomicity covers invariant 4.
func checkFailureAtomicity() error {
	sk := New(2)
	if err := sk.Write([]Rec{{Partition: 0, Offset: 0, Key: "a", Val: 1}}); err != nil {
		return err
	}
	before, dups := sk.Table(), sk.Duplicates()
	bads := [][]Rec{
		{{Partition: 0, Offset: 1, Key: "", Val: 1}},                                 // invalid record
		{{Partition: 0, Offset: 2, Key: "a", Val: 1}, {Offset: 1, Key: "a", Val: 1}}, // out of order
		{{Partition: 1, Key: "a"}, {Partition: 2, Key: "a"}},                         // too many partitions
	}
	for _, b := range bads {
		if err := sk.Write(b); err == nil {
			return fmt.Errorf("selfcheck: bad batch accepted")
		}
	}
	if !equalTable(sk.Table(), before) || sk.Duplicates() != dups || sk.Watermark(0) != 0 {
		return fmt.Errorf("selfcheck: rejected batch left trace")
	}
	return sk.Write([]Rec{{Partition: 0, Offset: 1, Key: "a", Val: 1}}) // still usable
}

func equalTable(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
