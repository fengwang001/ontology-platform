package certcache

import (
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(nsec int) time.Time { return epoch.Add(time.Duration(nsec) * time.Second) }

func newTestCache(t *testing.T, capacity int, now func() time.Time) *Cache {
	t.Helper()
	c := NewWithClock(capacity, now)
	c.SetLogOutput(os.Stdout)
	return c
}

func mustAccept(t *testing.T, c *Cache, r Response) Record {
	t.Helper()
	res := c.Submit(r)
	if !res.Accepted {
		t.Fatalf("响应被意外拒绝: %+v reason=%s", r, res.Reason)
	}
	return res.Record
}

func mustReject(t *testing.T, c *Cache, r Response, want RejectReason) {
	t.Helper()
	before := c.Snapshot()
	res := c.Submit(r)
	if res.Accepted {
		t.Fatalf("响应被意外接受: %+v", r)
	}
	if res.Reason != want {
		t.Fatalf("拒绝原因不符: got=%s want=%s", res.Reason, want)
	}
	after := c.Snapshot()
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("被拒绝的响应改变了记录:\nbefore=%v\nafter =%v", before, after)
	}
}

// 1. 先到较新正常，后到较旧吊销：吊销终态不得因新旧而失效。
func TestNewerGoodThenOlderRevoked(t *testing.T) {
	now := at(1000)
	c := newTestCache(t, 10, func() time.Time { return now })

	mustAccept(t, c, Response{Serial: "cert-1", Status: StatusGood, A: at(200), B: at(300)})
	mustAccept(t, c, Response{Serial: "cert-1", Status: StatusRevoked, A: at(100), B: at(150)})

	recs := c.Snapshot()
	if len(recs) != 1 {
		t.Fatalf("应只有一条记录, got %d", len(recs))
	}
	rec := recs[0]
	if rec.Status != StatusRevoked || !rec.A.Equal(at(100)) || !rec.B.Equal(at(150)) {
		t.Fatalf("吊销应覆盖较新正常并保留自身 a/b, got %+v", rec)
	}
	v := c.Trust("cert-1")
	if v.Trusted || v.Reason != TrustRevoked {
		t.Fatalf("应报已吊销, got %+v", v)
	}
}

// 2. 先到吊销，后到更新的正常：正常/暂扣响应被忽略，证书永不恢复信任。
func TestRevokedThenNewerGood(t *testing.T) {
	now := at(1000)
	c := newTestCache(t, 10, func() time.Time { return now })

	mustAccept(t, c, Response{Serial: "cert-2", Status: StatusRevoked, A: at(100), B: at(150)})
	mustAccept(t, c, Response{Serial: "cert-2", Status: StatusGood, A: at(500), B: at(600)})
	mustAccept(t, c, Response{Serial: "cert-2", Status: StatusSuspended, A: at(700), B: at(800)})

	rec := c.Snapshot()[0]
	if rec.Status != StatusRevoked || !rec.A.Equal(at(100)) {
		t.Fatalf("吊销后正常/暂扣均应被忽略, got %+v", rec)
	}
	mustAccept(t, c, Response{Serial: "cert-2", Status: StatusRevoked, A: at(300), B: at(320)})
	rec = c.Snapshot()[0]
	if !rec.A.Equal(at(100)) || !rec.B.Equal(at(150)) {
		t.Fatalf("较晚吊销不得改变记录, got %+v", rec)
	}
	mustAccept(t, c, Response{Serial: "cert-2", Status: StatusRevoked, A: at(50), B: at(60)})
	rec = c.Snapshot()[0]
	if !rec.A.Equal(at(50)) || !rec.B.Equal(at(60)) {
		t.Fatalf("更早吊销应把生效时刻改小并取其 b, got %+v", rec)
	}
	if v := c.Trust("cert-2"); v.Trusted || v.Reason != TrustRevoked {
		t.Fatalf("应报已吊销, got %+v", v)
	}
}

