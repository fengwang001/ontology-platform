package tcc_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/ledger"
	"ontology/tcc"
)

// TestCancelTryRace：Try 与 Cancel 同分支并发，两种合法结局恰居其一：
// Try 先成功则 Cancel 释放（fz=0，Cancelled(Cancel)）；Cancel 先则 Try 必 ErrHanging。
func TestCancelTryRace(t *testing.T) {
	for iter := 0; iter < 300; iter++ {
		m := newMgr(t, 100, 10, "a", 100)
		xid, br, a := []byte("x"), []byte("1"), []byte("a")
		var wg sync.WaitGroup
		var tryErr, cancelErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; tryErr = m.Try(xid, br, a, 30, 0) }()
		go func() { defer wg.Done(); <-start; cancelErr = m.Cancel(xid, br, 0) }()
		close(start)
		wg.Wait()
		t.Logf("iter=%d Try=%v Cancel=%v", iter, tryErr, cancelErr)
		fz := m.Ledger().Frozen(a)
		rec, ok := m.Get(xid, br)
		switch {
		case tryErr == nil && cancelErr == nil:
			// Try 先：Cancel 必已释放，记录为显式取消。
			if fz != 0 || !ok || rec.State != tcc.StateCancelled || rec.Reason != tcc.CancelExplicit {
				t.Fatalf("Try 先结局异常 fz=%d rec=%+v", fz, rec)
			}
		case errors.Is(tryErr, ledger.ErrHanging) && cancelErr == nil:
			// Cancel 先：空回滚标记拦截 Try，从未冻结。
			if fz != 0 || !ok || rec.Reason != tcc.CancelEmpty {
				t.Fatalf("Cancel 先结局异常 fz=%d rec=%+v", fz, rec)
			}
		default:
			t.Fatalf("非法竞争结局 try=%v cancel=%v", tryErr, cancelErr)
		}
		if m.Ledger().Balance(a) != 100 {
			t.Fatalf("竞争期间余额不得变化 bal=%d", m.Ledger().Balance(a))
		}
	}
}

// TestHeapExaminedBound：10000 个无关 Tried，一次到期操作只考察“到期数+1”。
func TestHeapExaminedBound(t *testing.T) {
	fill := func(t *testing.T) *tcc.Manager {
		lg := ledger.New()
		if err := lg.Deposit([]byte("a"), tcc.MaxAmount); err != nil {
			t.Fatal(err)
		}
		m, err := tcc.New(lg, 10, 100000)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 10000; i++ {
			if err := m.Try([]byte("x"), []byte("k"+itoa(i)), []byte("a"), 1, 0); err != nil {
				t.Fatal(err)
			}
		}
		return m
	}
	// 档1：10000 个无关分支全部恰在 now=10 到期，考察=10000（堆弹空）。
	m1 := fill(t)
	if _, err := m1.Avail([]byte("a"), 10); err != nil {
		t.Fatal(err)
	}
	t.Logf("档1：10000 个分支同刻到期，Avail(a,10) 考察堆项=%d", m1.Examined())
	if m1.Examined() != 10000 {
		t.Fatalf("examined=%d want 10000", m1.Examined())
	}
	// 档2：同样 10000 个，now=9 无到期，只考察堆顶 1 项。
	m2 := fill(t)
	if _, err := m2.Avail([]byte("a"), 9); err != nil {
		t.Fatal(err)
	}
	t.Logf("档2：10000 个无关分支、无到期，Avail(a,9) 考察堆项=%d", m2.Examined())
	if m2.Examined() != 1 {
		t.Fatalf("examined=%d want 1", m2.Examined())
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [16]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}
