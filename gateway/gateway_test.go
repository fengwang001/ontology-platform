package gateway_test

import (
	"errors"
	"testing"

	"ontology/gateway"
	"ontology/quota"
)

func mustTenant(t *testing.T, g *gateway.Gateway, name string, rate, burst int64, prio int) {
	t.Helper()
	if err := g.AddTenant(name, rate, burst, prio); err != nil {
		t.Fatalf("AddTenant(%s): %v", name, err)
	}
}

func mustState(t *testing.T, g *gateway.Gateway, name string, tokens, lastRefill int64) {
	t.Helper()
	tok, lr, ok := g.TenantState(name)
	if !ok || tok != tokens || lr != lastRefill {
		t.Fatalf("state(%s)=(%d,%d,%v) want (%d,%d,true)", name, tok, lr, ok, tokens, lastRefill)
	}
}

func mustIngest(t *testing.T, g *gateway.Gateway, now int64, name string, prio int, size int64) {
	t.Helper()
	if _, err := g.Ingest(now, name, prio, size); err != nil {
		t.Fatalf("Ingest(%d,%s,%d,%d): %v", now, name, prio, size, err)
	}
}

func TestSpecExample(t *testing.T) {
	g, _ := gateway.New(100)
	mustTenant(t, g, "A", 10, 100, 1)
	mustTenant(t, g, "B", 10, 100, 0)

	r1, err := g.Ingest(0, "A", 2, 60)
	t.Logf("Ingest(0,A,2,60) -> seq=%d err=%v; A 余额 100000-60000=40000", r1.Seq, err)
	mustState(t, g, "A", 40000, 0)

	r2, err := g.Ingest(0, "B", 1, 30)
	t.Logf("Ingest(0,B,1,30) -> seq=%d err=%v; B 余额 70000", r2.Seq, err)
	mustState(t, g, "B", 70000, 0)

	r3, err := g.Ingest(0, "B", 0, 30)
	t.Logf("Ingest(0,B,0,30) -> evicted=%v err=%v; need=20 先挤 prio2 的 A/60", r3.Evicted, err)
	if err != nil || len(r3.Evicted) != 1 || r3.Evicted[0].Tenant != "A" || r3.Evicted[0].Size != 60 {
		t.Fatalf("evicted=%v err=%v", r3.Evicted, err)
	}
	mustState(t, g, "A", 100000, 0) // 40000+60000 恰到封顶
	mustState(t, g, "B", 40000, 0)
	if g.Used() != 60 {
		t.Fatalf("used=%d want 60", g.Used())
	}

	d, err := g.Drain(1, 40)
	t.Logf("Drain(1,40) -> %v err=%v; 出 prio0/30 后剩 10 放不下 prio1/30", d, err)
	if len(d) != 1 || d[0].Seq != r3.Seq {
		t.Fatalf("drained=%v", d)
	}

	_, err = g.Ingest(2, "A", 2, 80)
	t.Logf("Ingest(2,A,2,80) -> err=%v; need=10 可挤出=0", err)
	if !errors.Is(err, gateway.ErrQueueFull) {
		t.Fatalf("err=%v want ErrQueueFull", err)
	}
	mustState(t, g, "A", 100000, 0) // 队列已满零改动

	if _, err = g.Ingest(2, "A", 0, 10); !errors.Is(err, gateway.ErrPrioNotAllowed) {
		t.Fatalf("err=%v want ErrPrioNotAllowed (不降级)", err)
	}
	if _, err = g.Ingest(2, "B", 1, 50); !errors.Is(err, gateway.ErrQuotaExceeded) {
		t.Fatalf("err=%v want ErrQuotaExceeded (40020<50000)", err)
	}
	mustState(t, g, "B", 40000, 0) // 配额不足不落盘

	if _, err = g.Ingest(1000, "B", 1, 50); err != nil {
		t.Fatalf("Ingest(1000,B,1,50) err=%v (40000+10*1000=50000 恰等)", err)
	}
	st := g.Stats()
	if a := st["A"]; a.AcceptedBytes != [3]int64{0, 0, 60} ||
		a.EvictedBytes != [3]int64{0, 0, 60} || a.QueuedBytes != [3]int64{} {
		t.Fatalf("stats A=%+v", a)
	}
	if b := st["B"]; b.AcceptedBytes != [3]int64{30, 80, 0} ||
		b.DrainedBytes != [3]int64{30, 0, 0} || b.QueuedBytes != [3]int64{0, 80, 0} {
		t.Fatalf("stats B=%+v", b)
	}
}

func TestQuotaExactAndOffByOne(t *testing.T) {
	g, _ := gateway.New(1000)
	mustTenant(t, g, "T", 0, 50, 0)
	mustIngest(t, g, 0, "T", 0, 50) // 恰等通过
	if _, err := g.Ingest(0, "T", 0, 1); !errors.Is(err, gateway.ErrQuotaExceeded) {
		t.Fatalf("off-by-one err=%v want ErrQuotaExceeded", err)
	}
}

