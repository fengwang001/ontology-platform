package lease

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// 判别力台账（第四节）：下列每一处"改坏"都必须让指定测试失败。
//
//  1. Acquire 中把抢占判定 `now < m.expiresAt` 改成 `now <= m.expiresAt`
//     → TestInvariant5_ExpiryBoundaryAcquire 失败：到期时刻他人无法接管。
//  2. Acquire 中删除 `m.token++`（或挪到覆盖记录之后/返回之后）
//     → TestInvariant2_TokensStrictlyMonotonic 失败：token 重复为 1。
//  3. Release 末尾把计数器复位（增加 m.token = 0）
//     → TestInvariant2_TokensStrictlyMonotonic 失败：Release 后 token 回退。
//  4. Write 把 `!m.held || token != m.token` 提前返回的错误由
//     ErrStaleToken 换成 ErrLeaseExpired
//     → TestInvariant3_FencingRejectsStaleToken 失败：旧 token 错误类别变了。
//  5. Write 把过期判定 `now >= m.expiresAt` 改成 `now > m.expiresAt`
//     → TestInvariant5_WriteRejectedAtExpiryMoment 失败：到期那一刻写入成功。
//  6. Write 在失败路径前加任何 store 写入（把 m.store[key]=val 挪到校验前）
//     → TestInvariant4_RejectedWriteHasNoSideEffects 失败：快照出现脏值。
//  7. Acquire 删除他人有效持有时的 ErrLeaseHeld 提前返回
//     → TestInvariant1_MutualExclusion 失败：B 抢走了仍有效的租约。
//  8. Renew 把过期判定与 token 判定顺序对调（先判过期再判 token）
//     → TestRenewExpiredVsPreemptedAreDistinct 失败：抢占场景拿到
//     ErrLeaseExpired 而非 ErrNotHolder。
//  9. Renew 把 holder 不符的 ErrNotHolder 换成 ErrTokenMismatch
//     → TestRenewPreemptedReturnsNotHolder 失败。
// 10. Release 改成过期时返回 ErrLeaseExpired（不再幂等成功）
//     → TestReleaseExpiredMatchingSucceeds 失败。
// 11. Acquire 给同 holder 的重复 Acquire 改成返回 ErrLeaseHeld
//     → TestSameHolderReacquireIssuesNewToken 失败。

func snapshot(m *Manager) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]string, len(m.store))
	for k, v := range m.store {
		cp[k] = v
	}
	return cp
}

// 钉不变量 1（互斥）："任意时刻至多一个持有者的租约有效。"
// 持有者有效期间任何其他 holder 的 Acquire 都必须被拒；
// 到期后他人方可接管，且接管后旧持有者立即失效。
func TestInvariant1_MutualExclusion(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })

	tokA, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	if _, err := m.Acquire("B", 1); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("有效期间 B Acquire = %v, want ErrLeaseHeld", err)
	}
	if err := m.Write(tokA, "k", "a"); err != nil {
		t.Fatalf("A 写入: %v", err)
	}

	// 同一时刻只允许一个有效持有者：A 过期后 B 接管。
	clock = 100
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("到期后 B Acquire: %v", err)
	}
	if err := m.Write(tokB, "k", "b"); err != nil {
		t.Fatalf("B 写入: %v", err)
	}
	// A 旧 token 此刻绝不能再写，否则同一资源出现两个有效写者。
	if err := m.Write(tokA, "k", "evil"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("被抢占后 A 写 = %v, want ErrStaleToken", err)
	}
	if got, _ := m.Read("k"); got != "b" {
		t.Fatalf("Read(k) = %q, want b", got)
	}
}

// 钉不变量 2（token 严格单调）：注释原文"严格单调递增，永不回退。
// 即使租约被释放，计数器也不复位（不变量 2）。"
// 跨 Acquire、抢占、Release、重新 Acquire，token 必须连续递增不重复。
func TestInvariant2_TokensStrictlyMonotonic(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })

	var prev uint64
	issue := func(holder string, ttl int64) uint64 {
		t.Helper()
		tok, err := m.Acquire(holder, ttl)
		if err != nil {
			t.Fatalf("Acquire %s: %v", holder, err)
		}
		if tok <= prev {
			t.Fatalf("token %d 未严格大于上一个 %d", tok, prev)
		}
		prev = tok
		return tok
	}

	tokA := issue("A", 100)
	clock = 100
	tokB := issue("B", 100) // 抢占，token 仍递增
	if err := m.Release("B", tokB); err != nil {
		t.Fatalf("Release B: %v", err)
	}
	tokC := issue("C", 100) // Release 不复位计数器
	clock = 200
	tokA2 := issue("A", 100) // 旧 holder 名复用也不影响

	seen := map[uint64]bool{}
	for _, tok := range []uint64{tokA, tokB, tokC, tokA2} {
		if seen[tok] {
			t.Fatalf("token %d 重复发出", tok)
		}
		seen[tok] = true
	}
	if tokA != 1 || tokB != 2 || tokC != 3 || tokA2 != 4 {
		t.Fatalf("token 序列 = %d,%d,%d,%d, want 1,2,3,4", tokA, tokB, tokC, tokA2)
	}
}

