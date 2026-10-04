package pool

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/loan"
)

// TestRejectionOrder：参数非法 > 时钟回退 > 不存在 > 超量/不足，只报第一个。
func TestRejectionOrder(t *testing.T) {
	p := mustNew(t, 10, 500)
	S := []byte("S")
	if err := p.SetPrice(5, S, 10); err != nil {
		t.Fatal(err)
	}
	if err := p.Lend(1, nil, S, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v want ErrInvalid", err)
	}
	if err := p.Lend(4, []byte("L"), []byte("NOPE"), 10); !errors.Is(err, ErrClockBack) {
		t.Fatalf("got %v want ErrClockBack", err)
	}
	if _, err := p.Borrow(6, []byte("B"), []byte("NOPE"), 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
	if err := p.SetPrice(6, []byte("T"), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Borrow(7, []byte("B"), []byte("T"), 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
	if err := p.Lend(7, []byte("L"), []byte("T"), 5); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Borrow(8, []byte("B"), []byte("T"), 6); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("got %v want ErrInsufficient", err)
	}
	if err := p.Withdraw(8, []byte("X"), []byte("T"), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
	if err := p.Return(8, []byte("B"), []byte("T"), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
	if err := p.Withdraw(9, []byte("L"), []byte("T"), 6); !errors.Is(err, ErrOverQty) {
		t.Fatalf("got %v want ErrOverQty", err)
	}
	if _, err := p.Borrow(9, []byte("B"), []byte("T"), 3); err != nil {
		t.Fatal(err)
	}
	if err := p.Return(10, []byte("B"), []byte("T"), 4); !errors.Is(err, ErrOverQty) {
		t.Fatalf("got %v want ErrOverQty", err)
	}
}

// TestSettlementSurvivesRejection：拒绝不回滚结算与时钟；参数/时钟拒绝不改状态。
func TestSettlementSurvivesRejection(t *testing.T) {
	p := scenarioWithRecall(t)
	err := p.Lend(15, []byte("L9"), []byte("UNPRICED"), 1)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
	if p.Now() != 15 {
		t.Fatalf("now=%d, want 15", p.Now())
	}
	if len(p.Buyins()) != 1 || p.Buyins()[0].ContractID != 5 {
		t.Fatalf("settlement lost after rejection: %v", p.Buyins())
	}
	before := len(p.Buyins())
	if err := p.SetPrice(15, bsSym, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v want ErrInvalid", err)
	}
	if err := p.Lend(14, []byte("L"), bsSym, 1); !errors.Is(err, ErrClockBack) {
		t.Fatalf("got %v want ErrClockBack", err)
	}
	if p.Now() != 15 || len(p.Buyins()) != before {
		t.Fatalf("state changed by invalid/clock rejection")
	}
}

// TestReplaceBeforeRecall：同一原合约先出替换合约号，后出召回合约号。
func TestReplaceBeforeRecall(t *testing.T) {
	p := mustNew(t, 100, 0)
	S := []byte("S")
	if err := p.SetPrice(1, S, 10); err != nil {
		t.Fatal(err)
	}
	if err := p.Lend(1, []byte("A"), S, 100); err != nil {
		t.Fatal(err)
	}
	if err := p.Lend(2, []byte("B"), S, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Borrow(3, []byte("X"), S, 100); err != nil {
		t.Fatal(err)
	}
	if err := p.Withdraw(4, []byte("A"), S, 80); err != nil {
		t.Fatal(err)
	}
	rep, ok1 := p.Contract(2)
	rec, ok2 := p.Contract(3)
	if !ok1 || rep.Lender != "B" || rep.Qty != 30 {
		t.Fatalf("replacement contract=%+v ok=%v", rep, ok1)
	}
	if !ok2 || rec.Kind != loan.Recalled || rec.Qty != 50 || rec.Dl != 104 {
		t.Fatalf("recall contract=%+v ok=%v", rec, ok2)
	}
	if cqty(p, 1) != 20 {
		t.Fatalf("orig remaining=%d want 20", cqty(p, 1))
	}
}

// TestReplaceNotFromSelf：撤回人自己的空闲不用于替换，只作为立即交还。
func TestReplaceNotFromSelf(t *testing.T) {
	p := mustNew(t, 100, 0)
	S := []byte("S")
	if err := p.SetPrice(1, S, 10); err != nil {
		t.Fatal(err)
	}
	if err := p.Lend(1, []byte("A"), S, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Borrow(2, []byte("X"), S, 60); err != nil {
		t.Fatal(err)
	}
	if err := p.Withdraw(3, []byte("A"), S, 100); err != nil {
		t.Fatal(err)
	}
	if idle := idleOf(p, "S", "A"); idle != 0 {
		t.Fatalf("A idle=%d want 0", idle)
	}
	rec, ok := p.Contract(2)
	if !ok || rec.Kind != loan.Recalled || rec.Qty != 60 {
		t.Fatalf("recall=%+v ok=%v", rec, ok)
	}
	if cqty(p, 1) != 0 {
		t.Fatalf("c1 should be gone, qty=%d", cqty(p, 1))
	}
}

// TestDescendingContracts：撤回按合约号降序逐笔处理。
func TestDescendingContracts(t *testing.T) {
	p := mustNew(t, 100, 0)
	S := []byte("S")
	if err := p.SetPrice(1, S, 10); err != nil {
		t.Fatal(err)
	}
	if err := p.Lend(1, []byte("A"), S, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Borrow(2, []byte("X"), S, 30); err != nil {
		t.Fatal(err)
	}
	if err := p.Lend(3, []byte("A"), S, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Borrow(4, []byte("Y"), S, 30); err != nil {
		t.Fatal(err)
	}
	if err := p.Lend(5, []byte("A"), S, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Borrow(6, []byte("Z"), S, 30); err != nil {
		t.Fatal(err)
	}
	if err := p.Lend(7, []byte("B"), S, 50); err != nil {
		t.Fatal(err)
	}
	if err := p.Withdraw(8, []byte("A"), S, 60); err != nil {
		t.Fatal(err)
	}
	if cqty(p, 1) != 30 {
		t.Fatalf("c1=%d want 30 (untouched)", cqty(p, 1))
	}
	if cqty(p, 2) != 0 {
		t.Fatalf("c2=%d want 0 (20 replaced + 10 recalled)", cqty(p, 2))
	}
	if cqty(p, 3) != 0 {
		t.Fatalf("c3=%d want 0 (fully replaced)", cqty(p, 3))
	}
	c4, _ := p.Contract(4)
	c5, _ := p.Contract(5)
	c6, _ := p.Contract(6)
	if c4.Borrower != "Z" || c4.Qty != 30 || c5.Borrower != "Y" || c5.Qty != 20 {
		t.Fatalf("replacements c4=%+v c5=%+v", c4, c5)
	}
	if c6.Kind != loan.Recalled || c6.Qty != 10 || c6.Borrower != "Y" {
		t.Fatalf("recall c6=%+v", c6)
	}
}

// TestNoAutoReplacementAfterNewLend：召回只在 Withdraw 当时替换一次。
func TestNoAutoReplacementAfterNewLend(t *testing.T) {
	p := scenarioWithRecall(t)
	if err := p.Return(8, []byte("B1"), bsSym, 250); err != nil {
		t.Fatal(err)
	}
	if err := p.Lend(10, []byte("L9"), bsSym, 500); err != nil {
		t.Fatal(err)
	}
	if cqty(p, 5) != 50 {
		t.Fatalf("c5=%d want 50", cqty(p, 5))
	}
	if err := p.SetPrice(15, bsSym, 21); err != nil {
		t.Fatal(err)
	}
	if len(p.Buyins()) != 1 || p.Buyins()[0].ContractID != 5 {
		t.Fatalf("buyins=%v", p.Buyins())
	}
}

// TestReturnRecallFirst：还券先召回（按 dl、id）后普通（按 id）。
func TestReturnRecallFirst(t *testing.T) {
	p := mustNew(t, 100, 0)
	S := []byte("S")
	if err := p.SetPrice(1, S, 10); err != nil {
		t.Fatal(err)
	}
	for _, a := range []func() error{
		func() error { return p.Lend(1, []byte("A"), S, 100) },
		func() error { _, e := p.Borrow(2, []byte("X"), S, 100); return e },
		func() error { return p.Withdraw(3, []byte("A"), S, 30) },
		func() error { return p.Lend(4, []byte("B"), S, 100) },
		func() error { _, e := p.Borrow(5, []byte("X"), S, 40); return e },
	} {
		if err := a(); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Return(6, []byte("X"), S, 50); err != nil {
		t.Fatal(err)
	}
	if cqty(p, 2) != 0 {
		t.Fatalf("recall c2=%d want 0", cqty(p, 2))
	}
	if cqty(p, 1) != 50 {
		t.Fatalf("ordinary c1=%d want 50 (recall first)", cqty(p, 1))
	}
	if cqty(p, 3) != 40 {
		t.Fatalf("ordinary c3=%d want 40 (untouched)", cqty(p, 3))
	}
	if idle := idleOf(p, "S", "A"); idle != 20 {
		t.Fatalf("A idle=%d want 20", idle)
	}
}

// TestConcurrentLinearizable：并发调用不 panic 且不变量恒成立。
func TestConcurrentLinearizable(t *testing.T) {
	p := mustNew(t, 100, 0)
	S := []byte("S")
	if err := p.SetPrice(0, S, 10); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l := []byte("L" + itoa(i))
			b := []byte("B" + itoa(i))
			_ = p.Lend(int64(i), l, S, 50)
			_, _ = p.Borrow(int64(100+i), b, S, 10)
			_ = p.Return(int64(200+i), b, S, 10)
		}(i)
	}
	wg.Wait()
	if err := checkInvariant(p, "S"); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// checkInvariant 校验：累计 Lend = 空闲之和 + 未结合约量 + 累计交还。
func checkInvariant(p *Pool, sym string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := loan.ID(sym)
	var idleSum int64
	for _, lid := range p.book.Lenders(key) {
		idleSum += p.book.Idle(key, lid)
	}
	lent := p.book.TotalLend(key)
	active := p.book.ActiveTotal(key)
	handed := p.book.HandedBack(key)
	if lent != idleSum+active+handed {
		return fmt.Errorf("invariant %s: lent=%d idle=%d active=%d handed=%d",
			sym, lent, idleSum, active, handed)
	}
	// 欠券等于未结合约量之和（按借入人汇总）。
	debt := map[loan.ID]int64{}
	for _, id := range allContractIDs(p) {
		c, ok := p.book.Get(id)
		if !ok || c.Symbol != key {
			continue
		}
		debt[c.Borrower] += c.Qty
	}
	for _, bkey := range borrowersSeen(p, key) {
		want := debt[bkey]
		if got := p.book.Debt(key, bkey); got != want {
			return fmt.Errorf("debt mismatch borrower=%s book=%d sum=%d", bkey, got, want)
		}
	}
	// 每笔买入时刻不早于其 dl（买入时刻即 dl）。
	for _, b := range p.sched.Buyins() {
		c, ok := p.book.Get(b.ContractID)
		if ok && b.At < c.Dl {
			return fmt.Errorf("buyin at=%d before dl=%d", b.At, c.Dl)
		}
		if b.At < 0 {
			return fmt.Errorf("buyin at negative")
		}
	}
	return nil
}

func allContractIDs(p *Pool) []int64 {
	var ids []int64
	for id := int64(1); id <= p.nextID; id++ {
		ids = append(ids, id)
	}
	return ids
}

func borrowersSeen(p *Pool, sym loan.ID) []loan.ID {
	seen := map[loan.ID]bool{}
	for id := int64(1); id <= p.nextID; id++ {
		if c, ok := p.book.Get(id); ok && c.Symbol == sym {
			seen[c.Borrower] = true
		}
	}
	var out []loan.ID
	for k := range seen {
		out = append(out, k)
	}
	return out
}

// TestScannedBounded：scanned ≤ 出券人数+1，且与空闲为 0 的出借人数无关（10/10000 两档）。
func TestScannedBounded(t *testing.T) {
	for _, zeros := range []int{10, 10_000} {
		p := mustNew(t, 100, 0)
		S := []byte("S")
		if err := p.SetPrice(1, S, 10); err != nil {
			t.Fatal(err)
		}
		// 大量出借人各 Lend 1 并被借空 -> 空闲 0；时钟严格不减。
		for i := 0; i < zeros; i++ {
			name := fmt.Sprintf("Z%d", i)
			t2 := int64(2 + 2*i)
			if err := p.Lend(t2, []byte(name), S, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Borrow(t2+1, []byte("sink"), S, 1); err != nil {
				t.Fatal(err)
			}
		}
		// 三个有空闲的出借人，登记序号在后；需求跨其中两人。
		for _, name := range []string{"A1", "A2", "A3"} {
			if err := p.Lend(int64(2+2*zeros), []byte(name), S, 10); err != nil {
				t.Fatal(err)
			}
		}
		tBorrow := int64(3 + 2*zeros)
		ids, err := p.Borrow(tBorrow, []byte("taker"), S, 15)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 2 {
			t.Fatalf("zeros=%d lenders=%d want 2", zeros, len(ids))
		}
		p.mu.Lock()
		scanned := p.scanned
		p.mu.Unlock()
		if scanned > len(ids)+1 {
			t.Fatalf("zeros=%d scanned=%d > lenders+1=%d", zeros, scanned, len(ids)+1)
		}
		if scanned != len(ids) {
			t.Fatalf("zeros=%d scanned=%d want exactly lenders=%d", zeros, scanned, len(ids))
		}
		if err := checkInvariant(p, "S"); err != nil {
			t.Fatal(err)
		}
	}
}

// TestReplayDeterminism：相同操作序列重放得到相同合约与买入清单。
func TestReplayDeterminism(t *testing.T) {
	build := func() (*Pool, []int64, []Buyin) {
		p := mustNew(t, 10, 500)
		S := []byte("S")
		_ = p.SetPrice(1, S, 21)
		_ = p.Lend(1, []byte("L1"), S, 500)
		_ = p.Lend(2, []byte("L2"), S, 300)
		ids1, _ := p.Borrow(3, []byte("B1"), S, 600)
		_, _ = p.Borrow(4, []byte("B2"), S, 100)
		_ = p.Withdraw(5, []byte("L1"), S, 400)
		_ = p.Return(8, []byte("B1"), S, 250)
		_ = p.Lend(15, []byte("L2"), S, 1)
		return p, ids1, p.Buyins()
	}
	p1, idsA, bsA := build()
	p2, idsB, bsB := build()
	if fmt.Sprint(idsA) != fmt.Sprint(idsB) {
		t.Fatalf("contract ids differ %v vs %v", idsA, idsB)
	}
	if fmt.Sprint(bsA) != fmt.Sprint(bsB) {
		t.Fatalf("buyins differ %v vs %v", bsA, bsB)
	}
	for id := int64(1); id <= p1.nextID; id++ {
		c1, ok1 := p1.Contract(id)
		c2, ok2 := p2.Contract(id)
		if ok1 != ok2 || c1 != c2 {
			t.Fatalf("contract %d differs: %+v(%v) vs %+v(%v)", id, c1, ok1, c2, ok2)
		}
	}
}
