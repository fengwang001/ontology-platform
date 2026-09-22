package ttlcache

import (
	"strconv"
	"testing"
)

// massExpiryExamined 装满 n 项（同一时刻写入、同一 TTL，全部同时过期），
// 再 Put 一项触发驱逐，返回这次驱逐考察的候选数。
// 同时断言被驱逐的是并列项中最近使用者（最后写入的键）。
func massExpiryExamined(t *testing.T, n int) int {
	t.Helper()
	c, clk := newCache(t, n)
	for i := 0; i < n; i++ {
		mustPut(t, c, "k"+strconv.Itoa(i), "v", 10)
	}
	clk.Advance(10) // n 项全部同时过期
	mustPut(t, c, "trigger", "v", 100)

	last := "k" + strconv.Itoa(n-1)
	if c.Delete(last) {
		t.Errorf("N=%d: 并列时应驱逐最近写入的 %s", n, last)
	}
	if c.Len() != n {
		t.Errorf("N=%d: Len() = %d, 期望 %d", n, c.Len(), n)
	}
	return c.lastExamined
}

// 大量项同时过期时，单次驱逐考察的候选数不随缓存规模线性增长：
// 旧的线性扫描在 N=100/1000 时分别考察 100/1000 项，
// 重构后应为与 N 无关的小常数。
func TestEvictExaminedSublinear(t *testing.T) {
	small := massExpiryExamined(t, 100)
	large := massExpiryExamined(t, 1000)
	t.Logf("N=100 考察 %d 个候选, N=1000 考察 %d 个候选", small, large)
	if small > 20 || large > 20 {
		t.Errorf("考察数应为小常数: N=100→%d, N=1000→%d", small, large)
	}
	if large > small*2+4 {
		t.Errorf("考察数随 N 线性增长: N=100→%d, N=1000→%d", small, large)
	}
}

// 没有任何过期项时，驱逐同样不扫描候选堆（走 O(1) 快路径后驱逐 LRU 队尾）。
func TestEvictExaminedNoneExpired(t *testing.T) {
	const n = 500
	c, _ := newCache(t, n)
	for i := 0; i < n; i++ {
		mustPut(t, c, "k"+strconv.Itoa(i), "v", 1000)
	}
	mustPut(t, c, "trigger", "v", 1000) // 触发驱逐：无过期项
	if c.lastExamined > 20 {
		t.Errorf("无过期项时考察数 = %d, 期望小常数", c.lastExamined)
	}
	if !c.Delete("trigger") {
		t.Error("trigger 应存在")
	}
	if c.Delete("k0") {
		t.Error("最久未使用的 k0 应被驱逐")
	}
}
