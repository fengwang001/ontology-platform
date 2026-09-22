package lease

import (
	"errors"
	"testing"
)

// TestWritePreviousTokenAndUnknownHugeTokenAreSameSentinel 钉 errors.go 的
// 设计决策原话："token 是"当前 token 的前一个值"与"从未发出过的巨大值"
// 归为同一类错误 ErrStaleToken……区分"曾经合法但已过时"与"从未存在"
// 对安全性没有帮助，反而泄露内部状态。"
// 改坏方式：为"前一个值"单独引入/返回别的哨兵（如 ErrTokenMismatch），
// 本测试对旧 token 的 ErrStaleToken 断言失败。
func TestWritePreviousTokenAndUnknownHugeTokenAreSameSentinel(t *testing.T) {
	m := New(newFakeClock(0).now)
	tokOld, _ := m.Acquire("A", 100)
	_, _ = m.Acquire("A", 100)

	errPrev := m.Write(tokOld, "k", "x") // "当前有效 token 的前一个值"
	if !errors.Is(errPrev, ErrStaleToken) {
		t.Fatalf("前一个 token 写 = %v, want ErrStaleToken", errPrev)
	}
	errHuge := m.Write(^uint64(0), "k", "x") // "从未发出过的巨大值"
	if !errors.Is(errHuge, ErrStaleToken) {
		t.Fatalf("巨大 token 写 = %v, want ErrStaleToken", errHuge)
	}
	if !errors.Is(errPrev, errHuge) {
		t.Fatal("两类 token 必须是同一个哨兵")
	}
	// token 0（计数器初值，从未成功 Acquire 发出过）同等拒绝。
	if err := m.Write(0, "k", "x"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("token=0 写 = %v, want ErrStaleToken", err)
	}
}

// TestPreemptedHolderWriteRejectedAndCurrentHolderUnaffected 钉不变量 3
// （fencing：被抢占者的旧 token 一律拒绝）与互斥：被抢占后当前持有者
// 的写不受干扰。
// 改坏方式：把 Write 的 `token != m.token` 检查删掉，A 的旧 token 写会
// 成功覆盖 B 的资源，本测试失败。
func TestPreemptedHolderWriteRejectedAndCurrentHolderUnaffected(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tokA, _ := m.Acquire("A", 100)
	if err := m.Write(tokA, "k", "fromA"); err != nil {
		t.Fatalf("A 写: %v", err)
	}
	clk.set(100)
	tokB, _ := m.Acquire("B", 100)

	if err := m.Write(tokA, "k", "evil"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("被抢占者旧 token 写 = %v, want ErrStaleToken", err)
	}
	if err := m.Write(tokB, "k", "fromB"); err != nil {
		t.Fatalf("当前持有者 B 写: %v", err)
	}
	if got, _ := m.Read("k"); got != "fromB" {
		t.Fatalf("资源内容 = %q, want fromB", got)
	}
}

// TestExpiredTokenWriteRejectedExactlyAndAfterExpiry 钉不变量 5：
// "租约到期后持有者立即不再有效，哪怕他还在用旧 token 写。"
// token 仍匹配记录，但 now==expiresAt 起 Write 返回 ErrLeaseExpired，
// 且这与 ErrStaleToken 是两个不同哨兵。
// 改坏方式：把 Write 的过期分支返回 ErrStaleToken，本测试的
// ErrLeaseExpired 断言失败。
func TestExpiredTokenWriteRejectedExactlyAndAfterExpiry(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tok, _ := m.Acquire("A", 100)
	_ = m.Write(tok, "k", "v")

	for _, ms := range []int64{100, 101, 1000} {
		clk.set(ms)
		err := m.Write(tok, "k", "late")
		if !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("now=%d Write = %v, want ErrLeaseExpired", ms, err)
		}
		if errors.Is(err, ErrStaleToken) {
			t.Fatalf("now=%d 过期错误不应同时命中 ErrStaleToken", ms)
		}
		if got, _ := m.Read("k"); got != "v" {
			t.Fatalf("now=%d 被拒写产生副作用", ms)
		}
	}
}

// TestRejectedWritesHaveNoSideEffectsAcrossKeys 钉不变量 4（无副作用）：
// "任何被拒绝的 Write 都不得改变资源内容——拒绝后 Read 的结果必须与
// 调用前逐键一致。"store.go 注释："所有校验都在写之前完成，失败路径
// 没有任何副作用。"在已有多键内容的快照上，跑遍所有拒绝路径，逐键比对。
// 改坏方式：把 `m.store[key] = val` 挪到过期校验之前（先写后拒），
// 快照 diff 立即失败。
func TestRejectedWritesHaveNoSideEffectsAcrossKeys(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tok, _ := m.Acquire("A", 100)
	if err := m.Write(tok, "k1", "v1"); err != nil {
		t.Fatalf("预置 k1: %v", err)
	}
	if err := m.Write(tok, "k2", "v2"); err != nil {
		t.Fatalf("预置 k2: %v", err)
	}
	snapshot := map[string]string{"k1": "v1", "k2": "v2"}

	reject := func(name string, fn func() error) {
		t.Helper()
		if err := fn(); err == nil {
			t.Fatalf("%s 应当被拒绝", name)
		}
		for k, want := range snapshot {
			if got, ok := m.Read(k); !ok || got != want {
				t.Fatalf("%s 后 %s = %q,%v，快照被破坏（want %q）", name, k, got, ok, want)
			}
		}
		if _, ok := m.Read("new"); ok {
			t.Fatalf("%s 凭空写入了新键 new", name)
		}
	}

	reject("前一个 token", func() error { return m.Write(tok-1, "k1", "x") })
	reject("巨大未知 token", func() error { return m.Write(^uint64(0), "k2", "x") })
	reject("未知 token 写新键", func() error { return m.Write(123456, "new", "x") })
	clk.set(100)
	reject("恰好到期写已有键", func() error { return m.Write(tok, "k1", "x") })
	reject("恰好到期写新键", func() error { return m.Write(tok, "new", "x") })
}

// TestWriteWhenNoLeaseEverIssuedIsStale 钉 Write 注释：
// "token 不等于当前记录的 token（被抢占者的旧 token、从未发出过的值，
// 一律同等对待）"。从未 Acquire 过时 held=false，任何 token（包括
// 伪造的 1）都归 ErrStaleToken 而非 ErrLeaseExpired。
// 改坏方式：把 Write 的 `!m.held || token != m.token` 拆成 held 判
// ErrLeaseExpired，本测试失败。
func TestWriteWhenNoLeaseEverIssuedIsStale(t *testing.T) {
	m := New(newFakeClock(0).now)
	if err := m.Write(1, "k", "v"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("从未发租约时 Write = %v, want ErrStaleToken", err)
	}
	if _, ok := m.Read("k"); ok {
		t.Fatal("被拒写产生了键")
	}
}
