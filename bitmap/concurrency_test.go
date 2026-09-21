package bitmap

import (
	"sync"
	"testing"
)

// TestConcurrentSetClear hammers one Bitmap from many goroutines:
// setters own disjoint bit ranges, clearers only touch bits that
// stay 0, and readers continuously call Bytes/Count/Verify. After
// the join, Verify must pass and Count must equal the exact number
// of set bits. Run with -race.
func TestConcurrentSetClear(t *testing.T) {
	const (
		setters      = 8
		bitsPerSet   = 2000
		clearers     = 4
		clearsPerOne = 2000
	)
	b := New()
	var wg sync.WaitGroup
	// Setters: disjoint ranges [base, base+bitsPerSet).
	for s := 0; s < setters; s++ {
		base := uint32(s * bitsPerSet)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := uint32(0); i < bitsPerSet; i++ {
				b.Set(base + i)
			}
		}()
	}
	// Clearers: a disjoint, never-set region; redundant clears must
	// be safe and idempotent under concurrency.
	clearBase := uint32(setters * bitsPerSet)
	for c := 0; c < clearers; c++ {
		lo := clearBase + uint32(c*clearsPerOne)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := uint32(0); i < clearsPerOne; i++ {
				b.Clear(lo + i)
			}
		}()
	}
	// Readers: must never observe a half-updated structure.
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = b.Bytes()
				_ = b.Count()
				if err := b.Verify(); err != nil {
					t.Errorf("concurrent Verify: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	readers.Wait()
	if err := b.Verify(); err != nil {
		t.Fatalf("final Verify: %v", err)
	}
	if got, want := b.Count(), uint64(setters*bitsPerSet); got != want {
		t.Fatalf("Count = %d, want %d (lost updates)", got, want)
	}
	// The canonical encoding of the result must equal a bitmap
	// built in one shot.
	oneShot := New()
	oneShot.SetRange(0, setters*bitsPerSet-1)
	if got, want := b.Bytes(), oneShot.Bytes(); string(got) != string(want) {
		t.Fatal("concurrent result encoding != single-shot encoding")
	}
}
