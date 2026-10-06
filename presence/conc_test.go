package presence

import (
	"sync"
	"testing"
)

// TestConcurrentSmoke hammers the service from many goroutines to exercise
// shard locking and the all-or-nothing drain under the race detector.
func TestConcurrentSmoke(t *testing.T) {
	svc, err := New(Config{LeaseSeconds: 5, Shards: 4})
	if err != nil {
		t.Fatal(err)
	}
	users := []string{"a", "b", "c", "d"}
	for _, u := range users {
		must(t, svc.Report(u, "d0", Online, 0))
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				now := int64(i)
				u := users[g%len(users)]
				v := users[(g+1)%len(users)]
				switch i % 8 {
				case 0:
					_ = svc.Report(u, "d0", []Status{Away, Busy, Online}[i%3], now)
				case 1:
					_ = svc.Subscribe(v, u, now)
				case 2:
					_, _ = svc.Drain(v, now)
				case 3:
					_, _ = svc.Query(v, u, now)
				case 4:
					_ = svc.SetInvisible(u, i%2 == 0, now)
				case 5:
					_ = svc.Block(u, v, now)
				case 6:
					_ = svc.Unblock(u, v, now)
				case 7:
					_ = svc.Unsubscribe(v, u, now)
				}
			}
		}(g)
	}
	wg.Wait()
}
