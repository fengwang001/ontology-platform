package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentUpdatesAndSnapshots(t *testing.T) {
	tracker, err := NewTracker(2, []Edge{{0, 1, 1}}, []int{0})
	if err != nil {
		t.Fatal(err)
	}

	var writers sync.WaitGroup
	var readers sync.WaitGroup
	stop := make(chan struct{})

	for worker := 0; worker < 4; worker++ {
		writers.Add(1)
		go func(worker int) {
			defer writers.Done()
			for i := 0; i < 100; i++ {
				time := int64(worker*100 + i)
				tracker.Update([]Delta{{0, time, 1}})
				tracker.Update([]Delta{{0, time, -1}})
			}
		}(worker)
	}

	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					version, frontiers := tracker.Frontiers()
					if len(frontiers) != 2 || frontiers[0] < -1 || frontiers[1] < -1 {
						t.Errorf("invalid snapshot ver=%d frontiers=%v", version, frontiers)
						return
					}
				}
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()

	if version, frontiers := tracker.Frontiers(); version != 800 {
		t.Fatalf("version = %d, frontiers = %v", version, frontiers)
	}
}
