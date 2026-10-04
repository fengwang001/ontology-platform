package adjust

import (
	"fmt"
	"sync"
	"testing"
)

func setupTouchedEngine(t *testing.T, others int) *Engine {
	t.Helper()
	e := New()
	if err := e.SetClose([]byte("S"), 1000); err != nil {
		t.Fatal(err)
	}
	if err := e.SetClose([]byte("OTHER"), 1000); err != nil {
		t.Fatal(err)
	}
	// 目标标的 3 个持仓账户。
	for _, a := range []string{"A", "B", "C"} {
		if err := e.Trade([]byte(a), []byte("S"), 10); err != nil {
			t.Fatal(err)
		}
	}
	// others 个只持有其他标的的账户。
	for i := 0; i < others; i++ {
		a := fmt.Sprintf("other%05d", i)
		if err := e.Trade([]byte(a), []byte("OTHER"), 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Advance(5); err != nil {
		t.Fatal(err)
	}
	if err := e.Announce([]byte("x1"), []byte("S"), 30, 3, 5, 7); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestTouchedIndependentOfOthers(t *testing.T) {
	for _, others := range []int{10, 10000} {
		e := setupTouchedEngine(t, others)
		e.book.ResetTouched()
		if err := e.Advance(7); err != nil {
			t.Fatal(err)
		}
		got := e.book.Touched()
		if got > 3 {
			t.Fatalf("others=%d touched=%d, want <= 3 (snapshot holders of S)", others, got)
		}
		if got != 3 {
			t.Fatalf("others=%d touched=%d, want exactly 3", others, got)
		}
		t.Logf("判定依据: 其他标的账户 %d 个, 快照非零账户 3 个, touched=%d (两者无关)", others, got)
	}
}

func TestConcurrentOps(t *testing.T) {
	e := New()
	if err := e.SetClose([]byte("S"), 1000); err != nil {
		t.Fatal(err)
	}
	if err := e.Advance(1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := []byte(fmt.Sprintf("acct%02d", i))
			_ = e.Deposit(a, 1000)
			_ = e.Trade(a, []byte("S"), 5)
			_ = e.Freeze(a, []byte("S"), 2)
			_ = e.Unfreeze(a, []byte("S"), 1)
			_ = e.PlaceOrder([]byte(fmt.Sprintf("o%02d", i)), a, []byte("S"), Buy, 900, 10)
		}(i)
	}
	wg.Wait()
	// 不变量：每个账户每个标的 0<=f<=q。
	for i := 0; i < 50; i++ {
		a := fmt.Sprintf("acct%02d", i)
		q, f, err := e.book.Position([]byte(a), []byte("S"))
		if err != nil || f < 0 || f > q || q != 5 {
			t.Fatalf("acct %s q=%d f=%d err=%v", a, q, f, err)
		}
	}
	// 推进触发除权，所有送股总和守恒。
	if err := e.Announce([]byte("x1"), []byte("S"), 30, 3, 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := e.Advance(2); err != nil {
		t.Fatal(err)
	}
	res, err := e.Result([]byte("x1"))
	if err != nil {
		t.Fatal(err)
	}
	var totalQ int64
	wantShares := int64(0)
	for i := 0; i < 50; i++ {
		qq, _, _ := e.book.Position([]byte(fmt.Sprintf("acct%02d", i)), []byte("S"))
		totalQ += qq
	}
	var shares int64
	awByAcct := map[string]int64{}
	for _, a := range res.Awards {
		shares += a.Shares
		awByAcct[string(a.Acct)] = a.Shares
	}
	snapQ := int64(0)
	act, _ := e.reg.Get([]byte("x1"))
	for _, s := range act.Snap {
		snapQ += s.Q
		// 每个账户独立取整。
		wantShares += s.Q * 3 / 10
		if s.Q*3/10 != awByAcct[string(s.Acct)] {
			t.Fatalf("acct %s shares mismatch: snap q=%d got=%d", s.Acct, s.Q, awByAcct[string(s.Acct)])
		}
	}
	if shares != wantShares {
		t.Fatalf("bonus shares = %d, want %d", shares, wantShares)
	}
	// 总量上界：送股总数不超过 floor(快照 q 之和×b/10)（逐账户取整只会更少）。
	if shares > snapQ*3/10 {
		t.Fatalf("bonus %d exceeds bound %d (snapQ=%d, postQ=%d)", shares, snapQ*3/10, snapQ, totalQ)
	}
}