// 钉不变量 3（fencing）："Write 只接受当前有效租约的 token；
// 被抢占者、已过期者、已释放者的旧 token 一律拒绝。"
func TestInvariant3_FencingRejectsStaleToken(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })

	tokA, _ := m.Acquire("A", 100)
	if err := m.Write(tokA, "k", "a"); err != nil {
		t.Fatalf("当前 token 写: %v", err)
	}

	// 已释放者的旧 token。
	if err := m.Release("A", tokA); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := m.Write(tokA, "k", "x"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("Release 后旧 token 写 = %v, want ErrStaleToken", err)
	}

	// 被抢占者的旧 token。
	tokB, _ := m.Acquire("B", 100)
	if err := m.Write(tokA, "k", "x"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("被抢占者旧 token 写 = %v, want ErrStaleToken", err)
	}

	// 已过期者的当前记录 token：过期是另一类错误 ErrLeaseExpired。
	clock = 100
	if err := m.Write(tokB, "k", "x"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("过期 token 写 = %v, want ErrLeaseExpired", err)
	}
}

// 钉不变量 4（无副作用）："任何被拒绝的 Write 都不会触碰 store
// （不变量 4）：所有校验都在写之前完成，失败路径没有任何副作用。"
func TestInvariant4_RejectedWriteHasNoSideEffects(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tokA, _ := m.Acquire("A", 100)
	if err := m.Write(tokA, "keep", "v"); err != nil {
		t.Fatalf("种子写入: %v", err)
	}

	// 被抢占者旧 token 的拒绝前后快照对比。
	clock = 100
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	for name, tok := range map[string]uint64{
		"被抢占者旧token": tokA,
		"前一个token值":  0, // 0 从未发出（当前 token 为 2）
		"从未发出的巨大值":   ^uint64(0),
	} {
		before := snapshot(m)
		if err := m.Write(tok, "evil", "x"); !errors.Is(err, ErrStaleToken) {
			t.Fatalf("%s: = %v, want ErrStaleToken", name, err)
		}
		if err := m.Write(tok, "keep", "OVERWRITE"); err == nil {
			t.Fatalf("%s: 覆盖写竟成功", name)
		}
		if after := snapshot(m); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: 被拒写改变了 store: before=%v after=%v", name, before, after)
		}
	}

	// 过期 token 的拒绝同样无副作用（覆盖已有键 + 写新键）。
	clock = 200
	before := snapshot(m)
	if err := m.Write(tokB, "keep", "OVERWRITE"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("过期覆盖写 = %v", err)
	}
	if err := m.Write(tokB, "brand-new", "x"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("过期写新键 = %v", err)
	}
	if after := snapshot(m); !reflect.DeepEqual(before, after) {
		t.Fatalf("过期被拒写产生了副作用: before=%v after=%v", before, after)
	}
}

// 钉不变量 5 + 注释"采用左闭右开区间 [start, expiresAt)"：
// now == expiresAt-1 仍可续约、写入，且他人不能接管。
func TestInvariant5_OneMsBeforeExpiryStillValid(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 100) // [0, 100)

	clock = 99
	if err := m.Renew("A", tok, 100); err != nil {
		t.Fatalf("到期前 1ms Renew: %v", err)
	}
	// 续约后区间变为 [99, 199)。
	if err := m.Write(tok, "k", "late"); err != nil {
		t.Fatalf("到期前 1ms Write: %v", err)
	}
	clock = 198
	if err := m.Write(tok, "k2", "v2"); err != nil {
		t.Fatalf("续约后到期前 1ms Write: %v", err)
	}
	if _, err := m.Acquire("B", 1); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("到期前他人 Acquire = %v, want ErrLeaseHeld", err)
	}
}

// 钉不变量 5：到期那一刻 Write 立即失效——
// "旧持有者在同一时刻的写必须被拒绝"。
func TestInvariant5_WriteRejectedAtExpiryMoment(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 100)
	clock = 100

	before := snapshot(m)
	if err := m.Write(tok, "k", "x"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("到期时刻 Write = %v, want ErrLeaseExpired", err)
	}
	if !reflect.DeepEqual(before, snapshot(m)) {
		t.Fatal("到期时刻被拒写产生了副作用")
	}
}

// 钉不变量 5 + 互斥边界论述："采用左闭右开区间 [start, expiresAt)
// 可以让新持有者在 now == expiresAt 时立刻 Acquire 成功"。
func TestInvariant5_ExpiryBoundaryAcquire(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tokA, _ := m.Acquire("A", 100)
	clock = 100

	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("到期时刻 B 应能 Acquire, got %v", err)
	}
	if tokB <= tokA {
		t.Fatalf("新 token %d 未大于旧 token %d", tokB, tokA)
	}
	if err := m.Write(tokB, fmt.Sprintf("k-%d", clock), "b"); err != nil {
		t.Fatalf("新持有者写: %v", err)
	}
}

// 钉注释"过期后记录仍保留，用于把 ErrLeaseExpired 与 ErrNotHolder
// 区分开"：过期未被接管时，同 holder+token 的 Renew 得到
// ErrLeaseExpired（记录还在），而非 ErrNotHolder。
func TestInvariant5_ExpiredRecordRetainedForClassification(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 100)
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("种子写: %v", err)
	}
	clock = 100

	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("过期 Renew = %v, want ErrLeaseExpired", err)
	}
	// Read 路径不受租约状态影响。
	if got, ok := m.Read("k"); !ok || got != "v" {
		t.Fatalf("过期后 Read(k) = %q,%v", got, ok)
	}
}
