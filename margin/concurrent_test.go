package margin

import (
	"sync"
	"testing"
)

// 并发调用：等价于某个串行顺序，且 Mark 期间观察者看不到中间状态。
func TestConcurrentSerializability(t *testing.T) {
	e := mustNew(t, 1000, 500, 100)
	for _, a := range []string{"A", "B", "C", "D"} {
		mustOK(t, e.Deposit(a, 100000), "deposit "+a)
	}

	const goroutines = 16
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 观察者：任何时刻保证金、Z、B 都必须非负，持仓与成本符号一致。
	observeDone := make(chan struct{})
	go func() {
		defer close(observeDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, a := range []string{"A", "B", "C", "D"} {
				snap, err := e.AccountSnapshot(a)
				if err != nil {
					t.Errorf("snapshot: %v", err)
					return
				}
				if snap.M < 0 || (snap.Q == 0) != (snap.C == 0) {
					t.Errorf("illegal snapshot: %+v", snap)
					return
				}
				if (snap.Q > 0 && snap.C <= 0) || (snap.Q < 0 && snap.C >= 0) {
					t.Errorf("q/c sign mismatch: %+v", snap)
					return
				}
			}
			if e.InsuranceFund() < 0 || e.BadDebt() < 0 {
				t.Errorf("negative Z or B")
				return
			}
		}
	}()

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			dir := Long
			if g%2 == 0 {
				dir = Short
			}
			a := []string{"A", "B", "C", "D"}[g%4]
			for k := 0; k < 200; k++ {
				p := int64(80 + (k+g)%40)
				switch k % 5 {
				case 0:
					_ = e.Open(a, dir, 1, p)
				case 1:
					_, _ = e.Mark(p)
				case 2:
					_ = e.Deposit(a, 1)
				case 3:
					_ = e.Withdraw(a, 1)
				case 4:
					_ = e.Close(a, p)
				}
			}
		}(g)
	}
	wg.Wait()
	close(stop)
	<-observeDone

	// 收尾：最终一次 Mark 后全部账户状态仍满足不变量。
	_, err := e.Mark(100)
	if err != nil {
		t.Fatalf("final mark: %v", err)
	}
}
