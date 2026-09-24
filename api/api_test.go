package api_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

func TestSelfCheck(t *testing.T) {
	for _, ps := range [][2]int{{1, 0}, {2, 3}, {6, 100}} {
		e, err := api.New(ps[0], ps[1])
		if err != nil {
			t.Fatal(err)
		}
		if err := e.SelfCheck(); err != nil {
			t.Fatalf("p=%d S=%d: %v", ps[0], ps[1], err)
		}
	}
}

func TestConcurrentAdd(t *testing.T) {
	for _, g := range []int{1, 2, 4, 16} {
		const per = 250
		par, _ := api.New(6, 10)
		var wg sync.WaitGroup
		for w := 0; w < g; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < per; i++ {
					if err := par.Add(fmt.Sprintf("k-%d-%d", w, i)); err != nil {
						t.Error(err)
					}
					_ = par.Card() // 并发读不得崩溃
				}
			}(w)
		}
		wg.Wait()
		ser, _ := api.New(6, 10)
		for w := 0; w < g; w++ {
			for i := 0; i < per; i++ {
				ser.Add(fmt.Sprintf("k-%d-%d", w, i))
			}
		}
		if par.Card() != ser.Card() {
			t.Fatalf("g=%d: 并发 Card=%v != 串行 %v", g, par.Card(), ser.Card())
		}
	}
}
