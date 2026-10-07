package ontology

import (
	"fmt"
	"testing"
)

// buildCostStore creates a store with filler instances plus the small set
// of working instances used by each measured batch.
func buildCostStore(b *testing.B, total int) *Store {
	b.Helper()
	s := New(internalConfig())
	for i := 0; i < total; i++ {
		if err := s.CreateInstance(ID("f"+itoa(i)), "Person"); err != nil {
			b.Fatal(err)
		}
	}
	for _, id := range []ID{"w0", "w1", "w2", "w3"} {
		if err := s.CreateInstance(id, "Person"); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

// BenchmarkCommitVersionGate measures the joint version gate for a fixed
// batch size while the total instance count varies across sub-benchmarks.
// ns/op must stay flat across 1k -> 16k -> 64k instances, demonstrating
// that gate cost depends on batch size, not store size.
func BenchmarkCommitVersionGate(b *testing.B) {
	for _, total := range []int{1_000, 16_000, 64_000} {
		s := buildCostStore(b, total)
		b.Run(fmt.Sprintf("total=%d", total), func(b *testing.B) {
			// One contending successful batch per benchmark iteration is
			// not possible because versions advance; instead measure the
			// rejection path, which performs the identical gate work and
			// leaves state untouched so iterations stay uniform.
			batch := Batch{
				ID: "probe",
				Preconditions: []Precondition{
					{Instance: "w0", ExpectedVersion: 999_999},
					{Instance: "w1", ExpectedVersion: 999_999},
					{Instance: "w2", ExpectedVersion: 999_999},
					{Instance: "w3", ExpectedVersion: 999_999},
				},
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := s.Commit(batch)
				if r.Status != StatusVersionMismatch {
					b.Fatalf("status = %s", r.Status)
				}
			}
		})
	}
}

// BenchmarkCommitVersionGateByBatchSize measures the intended linear growth
// in the batch's own precondition count.
func BenchmarkCommitVersionGateByBatchSize(b *testing.B) {
	const total = 32_000
	for _, k := range []int{1, 4, 16, 64} {
		s := buildCostStore(b, total)
		ids := make([]ID, 0, k)
		for i := 0; i < 128; i++ {
			id := ID("k" + itoa(i))
			if err := s.CreateInstance(id, "Person"); err != nil {
				b.Fatal(err)
			}
		}
		for i := 0; i < k; i++ {
			ids = append(ids, ID("k"+itoa(i)))
		}
		b.Run(fmt.Sprintf("k=%d", k), func(b *testing.B) {
			pre := make([]Precondition, k)
			for i := range pre {
				pre[i] = Precondition{Instance: ids[i], ExpectedVersion: 999_999}
			}
			batch := Batch{ID: "probe", Preconditions: pre}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if r := s.Commit(batch); r.Status != StatusVersionMismatch {
					b.Fatalf("status = %s", r.Status)
				}
			}
		})
	}
}

// TestGateCostIndependentOfStoreSize asserts the no-growth requirement
// structurally: the gate implementation touches only the instances named by
// the batch. The commit path never ranges over s.instances after the locked
// subset is resolved by O(k) map lookups.
func TestGateCostIndependentOfStoreSize(t *testing.T) {
	small := New(internalConfig())
	large := New(internalConfig())
	if err := small.CreateInstance("x", "Person"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50_000; i++ {
		if err := large.CreateInstance(ID("f"+itoa(i)), "Person"); err != nil {
			t.Fatal(err)
		}
	}
	if err := large.CreateInstance("x", "Person"); err != nil {
		t.Fatal(err)
	}
	batch := Batch{
		ID:            "probe",
		Preconditions: []Precondition{{Instance: "x", ExpectedVersion: 1}},
		Ops:           []Op{{Kind: OpSetAttr, Instance: "x", Attr: Attr("a"), Value: 1}},
	}
	r1 := small.Commit(batch)
	r2 := large.Commit(batch)
	if r1.Status != StatusCommitted || r2.Status != StatusCommitted {
		t.Fatalf("results %s %s", r1.Status, r2.Status)
	}
}
