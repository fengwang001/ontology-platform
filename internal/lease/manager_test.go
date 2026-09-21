package lease

import (
	"errors"
	"testing"
)

// clock 是测试用的逻辑时钟，由测试手动推进。
type clock struct{ t int64 }

func (c *clock) now() int64  { return c.t }
func (c *clock) set(t int64) { c.t = t }
func (c *clock) add(d int64) { c.t += d }

func newManager() (*Manager, *clock) {
	c := &clock{}
	return New(c.now), c
}

// 样例表第 1、2 行：空闲 Acquire 成功且 token 从 1 开始；
// 他人持有有效租约时 Acquire 失败。
func TestAcquireMutualExclusion(t *testing.T) {
	m, _ := newManager()
	tok, err := m.Acquire("A", 100)
	if err != nil || tok != 1 {
		t.Fatalf("first Acquire = (%d, %v), want (1, nil)", tok, err)
	}
	if _, err := m.Acquire("B", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("Acquire while held = %v, want ErrLeaseHeld", err)
	}
}

// 样例表第 3 行：A 到期后 B 可以 Acquire，token 递增为 2。
func TestAcquireAfterExpiry(t *testing.T) {
	m, c := newManager()
	if _, err := m.Acquire("A", 100); err != nil {
		t.Fatal(err)
	}
	c.add(100) // now == expiresAt，半开区间下已过期
	tok, err := m.Acquire("B", 100)
	if err != nil || tok != 2 {
		t.Fatalf("Acquire after expiry = (%d, %v), want (2, nil)", tok, err)
	}
}

// 样例表第 4、5 行：被抢占者用旧 token 写被拒绝且无副作用；
// 当前持有者写成功且可读回。
func TestFencingAndNoSideEffect(t *testing.T) {
	m, c := newManager()
	tokA, _ := m.Acquire("A", 100)
	if err := m.Write(tokA, "k", "v"); err != nil {
		t.Fatalf("holder Write = %v, want nil", err)
	}
	if got, ok := m.Read("k"); !ok || got != "v" {
		t.Fatalf("Read = (%q, %v), want (\"v\", true)", got, ok)
	}
	c.add(100)
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(tokA, "k", "evil"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("stale Write = %v, want ErrStaleToken", err)
	}
	if got, ok := m.Read("k"); !ok || got != "v" {
		t.Fatalf("Read after rejected Write = (%q, %v), want (\"v\", true)", got, ok)
	}
}

// 样例表第 6 行：Release 后他人可 Acquire，token 继续递增。
func TestReleaseThenAcquire(t *testing.T) {
	m, _ := newManager()
	tokA, _ := m.Acquire("A", 100)
	if err := m.Release("A", tokA); err != nil {
		t.Fatal(err)
	}
	tokB, err := m.Acquire("B", 100)
	if err != nil || tokB != 2 {
		t.Fatalf("Acquire after Release = (%d, %v), want (2, nil)", tokB, err)
	}
	if err := m.Write(tokA, "k", "v"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("Write after Release = %v, want ErrStaleToken", err)
	}
}

// 样例表第 7、8 行：非法 ttl 与空 holder 都是可判定错误。
func TestInvalidArguments(t *testing.T) {
	m, _ := newManager()
	if _, err := m.Acquire("A", 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("Acquire ttl=0 = %v, want ErrInvalidTTL", err)
	}
	if _, err := m.Acquire("A", -5); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("Acquire ttl<0 = %v, want ErrInvalidTTL", err)
	}
	if _, err := m.Acquire("", 100); !errors.Is(err, ErrEmptyHolder) {
		t.Fatalf("Acquire empty holder = %v, want ErrEmptyHolder", err)
	}
	if err := m.Renew("", 1, 100); !errors.Is(err, ErrEmptyHolder) {
		t.Fatalf("Renew empty holder = %v, want ErrEmptyHolder", err)
	}
	if err := m.Release("", 1); !errors.Is(err, ErrEmptyHolder) {
		t.Fatalf("Release empty holder = %v, want ErrEmptyHolder", err)
	}
}

// 非法参数不得消耗 token：失败后下一次 Acquire 仍拿到下一个序号。
func TestFailedAcquireKeepsTokenSequence(t *testing.T) {
	m, _ := newManager()
	_, _ = m.Acquire("", 100)
	_, _ = m.Acquire("A", 0)
	tok, err := m.Acquire("A", 100)
	if err != nil || tok != 1 {
		t.Fatalf("Acquire = (%d, %v), want (1, nil)", tok, err)
	}
}
