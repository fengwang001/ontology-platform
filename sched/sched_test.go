package sched

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSchedulerCases(t *testing.T) {
	cases := []struct {
		name    string
		tasks   int
		max     int
		wantMax int
	}{
		{"500 tasks cap 8", 500, 8, 8},
		{"50 tasks cap 3", 50, 3, 3},
		{"100 tasks cap 1 serial", 100, 1, 1},
		{"fewer tasks than cap", 5, 8, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.max, tc.tasks)
			var cur, peak int32
			var wg sync.WaitGroup
			wg.Add(tc.tasks)
			go func() {
				for id := range s.Launch() {
					go func(id string) {
						n := atomic.AddInt32(&cur, 1)
						for {
							p := atomic.LoadInt32(&peak)
							if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
								break
							}
						}
						atomic.AddInt32(&cur, -1)
						s.Finish(id)
						wg.Done()
					}(id)
				}
			}()
			for i := 0; i < tc.tasks; i++ {
				s.Enqueue(fmt.Sprintf("t%04d", i))
			}
			done := 0
			for done < tc.tasks {
				<-s.Done()
				s.Release()
				done++
			}
			close(s.start)
			wg.Wait()
			if int(peak) > tc.wantMax {
				t.Fatalf("observed peak=%d > %d", peak, tc.wantMax)
			}
			if s.Peak() > tc.wantMax {
				t.Fatalf("scheduler peak=%d > %d", s.Peak(), tc.wantMax)
			}
			if tc.tasks < tc.max && s.Peak() != tc.tasks {
				t.Fatalf("peak=%d want %d", s.Peak(), tc.tasks)
			}
			bound := 4 * tc.tasks
			if s.Decisions() > bound {
				t.Fatalf("decisions=%d > bound %d", s.Decisions(), bound)
			}
		})
	}
}
