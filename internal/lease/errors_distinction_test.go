package lease

import (
	"errors"
	"testing"
)

// 钉 Renew 注释声明的三类哨兵必须能被 errors.Is 分别判定：
// "holder 不是当前记录的持有者（被抢占/从未持有/已释放）：ErrNotHolder
//  holder 匹配但 token 不符：ErrTokenMismatch
//  holder 与 token 都匹配但租约已过期：ErrLeaseExpired"
//
// 判别力台账补充：
//   - 把 Renew 中三处 return 的哨兵任意互换（例如被抢占分支返回
//     ErrLeaseExpired），TestRenewExpiredVsPreemptedAreDistinct 与
//     TestRenewPreemptedReturnsNotHolder 失败。
//   - 把 token 校验删掉（直接续约成功），
//     TestRenewTokenMismatchReturnsTokenMismatch 失败。
//   - Write 把 ErrStaleToken 细分为两类新哨兵，
//     TestWritePrevTokenAndUnknownTokenSameClass 失败。

// 被抢占后 Renew：holder 不是当前持有者 → ErrNotHolder。
// 钉 errors.go 注释："被抢占后调用 Renew/Release 返回 ErrNotHolder，
// 与'自己持有但已过期'返回的 ErrLeaseExpired 是不同类别。"
func TestRenewPreemptedReturnsNotHolder(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tokA, _ := m.Acquire("A", 100)

	// B 等 A 过期后接管（无需抢占仍有效的 A）。
	clock = 100
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("B Acquire: %v", err)
	}
	err := m.Renew("A", tokA, 100)
	if !errors.Is(err, ErrNotHolder) {
		t.Fatalf("被抢占后 Renew = %v, want ErrNotHolder", err)
	}
}

// 自己持有但已过期、且无人接管：holder 与 token 都匹配 →
// ErrLeaseExpired。与上一个测试构成"被抢占 vs 自己过期"的对照。
func TestRenewExpiredReturnsLeaseExpired(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 100)
	clock = 100

	err := m.Renew("A", tok, 100)
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("过期 Renew = %v, want ErrLeaseExpired", err)
	}
}

// holder 匹配（仍是记录持有者）但 token 是旧值 → ErrTokenMismatch。
// 触发方式：同一 holder 重复 Acquire 拿到新 token 后，用旧 token 续约。
func TestRenewTokenMismatchReturnsTokenMismatch(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	old, _ := m.Acquire("A", 100)
	new, err := m.Acquire("A", 100) // 自己仍有效，重新获取
	if err != nil {
		t.Fatalf("重复 Acquire: %v", err)
	}
	if new == old {
		t.Fatalf("新旧 token 不应相等: %d", new)
	}

	err = m.Renew("A", old, 100)
	if !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("旧 token Renew = %v, want ErrTokenMismatch", err)
	}
}

// 三个哨兵两两不同：errors.Is 必须能把三种恢复路径彻底区分开。
// 钉注释："两种情形的恢复策略不同……区分开便于调用方分别处理。"
func TestRenewExpiredVsPreemptedAreDistinct(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tokA, _ := m.Acquire("A", 100)

	clock = 100
	expiredErr := m.Renew("A", tokA, 100) // 自己过期、记录仍在

	tokB, _ := m.Acquire("B", 100) // B 接管
	preemptedErr := m.Renew("A", tokA, 100)

	_, _ = m.Acquire("B", 100) // B 重获，tokB 变旧
	mismatchErr := m.Renew("B", tokB, 100)

	cases := []struct {
		name      string
		err       error
		want      error
		alsoNotIs []error
	}{
		{"过期", expiredErr, ErrLeaseExpired, []error{ErrNotHolder, ErrTokenMismatch}},
		{"被抢占", preemptedErr, ErrNotHolder, []error{ErrLeaseExpired, ErrTokenMismatch}},
		{"token不符", mismatchErr, ErrTokenMismatch, []error{ErrLeaseExpired, ErrNotHolder}},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, tc.err, tc.want)
		}
		for _, other := range tc.alsoNotIs {
			if errors.Is(tc.err, other) {
				t.Errorf("%s: err = %v 不应同时匹配 %v", tc.name, tc.err, other)
			}
		}
	}
}

// 钉 ErrStaleToken 的注释决策："token 是'当前 token 的前一个值'
// 与'从未发出过的巨大值'归为同一类错误 ErrStaleToken。"
// 判别力：若有人把两种情形拆成不同哨兵（或对巨大值 panic），本测试失败。
func TestWritePrevTokenAndUnknownTokenSameClass(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	old, _ := m.Acquire("A", 100)
	cur, _ := m.Acquire("A", 100) // 重获后 old 成为"前一个值"，cur 当前有效

	errPrev := m.Write(old, "k", "x")
	errHuge := m.Write(^uint64(0), "k", "x")
	errZero := m.Write(0, "k", "x")

	for name, err := range map[string]error{
		"前一个token": errPrev,
		"巨大未知值":    errHuge,
		"零值":       errZero,
	} {
		if !errors.Is(err, ErrStaleToken) {
			t.Errorf("%s Write = %v, want ErrStaleToken", name, err)
		}
		if errors.Is(err, ErrLeaseExpired) {
			t.Errorf("%s Write 不应归类为 ErrLeaseExpired", name)
		}
	}

	// 对照组：当前有效 token 必须仍然能写，证明拒绝确实只针对旧/未知值。
	if err := m.Write(cur, "k", "ok"); err != nil {
		t.Fatalf("当前 token Write: %v", err)
	}
	if got, _ := m.Read("k"); got != "ok" {
		t.Fatalf("Read(k) = %q, want ok", got)
	}
}

// 从未持有过租约的 holder 调 Renew，同样属于 ErrNotHolder
// （注释列举的"从未持有"情形），且不得误报 token/过期类错误。
func TestRenewNeverHeldReturnsNotHolder(t *testing.T) {
	m := New(func() int64 { return 0 })
	err := m.Renew("ghost", 1, 100)
	if !errors.Is(err, ErrNotHolder) {
		t.Fatalf("从未持有者 Renew = %v, want ErrNotHolder", err)
	}
}
