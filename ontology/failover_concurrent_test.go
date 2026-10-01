package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentAccess(t *testing.T) {
	a := mustNew(t, 4, 160, 25, []int{60, 60, 60, 60})
	for i := 0; i < 40; i++ {
		add(t, a, i%4, fmt.Sprintf("h%d", i), i%3 != 0)
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for k := 0; k < 300; k++ {
				id := fmt.Sprintf("h%d", (base+k)%40)
				switch k % 5 {
				case 0:
					_ = a.SetHealth(id, k%2 == 0)
				case 1:
					if loads, err := a.Loads(); err == nil {
						sum := 0
						for _, v := range loads {
							if v < 0 {
								t.Errorf("negative load %v", loads)
							}
							sum += v
						}
						if sum != 100 {
							t.Errorf("loads sum %d", sum)
						}
					}
				case 2:
					if _, err := a.PickLevel((base + k) % 100); err != nil &&
						err != ErrOutOfCapacity {
						t.Errorf("PickLevel: %v", err)
					}
				case 3:
					_ = a.AddHost(0, fmt.Sprintf("tmp%d-%d", base, k), true)
				default:
					_ = a.RemoveHost(fmt.Sprintf("tmp%d-%d", base, k))
				}
			}
		}(w)
	}
	wg.Wait()

	// Final state must be internally consistent: totals match the map.
	totals := make([]int, a.L)
	for _, h := range a.hosts {
		totals[h.level]++
	}
	for p := range totals {
		if totals[p] != a.totals[p] {
			t.Fatalf("level %d map hosts %d != counter %d", p, totals[p], a.totals[p])
		}
	}
}
