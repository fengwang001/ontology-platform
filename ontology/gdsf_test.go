package ontology

import (
	"errors"
	"fmt"
	"math/big"
	"testing"
)

func ratN(n int64) *big.Rat { return big.NewRat(n, 1) }

func ratStr(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat: " + s)
	}
	return r
}

func mustCache(t *testing.T, cap int64) *Cache {
	t.Helper()
	c, err := NewCache(cap)
	if err != nil {
		t.Fatalf("NewCache(%d): %v", cap, err)
	}
	return c
}

// 构造容量非法时返回可区分原因。
func TestNewCacheInvalidCapacity(t *testing.T) {
	for _, cap := range []int64{0, -1, -100} {
		if _, err := NewCache(cap); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("NewCache(%d) err=%v, want ErrInvalidCapacity", cap, err)
		}
	}
}

// 基本插入与 Used / Peek / tick。
func TestPutAndPeek(t *testing.T) {
	c := mustCache(t, 10)
	ev, err := c.Put("a", 3, 2)
	if err != nil || len(ev) != 0 {
		t.Fatalf("Put a ev=%v err=%v", ev, err)
	}
	if c.Used() != 3 {
		t.Fatalf("Used=%d want 3", c.Used())
	}
	v, ok, err := c.Peek("a")
	if err != nil || !ok {
		t.Fatalf("Peek a ok=%v err=%v", ok, err)
	}
	if v.H.Cmp(ratStr("2/3")) != 0 || v.Freq != 1 || v.Last != 0 {
		t.Fatalf("Peek a = %+v, want H=2/3 freq=1 last=0", v)
	}
	if _, ok, _ := c.Peek("missing"); ok {
		t.Fatalf("Peek missing should be not found")
	}
}

// 驱逐时 L 推进为“被驱逐者的 H”，不是 0，也不是平均值。
func TestEvictionAdvancesLToVictimH(t *testing.T) {
	c := mustCache(t, 3)
	c.Put("a", 1, 6)           // H=6
	c.Put("b", 1, 3)           // H=3
	c.Put("d", 1, 2)           // H=2
	ev, _ := c.Put("e", 1, 10) // 驱逐 H 最小者 d(H=2)，L=2
	if len(ev) != 1 || ev[0] != "d" {
		t.Fatalf("ev=%v want [d]", ev)
	}
	if L := c.inflation(); L.Cmp(ratN(2)) != 0 {
		t.Fatalf("L=%s want 2（被驱逐者 d 的 H），而非 0 或平均值", L.FloatString(6))
	}
	if v, _, _ := c.Peek("e"); v.H.Cmp(ratN(12)) != 0 {
		t.Fatalf("e H=%s want 12", v.H.FloatString(6))
	}
}

// 命中后用“当前 L”重算 H，而不是沿用旧 H。
func TestGetRecomputesHWithCurrentL(t *testing.T) {
	c := mustCache(t, 2)
	c.Put("a", 1, 4)          // H=4
	c.Put("b", 1, 1)          // H=1
	ev, _ := c.Put("c", 1, 1) // 驱逐 b，L=1；c 的 H=2
	if ev[0] != "b" {
		t.Fatalf("ev=%v want [b]", ev)
	}
	hit, err := c.Get("a")
	if !hit || err != nil {
		t.Fatalf("Get a hit=%v err=%v", hit, err)
	}
	// freq=2，H = 当前 L(1) + 2*4 = 9，而非旧 H(4)。
	if v, _, _ := c.Peek("a"); v.H.Cmp(ratN(9)) != 0 || v.Freq != 2 {
		t.Fatalf("after hit a H=%s freq=%d, want H=9 freq=2", v.H.FloatString(6), v.Freq)
	}
	// tick: Put a=0,b=1,c=2, Get a=3
	if v, _, _ := c.Peek("a"); v.Last != 3 {
		t.Fatalf("a last=%d want 3", v.Last)
	}
	if hit, _ := c.Get("zzz"); hit {
		t.Fatalf("Get zzz should miss")
	}
	if L := c.inflation(); L.Cmp(ratN(1)) != 0 {
		t.Fatalf("miss changed L: %s", L.FloatString(6))
	}
}