func TestQuotaCheckedBeforeQueue(t *testing.T) {
	g, _ := gateway.New(10)
	mustTenant(t, g, "T", 0, 5, 0)
	mustTenant(t, g, "U", 0, 100, 0)
	mustIngest(t, g, 0, "U", 2, 10) // 填满队列
	// 队列已满且配额不足：先报配额。
	if _, err := g.Ingest(0, "T", 0, 10); !errors.Is(err, gateway.ErrQuotaExceeded) {
		t.Fatalf("err=%v want ErrQuotaExceeded", err)
	}
}

func TestRefundCappedAtBurst(t *testing.T) {
	g, _ := gateway.New(100)
	mustTenant(t, g, "T", 10, 100, 0)
	mustTenant(t, g, "U", 0, 100, 0)
	mustIngest(t, g, 0, "T", 2, 60)
	r, err := g.Ingest(1000, "U", 0, 60) // need=20, 挤 T 的 60
	if err != nil || len(r.Evicted) != 1 {
		t.Fatalf("evicted=%v err=%v", r.Evicted, err)
	}
	// T: 40000 + 10*1000 补充 + 60000 退款 = 110000 -> 封顶 100000
	mustState(t, g, "T", 100000, 1000)
}

func TestSameTenantEvictionNotCountedForQuota(t *testing.T) {
	g, _ := gateway.New(100)
	mustTenant(t, g, "T", 0, 100, 0)
	mustIngest(t, g, 0, "T", 2, 60)
	// 虚拟余额 40000 < 60000：即使挤掉自己的 60 能退款也判配额不足。
	if _, err := g.Ingest(0, "T", 0, 60); !errors.Is(err, gateway.ErrQuotaExceeded) {
		t.Fatalf("err=%v want ErrQuotaExceeded", err)
	}
	mustState(t, g, "T", 40000, 0)

	// 配额恰等时挤自己的记录可以成功，退款发生在判定之后。
	g2, _ := gateway.New(100)
	mustTenant(t, g2, "T", 0, 200, 0)
	mustIngest(t, g2, 0, "T", 2, 60)
	mustIngest(t, g2, 0, "T", 2, 40)
	r, err := g2.Ingest(0, "T", 0, 100) // 虚拟余额 100000 恰等；need=100 全挤自己
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(r.Evicted) != 2 || r.Evicted[0].Size != 40 || r.Evicted[1].Size != 60 {
		t.Fatalf("evicted=%v want [40 60] 新到者先", r.Evicted)
	}
	mustState(t, g2, "T", 100000, 0) // 100000+40000+60000-100000
}

func TestClockAndRejectedOpsKeepState(t *testing.T) {
	g, _ := gateway.New(10)
	mustTenant(t, g, "T", 0, 100, 0)
	mustIngest(t, g, 5, "T", 0, 1)
	if _, err := g.Ingest(4, "T", 0, 1); !errors.Is(err, gateway.ErrClockBackward) {
		t.Fatalf("err=%v want ErrClockBackward", err)
	}
	if _, err := g.Ingest(10, "X", 0, 1); !errors.Is(err, gateway.ErrUnknownTenant) {
		t.Fatalf("err=%v want ErrUnknownTenant", err)
	}
	// 被拒绝的 now=10 不推进时钟：now=6 仍合法。
	mustIngest(t, g, 6, "T", 0, 1)
	if _, err := g.Drain(3, 1); !errors.Is(err, gateway.ErrClockBackward) {
		t.Fatalf("drain err=%v want ErrClockBackward", err)
	}
}

func TestAddTenantErrors(t *testing.T) {
	g, _ := gateway.New(10)
	bad := []struct {
		name        string
		rate, burst int64
		prio        int
	}{
		{"", 1, 1, 0}, {string(make([]byte, 65)), 1, 1, 0},
		{"x", -1, 1, 0}, {"x", 1_000_000_001, 1, 0},
		{"x", 1, 0, 0}, {"x", 1, 1_000_000_001, 0},
		{"x", 1, 1, -1}, {"x", 1, 1, 3},
	}
	for _, c := range bad {
		if err := g.AddTenant(c.name, c.rate, c.burst, c.prio); !errors.Is(err, quota.ErrInvalidParam) {
			t.Fatalf("AddTenant(%q,...) err=%v want ErrInvalidParam", c.name, err)
		}
	}
	mustTenant(t, g, "dup", 1, 1, 0)
	if err := g.AddTenant("dup", 1, 1, 0); !errors.Is(err, quota.ErrDuplicateName) {
		t.Fatalf("err=%v want ErrDuplicateName", err)
	}
	g2, _ := gateway.New(10)
	for i := 0; i < 1000; i++ {
		if err := g2.AddTenant(string(rune(i+1))+string(rune(i/256)), 1, 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := g2.AddTenant("overflow", 1, 1, 0); !errors.Is(err, quota.ErrTooManyTenants) {
		t.Fatalf("err=%v want ErrTooManyTenants", err)
	}
}
