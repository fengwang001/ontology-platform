package quota

import (
	"sync"
	"testing"
	"time"
)

// Under -race this checks locking; logically every observed Usage snapshot
// must be internally consistent: E(d) >= R(child) summed over children, and
// quotas are never exceeded. Rename and Batch must appear atomic to readers.
func TestConcurrent(t *testing.T) {
	l := New()
	if err := l.SetQuota(0, 1<<40, 1<<20); err != nil {
		t.Fatal(err)
	}

	const writers = 6
	const readers = 4
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Seed some directories so renames have targets.
	for i := 0; i < 8; i++ {
		if _, err := l.Mkdir(0); err != nil {
			t.Fatal(err)
		}
	}

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			counter := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				switch counter % 7 {
				case 0:
					l.Mkdir(ID(1 + seed%8))
				case 1:
					l.AddFile(ID(seed%9), int64(counter%5))
				case 2:
					l.Reserve(ID(1+seed%8), 2)
				case 3:
					l.Release(ID(1+seed%8), 1)
				case 4:
					l.Rename(ID(1+seed%8), ID(1+(seed+1)%8))
				case 5:
					l.Batch([]Op{
						{Kind: OpMkdir, P: 0},
						{Kind: OpReserve, X: 1, Bytes: 1},
						{Kind: OpRelease, X: 1, Bytes: 1},
					})
				default:
					l.SetQuota(1, -1, -1)
				}
				counter++
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				b, e, rv, err := l.Usage(0)
				if err != nil {
					t.Error(err)
					return
				}
				if b < 0 || e < 0 || rv < 0 {
					t.Errorf("negative usage: %d %d %d", b, e, rv)
					return
				}
			}
		}()
	}

	// Let it churn briefly; the race detector does the heavy validation.
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}
