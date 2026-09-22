package lease

import (
	"errors"
	"fmt"
	"testing"
)

// 本文件直接钉住题目要求的五条不变量。
//
// 判别力（改坏方式 -> 失败的测试）：
//   - Release 里加一行 m.token = 0（复位计数器）
//     -> TestTokenMonotonicAcrossRelease 失败。
//   - Acquire 里删掉 m.token++（或改为复用当前值）
//     -> TestTokenMonotonicAcrossRelease 失败（token 重复）。
//   - Write 把 m.store[key] = val 挪到校验之前（先写后校验）
//     -> TestRejectedWriteHasNoSideEffect 失败。
//   - Write 删掉过期检查（now >= expiresAt 分支）
//     -> TestExpiryInvalidatesImmediately 失败。
//   - Acquire 删掉 ErrLeaseHeld 检查（允许他人抢占有效租约）
//     -> TestMutualExclusion 失败。

// TestTokenMonotonicAcrossRelease 钉住不变量 2 与 manager.go 注释
// "即使租约被释放，计数器也不复位（不变量 2）"：多轮 Acquire/Release，
// token 必须严格递增、无重复、不回退。
func TestTokenMonotonicAcrossRelease(t *testing.T) {
	m, c := newTestManager()
	var prev uint64
	for i := 0; i < 10; i++ {
		holder := fmt.Sprintf("H%d", i)
		tok, err := m.Acquire(holder, 50)
		if err != nil {
			t.Fatalf("第 %d 轮 Acquire: %v", i, err)
		}
		if tok <= prev {
			t.Fatalf("token 回退/重复: %d <= %d（不变量 2）", tok, prev)
		}
		prev = tok
		if err := m.Release(holder, tok); err != nil {
			t.Fatalf("第 %d 轮 Release: %v", i, err)
		}
		c.Advance(10)
	}
}

// TestMutualExclusion 钉住不变量 1：租约有效期间任何他人不得取得；
// 只有到期或释放后他人才能接管，且接管后原持有者立即失去一切权限。
func TestMutualExclusion(t *testing.T) {
	m, c := newTestManager()
	tokA, _ := m.Acquire("A", 100)

	c.Set(99)
	if _, err := m.Acquire("B", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("A 有效期间 B Acquire = %v, 期望 ErrLeaseHeld", err)
	}
	if _, err := m.Acquire("C", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("A 有效期间 C Acquire = %v, 期望 ErrLeaseHeld", err)
	}

	c.Set(100) // A 到期，B 接管
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("A 到期后 B Acquire 应成功: %v", err)
	}
	if _, err := m.Acquire("A", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("B 有效期间 A 重新 Acquire = %v, 期望 ErrLeaseHeld", err)
	}
	if err := m.Write(tokA, "k", "x"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("A 旧 token Write = %v, 期望 ErrStaleToken", err)
	}
	if err := m.Write(tokB, "k", "v"); err != nil {
		t.Fatalf("B Write 应成功: %v", err)
	}
}

// TestFencingRejectsStaleHolders 钉住不变量 3：被抢占者、已释放者、
// 已过期者的旧 token 一律被 Write 拒绝。
func TestFencingRejectsStaleHolders(t *testing.T) {
	m, c := newTestManager()
	tokA, _ := m.Acquire("A", 100)
	c.Set(100)
	tokB, _ := m.Acquire("B", 100) // A 被抢占

	if err := m.Write(tokA, "k", "x"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("被抢占者 Write = %v, 期望 ErrStaleToken", err)
	}
	if err := m.Release("B", tokB); err != nil {
		t.Fatalf("B Release: %v", err)
	}
	if err := m.Write(tokB, "k", "x"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("已释放者 Write = %v, 期望 ErrStaleToken", err)
	}
	tokC, _ := m.Acquire("C", 100)
	c.Set(200) // C 也到期
	if err := m.Write(tokC, "k", "x"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("已过期者 Write = %v, 期望 ErrLeaseExpired", err)
	}
}

// TestRejectedWriteHasNoSideEffect 钉住不变量 4 与 store.go 注释
// "任何被拒绝的 Write 都不会触碰 store（不变量 4）：所有校验都在写之前
// 完成，失败路径没有任何副作用"：每种拒绝之后逐键比对 Read 结果。
func TestRejectedWriteHasNoSideEffect(t *testing.T) {
	m, c := newTestManager()
	tok1, _ := m.Acquire("A", 100)
	if err := m.Write(tok1, "k1", "v1"); err != nil {
		t.Fatalf("Write k1: %v", err)
	}
	if err := m.Write(tok1, "k2", "v2"); err != nil {
		t.Fatalf("Write k2: %v", err)
	}
	snapshot := map[string]string{"k1": "v1", "k2": "v2"}
	assertIntact := func(step string) {
		t.Helper()
		for k, want := range snapshot {
			if got, ok := m.Read(k); !ok || got != want {
				t.Fatalf("%s 后 Read(%s) = %q,%v, 期望 %q,true（不变量 4）",
					step, k, got, ok, want)
			}
		}
		if _, ok := m.Read("k3"); ok {
			t.Fatalf("%s 后 k3 不应存在（不变量 4）", step)
		}
	}

	tok2, _ := m.Acquire("A", 100) // tok1 过时
	rejected := []struct {
		name string
		err  error
	}{
		{"旧 token", m.Write(tok1, "k1", "evil")},
		{"巨大未知 token", m.Write(^uint64(0), "k2", "evil")},
		{"token=0", m.Write(0, "k3", "evil")},
		{"新 key 旧 token", m.Write(tok1, "k3", "evil")},
	}
	for _, r := range rejected {
		if !errors.Is(r.err, ErrStaleToken) {
			t.Fatalf("%s: err = %v, 期望 ErrStaleToken", r.name, r.err)
		}
		assertIntact(r.name)
	}

	c.Set(200) // 租约到期后的拒绝写同样不得有副作用
	if err := m.Write(tok2, "k1", "evil"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("过期写 = %v, 期望 ErrLeaseExpired", err)
	}
	assertIntact("过期写")
}

// TestExpiryInvalidatesImmediately 钉住不变量 5：到期那一刻起持有者
// 立即失效，哪怕记录仍在、token 仍匹配，写与续约都被拒。
func TestExpiryInvalidatesImmediately(t *testing.T) {
	m, c := newTestManager()
	tok, _ := m.Acquire("A", 100)

	c.Set(99)
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("到期前 Write 应成功: %v", err)
	}
	c.Set(100) // 恰好到期：立即失效
	if err := m.Write(tok, "k", "x"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("到期后 Write = %v, 期望 ErrLeaseExpired（不变量 5）", err)
	}
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("到期后 Renew = %v, 期望 ErrLeaseExpired（不变量 5）", err)
	}
	if got, _ := m.Read("k"); got != "v" {
		t.Fatalf("到期后的写不得生效, Read(k) = %q（不变量 4）", got)
	}
}
