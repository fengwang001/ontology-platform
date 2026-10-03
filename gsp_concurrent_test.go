package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrent 并发调用所有操作与查询，验证线性一致（无竞态、无数据损坏、
// 0<=h<=budget，且 h 等于未结算中标占用之和）。
func TestConcurrent(t *testing.T) {
	e := NewEngine()
	const bidders = 60
	for i := 0; i < bidders; i++ {
		mustReg(t, e, fmt.Sprintf("c%02d", i), int64(100+i%200), int64(1+i%1000), 10_000_000)
	}

	// invariant 检查全部竞价者与拍卖记录。
	invariant := func() bool {
		e.mu.RLock()
		defer e.mu.RUnlock()
		wantH := map[int64]int64{}
		for _, a := range e.auctions {
			if a.resolved {
				continue
			}
			for _, w := range a.winners {
				wantH[w.bidderSeq] += w.p
			}
		}
		for _, b := range e.bidders {
			if b.h < 0 || b.h > b.budget {
				return false
			}
			if b.h != wantH[b.seq] {
				return false
			}
			if b.q < 1 || b.q > 1000 || b.f < 0 {
				return false
			}
		}
		return true
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Auction worker。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			k := 1 + id*3
			p := int64(50 + id*10)
			if p > 1e6 {
				p = 1e6
			}
			for {
				select {
				case <-stop:
					return
				default:
					if ws, err := e.Auction(k, p); err == nil {
						for _, win := range ws {
							if win.P < p || win.P > 1e6 {
								t.Errorf("price out of range: %d", win.P)
								return
							}
						}
					}
				}
			}
		}(w)
	}

	// Resolve worker：反复扫描未结算拍卖并结算。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				e.mu.RLock()
				var target int64
				for id, a := range e.auctions {
					if !a.resolved {
						target = id
						break
					}
				}
				e.mu.RUnlock()
				if target == 0 {
					continue
				}
				st, _ := e.GetAuction(target)
				if !st.Resolved {
					var clicks []string
					for _, w := range st.Winners {
						if w.ID[1]%2 == 0 {
							clicks = append(clicks, w.ID)
						}
					}
					// ErrAuctionResolved 是并发下允许的正常结果。
					_ = e.Resolve(target, clicks)
				}
			}
		}()
	}

	// 查询 worker。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = e.GetBidder(fmt.Sprintf("c%02d", id))
					_ = e.NumBidders()
					_ = e.NumAuctions()
					_ = e.SortCompares()
					if !invariant() {
						t.Errorf("invariant violated")
						return
					}
				}
			}
		}(w)
	}

	// 并发 Register 同一批 id，验证“恰好一个成功”。
	var regWg sync.WaitGroup
	success := make(chan int, 8)
	for w := 0; w < 8; w++ {
		regWg.Add(1)
		go func() {
			defer regWg.Done()
			if err := e.Register("race-id", 500, 500, 500_000); err == nil {
				success <- 1
			}
		}()
	}
	regWg.Wait()
	close(success)
	nSuccess := 0
	for range success {
		nSuccess++
	}
	if nSuccess != 1 {
		t.Fatalf("concurrent register same id succeeded %d times, want 1", nSuccess)
	}

	close(stop)
	wg.Wait()

	if !invariant() {
		t.Fatalf("final invariant violated")
	}
	t.Logf("并发结束: bidders=%d auctions=%d，不变量全程成立，同 id 并发登记恰好 1 次成功",
		e.NumBidders(), e.NumAuctions())
}

// TestDeterministicReplay 相同操作序列重放两次，赢家、价格、质量分完全相同。
func TestDeterministicReplay(t *testing.T) {
	run := func() string {
		e := NewEngine()
		ids := []string{"p", "q", "r", "s", "t"}
		bids := []int64{77, 93, 93, 12, 60}
		qs := []int64{500, 400, 400, 900, 600}
		out := ""
		for i, id := range ids {
			if err := e.Register(id, bids[i], qs[i], 10_000_000); err != nil {
				t.Fatal(err)
			}
		}
		for auc := int64(1); auc <= 6; auc++ {
			ws, err := e.Auction(3, 20)
			if err != nil {
				t.Fatal(err)
			}
			var clicks []string
			for i, w := range ws {
				out += fmt.Sprintf("a%d:%s p%d qe%d;", auc, w.ID, w.P, w.Qe)
				if int(auc)+i%2 == 0 {
					clicks = append(clicks, w.ID)
				}
			}
			if err := e.Resolve(auc, clicks); err != nil {
				t.Fatal(err)
			}
		}
		for _, id := range ids {
			b, _ := e.GetBidder(id)
			out += fmt.Sprintf("%s{q%d,b%d,h%d,f%d};", id, b.Q, b.Budget, b.H, b.F)
		}
		return out
	}
	first := run()
	second := run()
	if first != second {
		t.Fatalf("replay differs:\nfirst:  %s\nsecond: %s", first, second)
	}
	t.Logf("判定依据: 两次重放输出完全一致 => %s", first)
}
