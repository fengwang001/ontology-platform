package lease

import (
	"errors"
	"testing"
)

// clock 是可手动推进的逻辑时钟，测试通过它确定性地控制过期。
type clock struct{ now int64 }

func (c *clock) Now() int64      { return c.now }
func (c *clock) Advance(d int64) { c.now += d }

func newManager() (*Manager, *clock) {
	c := &clock{}
	return New(c.Now), c
}

// 样例表：空闲时 Acquire 成功，token 从 1 开始。
func TestAcquireFirstToken(t *testing.T) {
	m, _ := newManager()
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if tok != 1 {
		t.Fatalf("token = %d, want 1", tok)
	}
}

// 样例表：A 持有中，B 抢占失败。
func TestAcquireWhileHeld(t *testing.T) {
	m, _ := newManager()
	if _, err := m.Acquire("A", 100); err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	if _, err := m.Acquire("B", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("Acquire B err = %v, want ErrLeaseHeld", err)
	}
}

// 样例表：A 到期后 B 抢占成功，token 严格递增。
func TestAcquireAfterExpiry(t *testing.T) {
	m, c := newManager()
	if _, err := m.Acquire("A", 100); err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	c.Advance(100)
	tok, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	if tok != 2 {
		t.Fatalf("token = %d, want 2", tok)
	}
}

// 样例表：被抢占者的旧 token 写失败，且 Read 结果不变。
func TestStaleTokenWriteRejected(t *testing.T) {
	m, c := newManager()
	tokA, _ := m.Acquire("A", 100)
	if err := m.Write(tokA, "k", "v1"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	c.Advance(100)
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	if err := m.Write(tokA, "k", "evil"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("Write err = %v, want ErrStaleToken", err)
	}
	if v, ok := m.Read("k"); !ok || v != "v1" {
		t.Fatalf("Read = %q,%v, want v1,true", v, ok)
	}
}

// 样例表：持有中写入成功，Read 可读回。
func TestWriteAndRead(t *testing.T) {
	m, _ := newManager()
	tok, _ := m.Acquire("A", 100)
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if v, ok := m.Read("k"); !ok || v != "v" {
		t.Fatalf("Read = %q,%v, want v,true", v, ok)
	}
	if _, ok := m.Read("missing"); ok {
		t.Fatal("Read(missing) should not exist")
	}
}

// 样例表：Release 后他人可 Acquire，token 继续递增。
func TestReleaseThenAcquire(t *testing.T) {
	m, _ := newManager()
	tokA, _ := m.Acquire("A", 100)
	if err := m.Release("A", tokA); err != nil {
		t.Fatalf("Release: %v", err)
	}
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	if tokB <= tokA {
		t.Fatalf("token %d not > %d", tokB, tokA)
	}
	// 已释放者的旧 token 写必须被拒绝。
	if err := m.Write(tokA, "k", "v"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("Write err = %v, want ErrStaleToken", err)
	}
}

// 样例表：ttlMillis <= 0 与空 holder 都是可判定错误。
func TestInvalidArgs(t *testing.T) {
	m, _ := newManager()
	if _, err := m.Acquire("A", 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("err = %v, want ErrInvalidTTL", err)
	}
	if _, err := m.Acquire("A", -5); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("err = %v, want ErrInvalidTTL", err)
	}
	if _, err := m.Acquire("", 100); !errors.Is(err, ErrInvalidHolder) {
		t.Fatalf("err = %v, want ErrInvalidHolder", err)
	}
	if err := m.Renew("", 1, 100); !errors.Is(err, ErrInvalidHolder) {
		t.Fatalf("Renew err = %v, want ErrInvalidHolder", err)
	}
	if err := m.Release("", 1); !errors.Is(err, ErrInvalidHolder) {
		t.Fatalf("Release err = %v, want ErrInvalidHolder", err)
	}
}
