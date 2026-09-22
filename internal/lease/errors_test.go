package lease

// 本文件钉住 Renew 的三类哨兵错误必须能被 errors.Is 两两区分，
// 以及 Release 对"已过期但 holder/token 都匹配"的幂等取舍，
// 还有 Write 对"前一个 token"与"从未发出的巨大值"的同类归并。

import (
	"errors"
	"testing"
)

// 三类错误的构造场景（实现注释 Renew 段原话）：
// "holder 不是当前记录的持有者（被抢占/从未持有/已释放）：ErrNotHolder"
// "holder 匹配但 token 不符：ErrTokenMismatch"
// "holder 与 token 都匹配但租约已过期：ErrLeaseExpired"

func renewErrors(t *testing.T) (notHolder, tokenMismatch, expired error) {
	t.Helper()
	var clock int64
	m := New(func() int64 { return clock })

	tokA, _ := m.Acquire("A", 100)
	clock = 100 // A 到期
	tokB, _ := m.Acquire("B", 100)

	// 被抢占者 A：holder 与当前记录（B）不符。
	notHolder = m.Renew("A", tokA, 100)
	// holder 是当前持有者 B，但用了 A 的旧 token。
	tokenMismatch = m.Renew("B", tokA, 100)
	// B 的 holder/token 都对，但租约已到期。
	clock = 200
	expired = m.Renew("B", tokB, 100)
	return
}

// 钉："被抢占后调用 Renew……与'租约已过期'、'token 不符'是三个不同的
// 哨兵错误……三者必须能被 errors.Is 分别判定。"
//
// 改坏对应（判别力）：
//   - 把 Renew 里被抢占分支的 ErrNotHolder 换成 ErrLeaseExpired，
//     TestSentinelNotHolderDistinctFromExpired 失败；
//   - 把 token 不符分支的 ErrTokenMismatch 换成 ErrNotHolder，
//     TestSentinelTokenMismatchDistinctFromNotHolder 失败。
func TestSentinelErrorsArePairwiseDistinct(t *testing.T) {
	notHolder, tokenMismatch, expired := renewErrors(t)

	if !errors.Is(notHolder, ErrNotHolder) {
		t.Fatalf("被抢占 Renew = %v, want ErrNotHolder", notHolder)
	}
	if !errors.Is(tokenMismatch, ErrTokenMismatch) {
		t.Fatalf("token 不符 Renew = %v, want ErrTokenMismatch", tokenMismatch)
	}
	if !errors.Is(expired, ErrLeaseExpired) {
		t.Fatalf("已过期 Renew = %v, want ErrLeaseExpired", expired)
	}

	// 两两互不为对方：errors.Is 对不同哨兵必须全部返回 false。
	if errors.Is(notHolder, ErrLeaseExpired) || errors.Is(notHolder, ErrTokenMismatch) {
		t.Errorf("ErrNotHolder 与另外两类混同: %v", notHolder)
	}
	if errors.Is(tokenMismatch, ErrNotHolder) || errors.Is(tokenMismatch, ErrLeaseExpired) {
		t.Errorf("ErrTokenMismatch 与另外两类混同: %v", tokenMismatch)
	}
	if errors.Is(expired, ErrNotHolder) || errors.Is(expired, ErrTokenMismatch) {
		t.Errorf("ErrLeaseExpired 与另外两类混同: %v", expired)
	}
}

// 单独钉"被抢占 vs 已过期"：两个场景 holder 都曾经合法持有，
// 唯一区别是记录是否已被他人覆盖。
func TestSentinelNotHolderDistinctFromExpired(t *testing.T) {
	notHolder, _, expired := renewErrors(t)
	if errors.Is(notHolder, ErrLeaseExpired) {
		t.Fatal("被抢占不应报 ErrLeaseExpired")
	}
	if errors.Is(expired, ErrNotHolder) {
		t.Fatal("自己过期不应报 ErrNotHolder")
	}
}

// 单独钉"token 不符 vs 不是 holder"：holder 匹配这一前提
// 必须先把错误类别收敛到 ErrTokenMismatch。
func TestSentinelTokenMismatchDistinctFromNotHolder(t *testing.T) {
	_, tokenMismatch, _ := renewErrors(t)
	if errors.Is(tokenMismatch, ErrNotHolder) {
		t.Fatal("holder 匹配但 token 错不应报 ErrNotHolder")
	}
}

// 钉 Release 的设计决策（manager.go Release 注释原话）：
// "holder 与 token 都匹配当前记录时，即使租约已经过期，
// Release 也算成功（幂等清理）。"
//
// 改坏对应（判别力）：若在 Release 的 token 校验之后插入
// `if now >= expiresAt { return ErrLeaseExpired }`，本测试失败。
func TestReleaseExpiredButMatchingSucceeds(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 10)

	clock = 10 // 恰好到期：记录还在，holder/token 都匹配
	if err := m.Release("A", tok); err != nil {
		t.Fatalf("过期后 Release = %v, want nil（幂等成功）", err)
	}
	// 释放终态：记录被清除，他人可立即获取且拿到更大的 token。
	tokB, err := m.Acquire("B", 10)
	if err != nil {
		t.Fatalf("过期 Release 后 B Acquire: %v", err)
	}
	if tokB <= tok {
		t.Fatalf("token 计数器被复位: %d <= %d", tokB, tok)
	}
}

// 同一段注释的另一面："但 holder 不匹配（已被他人抢占）仍返回
// ErrNotHolder，token 不符仍返回 ErrTokenMismatch，防止误清他人
// 的租约记录。"
func TestReleasePreemptedAndWrongTokenRejected(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tokA, _ := m.Acquire("A", 10)
	clock = 10
	tokB, _ := m.Acquire("B", 10)

	if err := m.Release("A", tokA); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("被抢占者 Release = %v, want ErrNotHolder", err)
	}
	if err := m.Release("B", tokA); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("holder 对 token 错 Release = %v, want ErrTokenMismatch", err)
	}
	// B 的租约记录必须完好无损，仍可正常使用。
	if err := m.Write(tokB, "k", "b"); err != nil {
		t.Fatalf("被误拒的 Release 不应影响 B, Write: %v", err)
	}
}

// 钉 Write 的设计决策（errors.go ErrStaleToken 注释原话）：
// "token 是'当前 token 的前一个值'与'从未发出过的巨大值'
// 归为同一类错误 ErrStaleToken。"
//
// 改坏对应（判别力）：若 Write 对旧 token 特判返回别的错误
// （例如 ErrTokenMismatch），本测试失败。
func TestWritePreviousAndUnknownTokenSameSentinel(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tokA, _ := m.Acquire("A", 100)
	tokB, _ := m.Acquire("A", 100) // 同 holder 重获，tokA 成为"前一个值"
	if tokB != tokA+1 {
		t.Fatalf("token = %d, want %d", tokB, tokA+1)
	}

	errPrev := m.Write(tokA, "k", "old")
	errHuge := m.Write(^uint64(0), "k", "fake")
	if !errors.Is(errPrev, ErrStaleToken) {
		t.Fatalf("前一个 token 写 = %v, want ErrStaleToken", errPrev)
	}
	if !errors.Is(errHuge, ErrStaleToken) {
		t.Fatalf("巨大未知 token 写 = %v, want ErrStaleToken", errHuge)
	}
	if !errors.Is(errPrev, errHuge) {
		t.Fatal("两类写必须归为同一个哨兵错误")
	}
	if _, ok := m.Read("k"); ok {
		t.Fatal("两次被拒的写都不得落盘")
	}
}
