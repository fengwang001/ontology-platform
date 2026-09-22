package lease

// 本文件逐条钉住任务要求的五条不变量。
// 每个测试的注释都标注了它钉的是哪一条。

import (
	"errors"
	"testing"
)

// snapshot 读出已知键集合的内容，用于逐键比对（不变量 4）。
func snapshot(m *Manager) map[string]string {
	got := make(map[string]string)
	for _, k := range []string{"k1", "k2", "k3", "x", "y"} {
		if v, ok := m.Read(k); ok {
			got[k] = v
		}
	}
	return got
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// 不变量 1（互斥）："任意时刻至多一个持有者的租约有效。"
// A 持有时 B 的 Acquire 必须失败；A 释放后 B 才能成功。
func TestInvariantMutualExclusion(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })

	tokA, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	if _, err := m.Acquire("B", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("A 有效期间 B Acquire = %v, want ErrLeaseHeld", err)
	}
	if err := m.Release("A", tokA); err != nil {
		t.Fatalf("Release A: %v", err)
	}
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("A 释放后 B 应能 Acquire, got %v", err)
	}
}

// 不变量 2（token 严格单调）："每次成功 Acquire 返回的 token
// 严格大于此前发出过的所有 token……Release 不复位计数器。"
func TestInvariantTokensStrictlyMonotonic(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })

	var prev uint64
	round := func(holder string, now int64) {
		clock = now // 推进时间使旧租约到期，模拟多轮持有
		tok, err := m.Acquire(holder, 10)
		if err != nil {
			t.Fatalf("Acquire %s: %v", holder, err)
		}
		if tok <= prev {
			t.Fatalf("token %d 未严格大于上一个 %d", tok, prev)
		}
		prev = tok
		if err := m.Release(holder, tok); err != nil {
			t.Fatalf("Release: %v", err)
		}
	}
	round("A", 0)
	round("B", 100)
	round("A", 200) // 同一 holder 再次出现也必须发新号
	round("C", 300)
	if prev != 4 {
		t.Fatalf("共 4 次成功 Acquire, 最后 token = %d, want 4", prev)
	}
}

// 不变量 3（fencing）："Write 只接受当前有效租约的 token；
// 被抢占者、已过期者、已释放者的旧 token 一律拒绝。"
func TestInvariantFencingRejectsStaleTokens(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })

	tokA, _ := m.Acquire("A", 100)
	if err := m.Write(tokA, "k1", "a"); err != nil {
		t.Fatalf("A 写: %v", err)
	}

	// 被抢占者：A 到期后 B 接管，A 的旧 token 写必须被拒。
	clock = 100
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	if err := m.Write(tokA, "k1", "evil"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("被抢占者旧 token 写 = %v, want ErrStaleToken", err)
	}

	// 已过期者：B 的 token 匹配记录但租约已到期，写必须被拒。
	clock = 200
	if err := m.Write(tokB, "k1", "late"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("过期 token 写 = %v, want ErrLeaseExpired", err)
	}

	// 已释放者：B 重新获取后释放，C 接管，B 的 token 已成旧号。
	tokB2, _ := m.Acquire("B", 100) // clock=200
	if err := m.Release("B", tokB2); err != nil {
		t.Fatalf("Release B: %v", err)
	}
	tokC, _ := m.Acquire("C", 100)
	if err := m.Write(tokB2, "k1", "ghost"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("已释放者旧 token 写 = %v, want ErrStaleToken", err)
	}
	if err := m.Write(tokC, "k1", "c"); err != nil {
		t.Fatalf("当前持有者 C 写应成功: %v", err)
	}
}

// 不变量 4（无副作用）："任何被拒绝的 Write 都不得改变资源内容——
// 拒绝后 Read 的结果必须与调用前逐键一致。"
func TestInvariantRejectedWriteHasNoSideEffect(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 100)
	if err := m.Write(tok, "k1", "v1"); err != nil {
		t.Fatalf("写 k1: %v", err)
	}
	if err := m.Write(tok, "k2", "v2"); err != nil {
		t.Fatalf("写 k2: %v", err)
	}
	base := snapshot(m)

	clock = 100 // A 到期
	try := func(err error, what string) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s 居然成功", what)
		}
		if got := snapshot(m); !mapsEqual(got, base) {
			t.Fatalf("%s 后 store 被改变: %v != %v", what, got, base)
		}
	}
	try(m.Write(tok, "k1", "late"), "到期后旧 token 改写已有键")
	try(m.Write(tok, "k3", "late-new"), "到期后写入新键")
	tokB, _ := m.Acquire("B", 100)
	try(m.Write(tok, "k2", "evil"), "被抢占者旧 token 改写")
	try(m.Write(999999, "k3", "fake"), "巨大伪造 token 写新键")
	if err := m.Write(tokB, "k3", "b"); err != nil {
		t.Fatalf("当前持有者写应成功: %v", err)
	}
}

// 不变量 5（过期即失效）："租约到期后持有者立即不再有效，
// 哪怕他还在用旧 token 写。" 到期前一拍可写，到期那一刻不可写。
func TestInvariantExpiryInvalidatesImmediately(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 10) // expiresAt = 10

	clock = 9
	if err := m.Write(tok, "k", "alive"); err != nil {
		t.Fatalf("到期前一拍 now=9 应可写: %v", err)
	}
	clock = 10 // 恰好到期
	if err := m.Write(tok, "k", "dead"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("到期那一刻 now=10 写 = %v, want ErrLeaseExpired", err)
	}
	clock = 11
	if err := m.Write(tok, "k", "dead2"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("过期后 now=11 写 = %v, want ErrLeaseExpired", err)
	}
	if got, _ := m.Read("k"); got != "alive" {
		t.Fatalf("被拒的写留下了痕迹: Read(k)=%q", got)
	}
}