// 3. 暂扣被更新的正常解除。
func TestSuspendedClearedByNewerGood(t *testing.T) {
	now := at(1000)
	c := newTestCache(t, 10, func() time.Time { return now })

	mustAccept(t, c, Response{Serial: "cert-3", Status: StatusSuspended, A: at(100), B: at(900)})
	if v := c.Trust("cert-3"); v.Trusted || v.Reason != TrustSuspended {
		t.Fatalf("应报已暂扣, got %+v", v)
	}
	mustAccept(t, c, Response{Serial: "cert-3", Status: StatusGood, A: at(200), B: at(1200)})
	v := c.Trust("cert-3")
	if !v.Trusted || v.Reason != TrustGood {
		t.Fatalf("更新的正常应解除暂扣, got %+v", v)
	}
}

// 4. a 相等时暂扣优先于正常；状态与 a 均相同取较小 b。
func TestEqualASuspendedWins(t *testing.T) {
	now := at(1000)

	mk := func() *Cache { return newTestCache(t, 10, func() time.Time { return now }) }

	c := mk()
	mustAccept(t, c, Response{Serial: "cert-4", Status: StatusGood, A: at(100), B: at(500)})
	mustAccept(t, c, Response{Serial: "cert-4", Status: StatusSuspended, A: at(100), B: at(500)})
	if rec := c.Snapshot()[0]; rec.Status != StatusSuspended {
		t.Fatalf("a 相等时暂扣应胜正常(正常先到), got %+v", rec)
	}

	c2 := mk()
	mustAccept(t, c2, Response{Serial: "cert-4", Status: StatusSuspended, A: at(100), B: at(500)})
	mustAccept(t, c2, Response{Serial: "cert-4", Status: StatusGood, A: at(100), B: at(500)})
	if rec := c2.Snapshot()[0]; rec.Status != StatusSuspended {
		t.Fatalf("a 相等时暂扣应胜正常(暂扣先到), got %+v", rec)
	}

	mustAccept(t, c, Response{Serial: "cert-4", Status: StatusSuspended, A: at(100), B: at(400)})
	if rec := c.Snapshot()[0]; !rec.B.Equal(at(400)) {
		t.Fatalf("状态与a相同应取较小b, got %+v", rec)
	}
}

// 5. 恰在 b 时刻即过期：有效区间为 [a,b)。
func TestExpiryExactlyAtB(t *testing.T) {
	now := at(300)
	c := newTestCache(t, 10, func() time.Time { return now })
	mustAccept(t, c, Response{Serial: "cert-5", Status: StatusGood, A: at(100), B: at(500)})

	now = at(499)
	if v := c.Trust("cert-5"); !v.Trusted || v.Reason != TrustGood {
		t.Fatalf("b 前一刻应可信, got %+v", v)
	}
	now = at(500)
	if v := c.Trust("cert-5"); v.Trusted || v.Reason != TrustExpired {
		t.Fatalf("恰在 b 时刻应报状态已过期, got %+v", v)
	}

	if v := c.Trust("missing"); v.Trusted || v.Reason != TrustUnknown {
		t.Fatalf("无记录应报未知, got %+v", v)
	}
}

