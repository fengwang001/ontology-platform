package lease

import (
	"errors"
	"math"
	"testing"
)

// 本文件钉住 errors.go 中 ErrStaleToken 的设计决策原文：
//   "设计决策：token 是'当前 token 的前一个值'与'从未发出过的巨大值'
//    归为同一类错误 ErrStaleToken。理由：fencing 不变量只要求
//    '拒绝一切不等于当前有效 token 的写'，区分'曾经合法但已过时'
//    与'从未存在'对安全性没有帮助，反而泄露内部状态。"
// 以及 store.go Write 注释：
//   "任何被拒绝的 Write 都不会触碰 store（不变量 4）：
//    所有校验都在写之前完成，失败路径没有任何副作用。"
//
// 判别力（改坏实现 → 失败的测试）：
//   - 把 Write 里的 ErrStaleToken 换成 ErrTokenMismatch
//     → TestPreviousAndHugeTokenSameError 失败。
//   - 把 Write 的校验挪到 `m.store[key] = val` 之后（先写后验）
//     → TestRejectedWriteHasNoSideEffect 失败。
//   - 把 `token != m.token` 放宽成 `token > m.token`（只拒未来 token）
//     → TestPreviousAndHugeTokenSameError 失败（旧 token 会被放行）。

// "当前 token 的前一个值"与"从未发出过的巨大值"必须同属 ErrStaleToken。
func TestPreviousAndHugeTokenSameError(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok1, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire #1: %v", err)
	}
	tok2, err := m.Acquire("A", 100) // tok1 从此过时
	if err != nil {
		t.Fatalf("Acquire #2: %v", err)
	}
	errPrev := m.Write(tok2-1, "k", "x")         // 曾经合法、现已过时的 token
	errHuge := m.Write(math.MaxUint64, "k", "x") // 从未发出过的巨大值
	if !errors.Is(errPrev, ErrStaleToken) {
		t.Fatalf("previous token: %v, want ErrStaleToken", errPrev)
	}
	if !errors.Is(errHuge, ErrStaleToken) {
		t.Fatalf("huge token: %v, want ErrStaleToken", errHuge)
	}
	// 两者必须能用同一个 errors.Is 判定归为一类。
	if errors.Is(errPrev, ErrLeaseExpired) || errors.Is(errHuge, ErrLeaseExpired) {
		t.Fatal("ErrStaleToken 不得与 ErrLeaseExpired 混淆")
	}
	_ = tok1
}

// 不变量 4：被拒绝的 Write 不得改变资源内容，逐键核对。
func TestRejectedWriteHasNoSideEffect(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := m.Write(tok, "k1", "v1"); err != nil {
		t.Fatalf("seed Write: %v", err)
	}
	before1, ok1 := m.Read("k1")
	_, ok2before := m.Read("k2")

	// 三种被拒路径：stale token、过期 token、巨大 token。
	clk.set(200) // 先让租约过期
	rejected := []error{
		m.Write(tok+1, "k1", "evil"),          // stale（记录已无效）
		m.Write(tok, "k1", "evil"),            // token 匹配但已过期
		m.Write(math.MaxUint64, "k2", "evil"), // 未知 token 写新键
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("rejected[%d] 应为非 nil 错误", i)
		}
	}
	if got, ok := m.Read("k1"); got != before1 || ok != ok1 {
		t.Fatalf("k1 被改动: (%q,%v) -> (%q,%v)", before1, ok1, got, ok)
	}
	if _, ok := m.Read("k2"); ok != ok2before {
		t.Fatalf("k2 不应存在（不存在的键也不得被写入）")
	}
}

// 不变量 2 + 3：token 在多次 Acquire/Release 循环中严格递增、
// 永不重复，且只有最新 token 能写。
func TestTokenStrictlyMonotonicAcrossCycles(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	seen := map[uint64]bool{}
	var prev uint64
	for i := 0; i < 20; i++ {
		tok, err := m.Acquire("A", 50)
		if err != nil {
			t.Fatalf("Acquire #%d: %v", i, err)
		}
		if tok <= prev {
			t.Fatalf("token 回退或重复: %d -> %d", prev, tok)
		}
		if seen[tok] {
			t.Fatalf("token %d 重复发出", tok)
		}
		seen[tok] = true
		prev = tok
		if i%2 == 0 {
			if err := m.Release("A", tok); err != nil {
				t.Fatalf("Release #%d: %v", i, err)
			}
		} else {
			clk.advance(50) // 让它过期，靠下一轮 Acquire 覆盖
		}
	}
}

// 不变量 3 的正面：当前有效 token 的写必须成功，
// 且 Read 能读回（防止把校验改紧到连合法写都拒）。
func TestCurrentTokenWriteSucceeds(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("Write with current token = %v, want nil", err)
	}
	if got, ok := m.Read("k"); !ok || got != "v" {
		t.Fatalf("Read(k) = %q,%v, want v,true", got, ok)
	}
}
