package cfs

import (
	"sync"
	"testing"
)

func TestConcurrentOperationsAndQueries(t *testing.T) {
	c, err := New(8, 4, 3, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	for cpu := 0; cpu < 4; cpu++ {
		mustWake(t, c, 0, cpu)
	}

	var wg sync.WaitGroup
	for cpu := 0; cpu < 4; cpu++ {
		wg.Add(1)
		go func(cpu int) {
			defer wg.Done()
			now := int64(1)
			for i := 0; i < 200; i++ {
				if i%3 == 0 {
					_ = c.Run(now, cpu, 1)
				} else {
					_ = c.Wake(now, cpu)
					_ = c.Idle(now, cpu)
				}
				now++
			}
		}(cpu)
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = c.Stats()
				_ = c.Pool()
				_ = c.Queue()
				if _, err := c.State(i % 4); err != nil {
					t.Errorf("State: %v", err)
				}
			}
		}()
	}

	wg.Wait()
}
