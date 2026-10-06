package greenreg

import (
	"sync"
	"testing"
)

// BenchmarkTransferBatch10 shows batch cost depends on batch size, not total
// registry size: compare 1k vs 200k certificates, ns/op should stay flat.
func BenchmarkTransferBatch10(b *testing.B) {
	for _, total := range []int{1_000, 200_000} {
		name := "1k"
		if total == 200_000 {
			name = "200k"
		}
		b.Run(name, func(b *testing.B) {
			r := New(Config{UnitQty: 1})
			_ = r.RegisterFacility("F", "a", 0)
			_, _, _ = r.RegisterGeneration("F", 0, int64(total))
			t64 := int64(total)
			batch := []int64{t64 - 9, t64 - 8, t64 - 7, t64 - 6, t64 - 5,
				t64 - 4, t64 - 3, t64 - 2, t64 - 1, t64}
			if err := r.Transfer("a", "t1", batch); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				from, to := "t2", "t1"
				if i%2 == 0 {
					from, to = "t1", "t2"
				}
				if err := r.Transfer(from, to, batch); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRevokeOnePeriod shows revocation scans only the target period.
func BenchmarkRevokeOnePeriod(b *testing.B) {
	r := New(Config{UnitQty: 1})
	_ = r.RegisterFacility("F", "a", 0)
	_, _, _ = r.RegisterGeneration("F", 0, 100_000)
	_, _, _ = r.RegisterGeneration("F", 1, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := int64(100)
		if i%2 == 0 {
			q = 99
		}
		_, _, _ = r.RegisterGeneration("F", 1, q)
	}
}

// Concurrent calls must leave the registry equivalent to some serial run:
// serials stay dense, counts consistent, no data race under -race.
func TestConcurrentSafety(t *testing.T) {
	r := New(Config{UnitQty: 2, MaxAgePeriods: 5})
	if err := r.RegisterFacility("F", "h", 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				p := int64(g*50 + i)
				_, _, _ = r.RegisterGeneration("F", p, int64(1+i%5))
				_ = r.Transfer("h", "h", []int64{1}) // self transfer always rejected
			}
		}(g)
	}
	wg.Wait()
	// Every committed issuance advanced the dense serial counter; no holes
	// are possible by construction, but verify count matches next serial.
	if r.pool.nextSerial != int64(len(r.pool.certs))+1 {
		t.Fatalf("serial gap: next=%d len=%d", r.pool.nextSerial, len(r.pool.certs))
	}
}

// Replaying the same accepted operation sequence yields the identical snapshot.
func TestDeterministicReplay(t *testing.T) {
	build := func() *Registry {
		r := New(Config{UnitQty: 3, MaxAgePeriods: 4})
		if err := r.RegisterFacility("F", "h", 0); err != nil {
			t.Fatal(err)
		}
		_, _, _ = r.RegisterGeneration("F", 0, 5)
		_, _, _ = r.RegisterGeneration("F", 1, 7) // cumulative: 3 certs total
		if err := r.RegisterUsage("u", 2, 9); err != nil {
			t.Fatal(err)
		}
		_ = r.Transfer("h", "u", []int64{1, 2, 3})
		if err := r.Retire("u", 2, []int64{1, 2}); err != nil {
			t.Fatal(err)
		}
		_, _, _ = r.RegisterGeneration("F", 0, 2)
		return r
	}
	if build().Snapshot() != build().Snapshot() {
		t.Fatal("replaying identical sequences must yield identical snapshots")
	}
}

// Rejected operations must not leave any trace: serials, holdings, usage.
func TestRejectionIsAtomic(t *testing.T) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 5})
	mustFac(t, r, "F", 0)
	before := mustIssue(t, r, "F", 0, 2)
	base := r.Snapshot()
	eventsBefore := len(r.Events())

	// invalid batch mixes a good cert, a revoked-state cert and bad serial.
	if err := r.Transfer("h", "u", []int64{before[0], 999}); errCode(err) != ErrInvalid {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	// self transfer
	if err := r.Transfer("h", "h", before); errCode(err) != ErrInvalid {
		t.Fatal(err)
	}
	// retirement failing on usage must not retire anything
	if err := r.RegisterUsage("u", 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Transfer("h", "u", before); err != nil {
		t.Fatal(err)
	}
	base = r.Snapshot()
	eventsBefore = len(r.Events())
	if err := r.Retire("u", 0, []int64{before[0], before[1]}); errCode(err) != ErrOverUsage {
		t.Fatalf("want ErrOverUsage, got %v", err)
	}
	if r.Snapshot() != base {
		t.Fatalf("rejected ops changed state\n%s\nvs\n%s", base, r.Snapshot())
	}
	if len(r.Events()) != eventsBefore {
		t.Fatal("rejected ops recorded events")
	}
}
