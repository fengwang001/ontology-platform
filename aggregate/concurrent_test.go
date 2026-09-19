package aggregate

import (
	"sync"
	"testing"
)

func TestConcurrentAddNoLostRows(t *testing.T) {
	agg := NewAggregator([]string{"g"}, "v")
	const workers = 16
	const perWorker = 2000

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Concurrent snapshots: all values are 1.0, so for any consistent
	// snapshot the group sum must equal its contributing count exactly.
	// Observing Count advanced without its sum would prove a torn update.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				res, err := agg.Snapshot()
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				for _, r := range res {
					contribs := float64(r.Count - r.Skipped)
					if r.SumValid && r.FloatSum != contribs {
						t.Errorf("torn snapshot: count=%d skipped=%d sum=%v",
							r.Count, r.Skipped, r.FloatSum)
						return
					}
				}
			}
		}
	}()

	var adders sync.WaitGroup
	for w := 0; w < workers; w++ {
		adders.Add(1)
		go func() {
			defer adders.Done()
			for i := 0; i < perWorker; i++ {
				agg.Add(map[string]any{"g": "x", "v": 1.0})
			}
		}()
	}
	adders.Wait()
	close(stop)
	wg.Wait()

	res, err := agg.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("want single group, got %d", len(res))
	}
	want := int64(workers * perWorker)
	if res[0].Count != want {
		t.Fatalf("lost rows: want count %d, got %d", want, res[0].Count)
	}
	if res[0].Skipped != 0 || res[0].FloatSum != float64(want) {
		t.Fatalf("unexpected sum state: %+v", res[0])
	}
}
