package railway

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// 并发购票：每个座位的每条相邻边最多被一张有效有座票占用；每个乘车人不相交。
// 并发结果必须等价于某个串行顺序（-race 下无数据竞争）。
func TestConcurrentInvariants(t *testing.T) {
	cfg := Config{AdvanceSeconds: 0, StandingRatio: 0.5}
	sp := TrainSpec{
		ID:          "T",
		Departures:  []int{1_000_000, 2_000_000, 3_000_000},
		Cars:        2,
		SeatsPerCar: 2,
		Alloc:       allocN(3, map[[2]int]int{{0, 1}: 50, {0, 2}: 50, {1, 2}: 50}),
		Shared:      50,
	}
	s := NewService(cfg)
	if err := s.AddTrain(sp); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var sold []*Ticket
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				from := (w + i) % 2
				to := from + 1 + (i % (3 - from))
				if to > 2 {
					to = from + 1
				}
				res, err := s.Buy(100, "T",
					TicketDesc{from, to, fmt.Sprintf("u%d", w)}, i%2 == 0)
				if err == nil {
					mu.Lock()
					sold = append(sold, res.Ticket)
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()

	nSeats := sp.Cars * sp.SeatsPerCar
	edgeSeat := make(map[[3]int]int) // (edge,car,no)->count
	edgeStand := make([]int, 2)
	perUser := map[string][]*Ticket{}
	quotaUsed := map[[2]int]int{}
	sharedUsed := 0
	for _, tk := range sold {
		if !tk.Standing {
			for e := tk.From; e < tk.To; e++ {
				k := [3]int{e, tk.Car, tk.No}
				edgeSeat[k]++
				if edgeSeat[k] > 1 {
					t.Fatalf("seat %d-%d double-booked on edge %d", tk.Car, tk.No, e)
				}
			}
		} else {
			for e := tk.From; e < tk.To; e++ {
				edgeStand[e]++
			}
		}
		perUser[tk.Passenger] = append(perUser[tk.Passenger], tk)
		if tk.QuotaShared {
			sharedUsed++
		} else {
			quotaUsed[[2]int{tk.From, tk.To}]++
		}
	}
	cap := nSeats / 2 // floor(4*0.5)
	for e, c := range edgeStand {
		if c > cap {
			t.Fatalf("standing over capacity on edge %d: %d", e, c)
		}
	}
	for u, tks := range perUser {
		for i := range tks {
			for j := i + 1; j < len(tks); j++ {
				if tks[i].From < tks[j].To && tks[j].From < tks[i].To {
					t.Fatalf("user %s holds intersecting tickets", u)
				}
			}
		}
	}
	for od, c := range quotaUsed {
		if c > sp.Alloc[od[0]][od[1]] {
			t.Fatalf("allocation oversold %v: %d", od, c)
		}
	}
}

// 并发退票：同一票最多退一次成功，退后再退必为已退。
func TestConcurrentRefund(t *testing.T) {
	s := NewService(Config{AdvanceSeconds: 0, StandingRatio: 0})
	if err := s.AddTrain(spec4()); err != nil {
		t.Fatal(err)
	}
	tk := buyOK(t, s, 10, "T1", 0, 1, "A", false)
	var wg sync.WaitGroup
	okCount := int32(0)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Refund(20, tk.ID); err == nil {
				atomic.AddInt32(&okCount, 1)
			}
		}()
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("exactly one refund must succeed, got %d", okCount)
	}
}
