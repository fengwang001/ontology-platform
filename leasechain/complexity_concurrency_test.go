package leasechain

import (
	"fmt"
	"sync"
	"testing"
)

// TestResponsibleChainCostIsDepthOnly 证明责任人查询开销只与该欠费所在链深度
// 有关、与系统内租约总数无关：
// 在大量“其他”租约存在时，对深度 d 的欠费，沿父指针行走步数恰为 2d+1
// （每层一次入链 + 一次取父指针），而与背景租约数量 N 无关。
func TestResponsibleChainCostIsDepthOnly(t *testing.T) {
	for _, depth := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("depth=%d", depth), func(t *testing.T) {
			build := func(bg int) (chainLen, steps int) {
				s := newTestService(t)
				root, _ := s.CreateMaster(0, "LL", "R0", 5, 400, 1000)
				parent := root.ID
				var leaf int64
				for i := 1; i <= depth; i++ {
					tn := fmt.Sprintf("L%d", i)
					if err := s.GrantGeneral(0, "LL", tn); err != nil {
						t.Fatal(err)
					}
					l, err := s.Sublease(0, parent, tn, 5, 400, 1000)
					if err != nil {
						t.Fatal(err)
					}
					parent, leaf = l.ID, l.ID
				}
				for i := 0; i < bg; i++ {
					if _, err := s.CreateMaster(0, fmt.Sprintf("X%d", i), "BX", 5, 400, 1000); err != nil {
						t.Fatal(err)
					}
				}
				due := installments(5, 400, 10, 1000)[0].due
				if err := s.Advance(due + 5); err != nil {
					t.Fatal(err)
				}
				var arrearID int64
				for _, a := range s.allArrears() {
					if a.LeaseID == leaf {
						arrearID = a.ID
					}
				}
				if arrearID == 0 {
					t.Fatal("leaf arrear missing")
				}
				ids, steps, err := s.ResponsibleChain(arrearID)
				if err != nil {
					t.Fatal(err)
				}
				return len(ids), steps
			}

			// N=10 与 N=2000 两种系统规模，责任人链长度与行走步数必须相同，
			// 且步数恰为 2*深度+1（每层一次入链 + 一次取父指针），与 N 无关。
			len1, steps1 := build(10)
			len2, steps2 := build(2000)
			if len1 != depth+1 || len2 != depth+1 {
				t.Fatalf("chain len %d/%d want %d", len1, len2, depth+1)
			}
			want := 2*depth + 1
			if steps1 != want || steps2 != want {
				t.Fatalf("steps %d/%d want %d (must depend only on depth)", steps1, steps2, want)
			}
		})
	}
}

// TestConcurrentSubleasesOneValidChild 并发转租不得产生两份有效下级。
func TestConcurrentSubleasesOneValidChild(t *testing.T) {
	s := newTestService(t)
	root, _ := s.CreateMaster(0, "LL", "A", 5, 400, 1000)
	const n = 32
	for i := 0; i < n; i++ {
		tn := fmt.Sprintf("W%d", i)
		if err := s.GrantGeneral(0, "LL", tn); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var success, rejected int64
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Sublease(1, root.ID, fmt.Sprintf("W%d", i), 5, 400, 1000)
			mu.Lock()
			if err == nil {
				success++
			} else {
				rejected++
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if success != 1 || rejected != n-1 {
		t.Fatalf("success=%d rejected=%d", success, rejected)
	}
	var children int
	for _, l := range s.allLeases() {
		if l.ParentID == root.ID && !l.Terminated {
			children++
		}
	}
	if children != 1 {
		t.Fatalf("valid children=%d, want 1", children)
	}
}

// TestConcurrentPayNoDoubleSettlement 并发清偿不得使同一笔欠费被清偿两次。
func TestConcurrentPayNoDoubleSettlement(t *testing.T) {
	s := newTestService(t)
	root, _ := s.CreateMaster(0, "LL", "A", 5, 400, 1000)
	mustGrantGeneral(t, s, "B")
	sub, _ := s.Sublease(0, root.ID, "B", 5, 400, 1000)
	due := installments(5, 400, 10, 1000)[0].due
	if err := s.Advance(due + 5); err != nil {
		t.Fatal(err)
	}
	var arrearID int64
	for _, a := range s.allArrears() {
		if a.LeaseID == sub.ID {
			arrearID = a.ID
		}
	}
	const n = 16
	var wg sync.WaitGroup
	var ok, bad int64
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.PayArrear(due+6, arrearID, root.ID, 1000)
			mu.Lock()
			if err == nil {
				ok++
			} else {
				bad++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if ok != 1 || bad != n-1 {
		t.Fatalf("paid=%d rejected=%d", ok, bad)
	}
	a, _ := s.GetArrear(arrearID)
	if a.Paid != a.Amount {
		t.Fatalf("paid=%d amount=%d", a.Paid, a.Amount)
	}
	if rs := s.RecourseParties(arrearID); len(rs) != 1 || rs[0].Amount != 1000 {
		t.Fatalf("recourses=%+v", rs)
	}
}

// TestInvariantPaidEqualsRecourse 不变量：每笔欠费清偿总额 == 追偿总额且不超欠费。
func TestInvariantPaidEqualsRecourse(t *testing.T) {
	s := newTestService(t)
	root, _ := s.CreateMaster(0, "LL", "A", 5, 400, 1000)
	mustGrantGeneral(t, s, "B", "C")
	b, _ := s.Sublease(0, root.ID, "B", 5, 400, 1000)
	c, _ := s.Sublease(0, b.ID, "C", 5, 400, 1000)
	due := installments(5, 400, 10, 1000)[0].due
	if err := s.Advance(due + 5); err != nil {
		t.Fatal(err)
	}
	var aid int64
	for _, a := range s.allArrears() {
		if a.LeaseID == c.ID {
			aid = a.ID
		}
	}
	amounts := []int64{300, 200, 500}
	payers := []int64{c.ID, b.ID, root.ID}
	for i, amt := range amounts {
		if _, err := s.PayArrear(due+6+i, aid, payers[i], amt); err != nil {
			t.Fatal(err)
		}
	}
	var rec int64
	for _, r := range s.RecourseParties(aid) {
		rec += r.Amount
	}
	a, _ := s.GetArrear(aid)
	if a.Paid != 1000 || rec != 1000 || a.Paid > a.Amount {
		t.Fatalf("invariant violated paid=%d recourse=%d amount=%d", a.Paid, rec, a.Amount)
	}
}