// 6. 容量满：只淘汰已过期正常记录（b 最小、并列序号小）；吊销与暂扣不被淘汰。
func TestEvictionPolicy(t *testing.T) {
	now := at(1000)

	c := newTestCache(t, 2, func() time.Time { return now })
	mustAccept(t, c, Response{Serial: "revoked", Status: StatusRevoked, A: at(10), B: at(20)})
	mustAccept(t, c, Response{Serial: "suspended", Status: StatusSuspended, A: at(10), B: at(2000)})
	mustReject(t, c, Response{Serial: "new", Status: StatusGood, A: at(10), B: at(2000)}, RejectCacheFull)

	c = newTestCache(t, 2, func() time.Time { return now })
	mustAccept(t, c, Response{Serial: "g1", Status: StatusGood, A: at(10), B: at(500)})
	mustAccept(t, c, Response{Serial: "g2", Status: StatusGood, A: at(10), B: at(300)})
	mustAccept(t, c, Response{Serial: "g3", Status: StatusGood, A: at(10), B: at(2000)})
	if c.Len() != 2 || c.Snapshot()[0].Serial != "g1" {
		t.Fatalf("应淘汰 b 最小的 g2, got %+v", c.Snapshot())
	}

	c = newTestCache(t, 2, func() time.Time { return now })
	mustAccept(t, c, Response{Serial: "zb", Status: StatusGood, A: at(10), B: at(500)})
	mustAccept(t, c, Response{Serial: "aa", Status: StatusGood, A: at(10), B: at(500)})
	mustAccept(t, c, Response{Serial: "g3", Status: StatusGood, A: at(10), B: at(2000)})
	serials := []string{c.Snapshot()[0].Serial, c.Snapshot()[1].Serial}
	if serials[0] != "g3" || serials[1] != "zb" {
		t.Fatalf("b 并列应淘汰序号小的 aa, got %v", serials)
	}

	c = newTestCache(t, 1, func() time.Time { return now })
	mustAccept(t, c, Response{Serial: "fresh", Status: StatusGood, A: at(900), B: at(2000)})
	mustReject(t, c, Response{Serial: "new", Status: StatusGood, A: at(10), B: at(2000)}, RejectCacheFull)
	mustAccept(t, c, Response{Serial: "fresh", Status: StatusGood, A: at(950), B: at(3000)})
}

// 7. 校验顺序：空序号 < 非法状态 < a>=b < 未来响应 < 容量满，只报第一个原因。
func TestRejectOrder(t *testing.T) {
	now := at(1000)
	c := newTestCache(t, 1, func() time.Time { return now })
	mustAccept(t, c, Response{Serial: "full", Status: StatusGood, A: at(10), B: at(2000)})

	mustReject(t, c, Response{Serial: "", Status: Status("bogus"), A: at(2000), B: at(1000)}, RejectEmptySerial)
	mustReject(t, c, Response{Serial: "x", Status: Status("bogus"), A: at(2000), B: at(1000)}, RejectBadStatus)
	mustReject(t, c, Response{Serial: "x", Status: StatusGood, A: at(500), B: at(500)}, RejectBadInterval)
	mustReject(t, c, Response{Serial: "x", Status: StatusGood, A: at(1001), B: at(2000)}, RejectFuture)
	mustReject(t, c, Response{Serial: "brand-new", Status: StatusGood, A: at(10), B: at(2000)}, RejectCacheFull)
}

// 8. 全局吊销闩锁：任一判定报出已吊销后，其后所有判定均不可信。
func TestRevocationLatchPoisonAll(t *testing.T) {
	now := at(1000)
	c := newTestCache(t, 10, func() time.Time { return now })
	mustAccept(t, c, Response{Serial: "rc", Status: StatusRevoked, A: at(100), B: at(200)})
	mustAccept(t, c, Response{Serial: "gc", Status: StatusGood, A: at(100), B: at(2000)})

	if v := c.Trust("gc"); !v.Trusted {
		t.Fatalf("闩锁触发前正常证书应可信, got %+v", v)
	}
	if v := c.Trust("never-seen"); v.Reason != TrustUnknown {
		t.Fatalf("闩锁触发前无记录应报未知, got %+v", v)
	}

	if v := c.Trust("rc"); v.Reason != TrustRevoked || v.Trusted {
		t.Fatalf("应报已吊销并触发闩锁, got %+v", v)
	}
	for _, serial := range []string{"gc", "never-seen", "rc", "anything"} {
		v := c.Trust(serial)
		if v.Trusted || v.Reason != TrustRevoked {
			t.Fatalf("闩锁触发后 %s 的判定必须不可信且报已吊销, got %+v", serial, v)
		}
	}
}

