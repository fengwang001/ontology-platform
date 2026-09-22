package lease

import (
	"errors"
	"testing"
)

// 仅有的冒烟测试：证明包能编译、最基本的路径能走通。
// 这里刻意没有覆盖不变量与边界，补全是本题的任务。

func TestSmokeAcquireWriteRead(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if tok != 1 {
		t.Fatalf("token = %d, want 1", tok)
	}
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, ok := m.Read("k"); !ok || got != "v" {
		t.Fatalf("Read(k) = %q,%v", got, ok)
	}
}

func TestSmokeSecondHolderRejected(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	if _, err := m.Acquire("A", 100); err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	if _, err := m.Acquire("B", 100); err == nil {
		t.Fatal("B 不该拿到租约")
	}
}

func TestSmokeInvalidArgs(t *testing.T) {
	m := New(func() int64 { return 0 })
	if _, err := m.Acquire("", 100); !errors.Is(err, ErrInvalidHolder) {
		t.Errorf("空 holder: %v", err)
	}
	if _, err := m.Acquire("A", 0); !errors.Is(err, ErrInvalidTTL) {
		t.Errorf("ttl=0: %v", err)
	}
}