// 一次 Put 连续驱逐多个时，L 逐个等于各被驱逐者的 H（单调推进）。
func TestMultipleEvictionsMonotonicL(t *testing.T) {
	c := mustCache(t, 2)
	c.Put("a", 1, 1) // H=1
	c.Put("b", 1, 2) // H=2
	ev, err := c.Put("big", 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ev) != "[a b]" {
		t.Fatalf("ev=%v want [a b]", ev)
	}
	if L := c.inflation(); L.Cmp(ratN(2)) != 0 {
		t.Fatalf("L=%s want 2（最后被驱逐者 b 的 H）", L.FloatString(6))
	}
	if v, _, _ := c.Peek("big"); v.H.Cmp(ratStr("9/2")) != 0 {
		t.Fatalf("big H=%s want 9/2", v.H.FloatString(6))
	}
	if c.Used() != 2 {
		t.Fatalf("Used=%d want 2", c.Used())
	}
}

// H 相等时驱逐 last 较小者。
func TestTieBreaksOnOlderLast(t *testing.T) {
	c := mustCache(t, 3)
	c.Put("a", 1, 2)          // H=2,last=0
	c.Put("x", 1, 1)          // H=1,last=1
	c.Put("y", 1, 1)          // H=1,last=2
	ev, _ := c.Put("z", 1, 1) // x 与 y 的 H 并列，取更老 x
	if ev[0] != "x" {
		t.Fatalf("ev=%v want [x] on tie", ev)
	}
	c.Get("z")                 // z: freq=2,H=1+2=3,last=3
	ev2, _ := c.Put("w", 1, 1) // 驱逐 y(H=1)，L=1；w H=2
	if ev2[0] != "y" {
		t.Fatalf("ev2=%v want [y]", ev2)
	}
	// a(H=2,last=0) 与 w(H=2,last=4) 并列，应驱逐更老的 a。
	ev3, _ := c.Put("q", 1, 9)
	if ev3[0] != "a" {
		t.Fatalf("ev3=%v want [a] (older last on H tie)", ev3)
	}
}

// 覆盖写先移除旧条目、释放字节且不推进 L。
func TestOverwriteRemovesOldWithoutAdvancingL(t *testing.T) {
	c := mustCache(t, 3)
	c.Put("a", 1, 1)
	c.Put("b", 1, 5)
	// 插入 size2 的 big：2+2=4>3，驱逐 a(H=1)，L=1；big 的 H=1+2/2=2，used=3。
	ev, _ := c.Put("big", 2, 2)
	if ev[0] != "a" {
		t.Fatalf("setup ev=%v want [a]", ev)
	}
	// b(size1,H5)、big(size2,H2)，used=3。覆盖 big 为新 size2/cost7：
	// 先移除旧 big 释放 2 字节，used=1，1+2==3 恰等于 Cap，不驱逐、L 不变。
	ev, err := c.Put("big", 2, 7)
	if err != nil || len(ev) != 0 {
		t.Fatalf("overwrite ev=%v err=%v, want no eviction", ev, err)
	}
	if c.Used() != 3 {
		t.Fatalf("Used=%d want 3", c.Used())
	}
	if L := c.inflation(); L.Cmp(ratN(1)) != 0 {
		t.Fatalf("overwrite advanced L to %s, want unchanged 1", L.FloatString(6))
	}
	if v, ok, _ := c.Peek("big"); !ok || v.Freq != 1 || v.H.Cmp(ratStr("9/2")) != 0 {
		t.Fatalf("big after overwrite = %+v ok=%v want freq=1 H=9/2", v, ok)
	}
}

// 已用 + size 恰等于 Cap 时不驱逐。
func TestExactFitNoEviction(t *testing.T) {
	c := mustCache(t, 4)
	c.Put("a", 3, 2)
	ev, err := c.Put("b", 1, 9)
	if err != nil || len(ev) != 0 {
		t.Fatalf("exact-fit ev=%v err=%v", ev, err)
	}
	if L := c.inflation(); L.Sign() != 0 {
		t.Fatalf("L changed on exact fit: %s", L.FloatString(6))
	}
}

// 新条目 H 低于现有条目时仍被插入（无准入过滤）。
func TestNewEntryWithLowerHInserted(t *testing.T) {
	c := mustCache(t, 2)
	c.Put("hi", 1, 100)
	if _, err := c.Put("lo", 1, 1); err != nil { // H=1 < 100，仍插入
		t.Fatal(err)
	}
	if _, ok, _ := c.Peek("lo"); !ok {
		t.Fatalf("low-H new entry must be admitted")
	}
	ev, _ := c.Put("z", 1, 1)
	if ev[0] != "lo" {
		t.Fatalf("ev=%v want [lo]", ev)
	}
}

