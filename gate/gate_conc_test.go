package gate

import (
	"sync"
	"testing"
)

func TestConcurrentEquivalence(t *testing.T) {
	g := New()
	must(g.Register(0, b("C0"), b("H")))
	g.SetLimit(0, b("X"), 1_000_000_000, 1_000_000_000, 1_000_000_000)

	// 串行构造基线：每个账户完成一批开仓+成交+撤单，记录最终敞口。
	runSerial := func(nAcct int) int64 {
		gg := New()
		must(gg.Register(0, b("C0"), b("H")))
		gg.SetLimit(0, b("X"), 1_000_000_000, 1_000_000_000, 1_000_000_000)
		var expected int64
		for a := 0; a < nAcct; a++ {
			acct := "C" + itoa(a+1)
			if err := gg.Register(int64(a+1), b(acct), b("H")); err != nil {
				t.Fatalf("serial register %s: %v", acct, err)
			}
		}
		for a := 0; a < nAcct; a++ {
			acct := "C" + itoa(a+1)
			oid := "k" + itoa(a)
			if err := gg.Order(1000+int64(a), b(oid), b(acct), b("X"), Long, Open, 10); err != nil {
				t.Fatalf("serial order %s: %v", oid, err)
			}
		}
		for a := 0; a < nAcct; a++ {
			oid := "k" + itoa(a)
			if err := gg.Fill(3000+int64(a), b(oid), 4); err != nil { // 持仓4，在途6
				t.Fatalf("serial fill %s: %v", oid, err)
			}
			expected += 10 // 已成交+在途均计入敞口
		}
		return expected
	}

	const nAcct = 200
	expected := runSerial(nAcct)

	var wg sync.WaitGroup
	for a := 0; a < nAcct; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			acct := "C" + itoa(a+1)
			oid := "k" + itoa(a)
			if err := g.Register(5000, b(acct), b("H")); err != nil {
				t.Errorf("register %s: %v", acct, err)
				return
			}
			if err := g.Order(5000, b(oid), b(acct), b("X"), Long, Open, 10); err != nil {
				t.Errorf("order %s: %v", oid, err)
				return
			}
			if err := g.Fill(5000, b(oid), 4); err != nil {
				t.Errorf("fill %s: %v", oid, err)
			}
		}(a)
	}
	wg.Wait()

	// 全部账户并发跑后，组敞口必须等于串行基线（每个账户 e=10，套保 0）。
	if ge := g.GroupExposure(b("H"), b("X")); ge != expected {
		t.Fatalf("concurrent group exposure=%d want %d", ge, expected)
	}
	// 不变量：在途量之和等于未终结委托未成交量之和；持仓非负。
	openSum := int64(0)
	remainSum := int64(0)
	for _, e := range g.book.AllRecs() {
		openSum += e.Rec.Open[0] + e.Rec.Open[1]
		if e.Rec.Pos[0] < 0 || e.Rec.Pos[1] < 0 {
			t.Fatalf("negative position: %+v", e.Rec)
		}
	}
	for _, o := range g.ords {
		if !o.terminated() && o.Offset == Open {
			remainSum += o.Remain
		}
	}
	if openSum != remainSum {
		t.Fatalf("pending open=%d != sum remain of live open orders=%d", openSum, remainSum)
	}
}