// 9. 同一批响应的全部乱序排列合并结果完全一致（容量足够时）。
func TestPermutationIndependence(t *testing.T) {
	now := at(1000)
	batch := []Response{
		{Serial: "k", Status: StatusGood, A: at(100), B: at(300)},
		{Serial: "k", Status: StatusGood, A: at(200), B: at(260)},
		{Serial: "k", Status: StatusSuspended, A: at(200), B: at(400)},
		{Serial: "k", Status: StatusRevoked, A: at(150), B: at(180)},
		{Serial: "k", Status: StatusRevoked, A: at(90), B: at(95)},
		{Serial: "m", Status: StatusSuspended, A: at(100), B: at(500)},
		{Serial: "m", Status: StatusGood, A: at(100), B: at(600)},
	}
	// 8! = 40320 个排列，覆盖全部合并分支；此用例只比对结果，日志静默。

	var want []Record
	for perm := range permutations(len(batch)) {
		c := newTestCache(t, len(batch), func() time.Time { return now })
		c.SetLogOutput(io.Discard)
		for _, idx := range perm {
			res := c.Submit(batch[idx])
			if !res.Accepted {
				t.Fatalf("排列 %v 中响应被意外拒绝: %+v", perm, batch[idx])
			}
		}
		got := c.Snapshot()
		if want == nil {
			want = got
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("乱序合并结果不一致:\nperm=%v\ngot =%v\nwant=%v", perm, got, want)
		}
	}
	t.Logf("全部排列合并结果一致: %v", want)
}

// permutations 以 Go 1.23+ range-over-int 风格生成 [0,n) 的全部排列。
func permutations(n int) func(func([]int) bool) {
	return func(yield func([]int) bool) {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		var recurse func(int) bool
		recurse = func(k int) bool {
			if k == 1 {
				dup := append([]int(nil), idx...)
				return yield(dup)
			}
			for i := 0; i < k; i++ {
				if !recurse(k - 1) {
					return false
				}
				if k%2 == 0 {
					idx[i], idx[k-1] = idx[k-1], idx[i]
				} else {
					idx[0], idx[k-1] = idx[k-1], idx[0]
				}
			}
			return true
		}
		recurse(n)
	}
}

// 10. 并发提交与判定：race 检测下状态不损坏；相同输入序列判定相同。
func TestConcurrentSubmitAndTrust(t *testing.T) {
	fixed := at(1000)
	c := NewWithClock(64, func() time.Time { return fixed })
	c.SetLogOutput(os.Stdout)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				serial := fmt.Sprintf("cert-%d", (w*200+i)%40)
				status := []Status{StatusGood, StatusSuspended, StatusGood}[i%3]
				if w == 0 && i == 50 {
					status = StatusRevoked
				}
				c.Submit(Response{
					Serial: serial,
					Status: status,
					A:      at(100 + i),
					B:      at(200 + i),
				})
				c.Trust(serial)
			}
		}(w)
	}
	wg.Wait()

	// 并发过程中必出现吊销判定；闩锁置位后一切判定不可信。
	v := c.Trust("cert-0")
	if v.Trusted || v.Reason != TrustRevoked {
		t.Fatalf("并发结束后闩锁应已触发, got %+v", v)
	}

	// 相同输入序列（固定时钟）必须产生相同判定。
	seq := []Response{
		{Serial: "a", Status: StatusGood, A: at(10), B: at(2000)},
		{Serial: "b", Status: StatusSuspended, A: at(10), B: at(2000)},
		{Serial: "a", Status: StatusGood, A: at(20), B: at(3000)},
	}
	run := func() []Verdict {
		cc := NewWithClock(10, func() time.Time { return fixed })
		cc.SetLogOutput(io.Discard)
		var vs []Verdict
		for _, r := range seq {
			cc.Submit(r)
		}
		for _, s := range []string{"a", "b", "missing"} {
			vs = append(vs, cc.Trust(s))
		}
		return vs
	}
	first := run()
	for rep := 0; rep < 5; rep++ {
		if got := run(); fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("相同输入序列判定不同:\ngot =%v\nwant=%v", got, first)
		}
	}
}