// 整数乘法用浮点会判错的大数据例。
//
// 在 L=2^53 附近 float64 的 ulp 为 2，因此浮点会把 H=L+1 舍入成 L（不可区分），
// 而精确有理运算能正确区分 L 与 L+1。这会改变驱逐顺序。
func TestLargeIntegersRequireRational(t *testing.T) {
	const M = int64(1) << 53
	c := mustCache(t, 3)
	c.Put("victim", 1, M)        // H=M
	c.Put("k1", 1, M)            // H=M
	c.Put("k2", 1, M)            // H=M（已满 3）
	ev, _ := c.Put("fill", 1, 1) // 三个 H=M 并列，驱逐最老 victim，L=M
	if ev[0] != "victim" {
		t.Fatalf("setup ev=%v want [victim]", ev)
	}
	if L := c.inflation(); L.Cmp(ratN(M)) != 0 {
		t.Fatalf("setup L=%s want %d", L.FloatString(0), M)
	}
	// 当前: k1(H=M,last1), k2(H=M,last2), fill(H=M+1,last3)，used=3。
	// 插入 a(cost1)：H=M+1，需驱逐当前最小者 k1(H=M)。
	// 若用 float64，M+1 被舍入为 M，会把 fill/a 与 k1/k2 混为同级而误判驱逐对象。
	evA, _ := c.Put("a", 1, 1)
	if len(evA) != 1 || evA[0] != "k1" {
		t.Fatalf("evA=%v want [k1]; 若用浮点会因 M+1 舍入为 M 而误判", evA)
	}
	// 反例有效性自检：Rat 能区分 M 与 M+1，float64 不能。
	if new(big.Rat).SetFrac64(M+1, 1).Cmp(ratN(M)) == 0 {
		t.Fatal("Rat should distinguish M and M+1")
	}
	if float64(M)+1 != float64(M) {
		t.Fatal("float64 at 2^53 should round M+1 to M")
	}
}

// 拒绝次序：键为空 → size<=0 → cost<1 → size>Cap，只报第一个。
func TestPutRejectionOrder(t *testing.T) {
	c := mustCache(t, 5)
	if _, err := c.Put("", 0, 0); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	if _, err := c.Put("k", 0, 0); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("want ErrInvalidSize, got %v", err)
	}
	if _, err := c.Put("k", -3, 0); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("want ErrInvalidSize for negative size, got %v", err)
	}
	if _, err := c.Put("k", 1, 0); !errors.Is(err, ErrInvalidCost) {
		t.Fatalf("want ErrInvalidCost, got %v", err)
	}
	if _, err := c.Put("k", 6, 1); !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("want ErrObjectTooLarge, got %v", err)
	}

	// 因 size>Cap 被拒的覆盖写不得改变任何条目、L 与 tick。
	c.Put("dup", 2, 3)
	beforeUsed := c.Used()
	if v, ok, _ := c.Peek("dup"); !ok || v.Freq != 1 || v.H.Cmp(ratStr("3/2")) != 0 {
		t.Fatalf("dup before = %+v ok=%v", v, ok)
	}
	if _, err := c.Put("dup", 6, 1); !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("overwrite too large: want ErrObjectTooLarge, got %v", err)
	}
	if c.Used() != beforeUsed {
		t.Fatalf("rejected overwrite changed Used: %d -> %d", beforeUsed, c.Used())
	}
	if v, ok, _ := c.Peek("dup"); !ok || v.Freq != 1 || v.H.Cmp(ratStr("3/2")) != 0 || v.Last != 0 {
		t.Fatalf("dup mutated by rejected overwrite: %+v", v)
	}
	if L := c.inflation(); L.Sign() != 0 {
		t.Fatalf("rejected overwrite changed L: %s", L.FloatString(6))
	}

	// Get / Peek 空键拒绝。
	if _, err := c.Get(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Get empty key err=%v want ErrEmptyKey", err)
	}
	if _, _, err := c.Peek(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Peek empty key err=%v want ErrEmptyKey", err)
	}
}
