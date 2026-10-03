package lessor

import (
	"errors"
	"reflect"
	"testing"
)

func exampleConfig() Config {
	return Config{MinTTL: 5, MaxTTL: 1000, E: 3, R: 2, Kmax: 4}
}

func mustNew(t *testing.T, cfg Config) *Lessor {
	t.Helper()
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v) unexpected error: %v", cfg, err)
	}
	return l
}

func mustErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustTTL(t *testing.T, l *Lessor, id, now, want int64) {
	t.Helper()
	got, err := l.TTL(id, now)
	if err != nil {
		t.Fatalf("TTL(%d,%d) unexpected error: %v", id, now, err)
	}
	if got != want {
		t.Fatalf("TTL(%d,%d) = %d, want %d", id, now, got, want)
	}
}

func mustTick(t *testing.T, l *Lessor, now int64, want []Revoked) {
	t.Helper()
	got, err := l.Tick(now)
	if err != nil {
		t.Fatalf("Tick(%d) unexpected error: %v", now, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tick(%d) = %+v, want %+v", now, got, want)
	}
}

// TestExampleBacklog 复现题目示例一：限速撤销、积压、到期判定与键移动。
func TestExampleBacklog(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))

	if g, err := l.Grant(1, 10, 0); err != nil || g != 10 {
		t.Fatalf("Grant(1,10,0) = (%d, %v), want (10, nil)", g, err)
	}
	if g, err := l.Grant(2, 2, 1); err != nil || g != 5 {
		t.Fatalf("Grant(2,2,1) = (%d, %v), want g=5 (max(2,MinTTL))", g, err)
	}
	if g, err := l.Grant(3, 8, 1); err != nil || g != 8 {
		t.Fatalf("Grant(3,8,1) = (%d, %v), want (8, nil)", g, err)
	}
	if g, err := l.Grant(4, 5, 2); err != nil || g != 5 {
		t.Fatalf("Grant(4,5,2) = (%d, %v), want (5, nil)", g, err)
	}

	mustNoErr(t, l.Attach("a", 1, 2))
	mustNoErr(t, l.Attach("b", 2, 2))
	mustNoErr(t, l.Attach("c", 2, 2))
	mustNoErr(t, l.Attach("a", 3, 3)) // a 从租约 1 移到租约 3

	// x<=9 的有 2(6)、4(7)、3(9)，R=2 只撤销 2 与 4，3 留作积压。
	mustTick(t, l, 9, []Revoked{
		{ID: 2, Keys: []string{"b", "c"}},
		{ID: 4, Keys: []string{}},
	})

	// 积压中的租约 Renew 报已过期，但不被移除。
	_, err := l.Renew(3, 9)
	mustErrIs(t, err, ErrExpired)

	// 积压的 3(9) 先于到期的 1(10)，两个都撤销。
	mustTick(t, l, 10, []Revoked{
		{ID: 3, Keys: []string{"a"}},
		{ID: 1, Keys: []string{}},
	})
}

// TestExampleFailover 复现题目示例二：检查点剩余寿命在切换后精确复现。
func TestExampleFailover(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))
	if g, err := l.Grant(7, 100, 0); err != nil || g != 100 {
		t.Fatalf("Grant(7,100,0) = (%d, %v), want (100, nil)", g, err)
	}
	mustNoErr(t, l.Checkpoint(30)) // sv = 100-30 = 70
	mustNoErr(t, l.Demote(40))
	mustTTL(t, l, 7, 45, 70)    // 从态返回 sv
	mustNoErr(t, l.Promote(50)) // x = 50+3+70 = 123
	mustTTL(t, l, 7, 50, 73)
	if g, err := l.Renew(7, 60); err != nil || g != 100 {
		t.Fatalf("Renew(7,60) = (%d, %v), want (100, nil)", g, err)
	}
	mustTTL(t, l, 7, 60, 100) // x=160，sv 已清零
}

// TestFailoverWithoutCheckpoint 无检查点时 Promote 用完整 g 推导到期时刻。
func TestFailoverWithoutCheckpoint(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))
	if _, err := l.Grant(7, 100, 0); err != nil {
		t.Fatal(err)
	}
	mustNoErr(t, l.Demote(40))
	mustTTL(t, l, 7, 45, 100)   // sv=0，从态返回 g
	mustNoErr(t, l.Promote(50)) // x = 50+3+100 = 153
	mustTTL(t, l, 7, 50, 103)
}

// TestEffectiveTTL Renew 恢复的是 g 而非调用时的 ttl。
func TestEffectiveTTL(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))
	g, err := l.Grant(1, 2, 0) // ttl=2 < MinTTL=5，g=5，x=5
	if err != nil || g != 5 {
		t.Fatalf("Grant(1,2,0) = (%d, %v), want g=5", g, err)
	}
	mustTTL(t, l, 1, 0, 5)
	g, err = l.Renew(1, 3) // x = 3+5 = 8，恢复 g=5 而非 ttl=2
	if err != nil || g != 5 {
		t.Fatalf("Renew(1,3) = (%d, %v), want g=5", g, err)
	}
	mustTTL(t, l, 1, 3, 5)
	mustTTL(t, l, 1, 6, 2)
}

// TestExpiryBoundary x 恰等于 now 即已过期。
func TestExpiryBoundary(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))
	if _, err := l.Grant(1, 10, 0); err != nil { // x=10
		t.Fatal(err)
	}
	mustTTL(t, l, 1, 10, 0) // 主态 max(x-now, 0)
	_, err := l.Renew(1, 10)
	mustErrIs(t, err, ErrExpired)
	mustErrIs(t, l.Attach("k", 1, 10), ErrExpired)
	mustTick(t, l, 10, []Revoked{{ID: 1, Keys: []string{}}})
}

// TestSameXOrderByID 到期时刻相同按 id 升序撤销。
func TestSameXOrderByID(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))
	for _, id := range []int64{3, 1, 2} {
		if _, err := l.Grant(id, 10, 0); err != nil { // 全部 x=10
			t.Fatal(err)
		}
	}
	mustTick(t, l, 10, []Revoked{
		{ID: 1, Keys: []string{}},
		{ID: 2, Keys: []string{}},
	})
	mustTick(t, l, 10, []Revoked{{ID: 3, Keys: []string{}}})
}

// TestRateLimitBacklog R 限速下积压跨多次 Tick。
func TestRateLimitBacklog(t *testing.T) {
	l := mustNew(t, exampleConfig()) // R=2
	mustNoErr(t, l.Promote(0))
	for id := int64(1); id <= 5; id++ {
		if _, err := l.Grant(id, 10, 0); err != nil { // 全部 x=10
			t.Fatal(err)
		}
	}
	mustTick(t, l, 10, []Revoked{{ID: 1, Keys: []string{}}, {ID: 2, Keys: []string{}}})
	mustTick(t, l, 10, []Revoked{{ID: 3, Keys: []string{}}, {ID: 4, Keys: []string{}}})
	mustTick(t, l, 10, []Revoked{{ID: 5, Keys: []string{}}})
	got, err := l.Tick(10)
	mustNoErr(t, err)
	if len(got) != 0 {
		t.Fatalf("Tick(10) = %+v, want empty", got)
	}
}

// TestKeyMove 键移动后原租约撤销不带走它；键升序返回。
func TestKeyMove(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))
	if _, err := l.Grant(1, 10, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Grant(2, 10, 0); err != nil {
		t.Fatal(err)
	}
	mustNoErr(t, l.Attach("k", 1, 0))
	mustNoErr(t, l.Attach("k", 2, 0)) // k 从租约 1 移到租约 2
	keys, err := l.Revoke(1, 0)
	mustNoErr(t, err)
	if len(keys) != 0 {
		t.Fatalf("Revoke(1) keys = %v, want empty (k 已移走)", keys)
	}
	// 键仍在租约 2 上。
	mustNoErr(t, l.Attach("c", 2, 0))
	mustNoErr(t, l.Attach("a", 2, 0))
	mustNoErr(t, l.Attach("b", 2, 0))
	keys, err = l.Revoke(2, 0)
	mustNoErr(t, err)
	if !reflect.DeepEqual(keys, []string{"a", "b", "c", "k"}) {
		t.Fatalf("Revoke(2) keys = %v, want [a b c k] 升序", keys)
	}
}

// TestCheckpointOnlyUnexpired Checkpoint 只写未过期租约。
func TestCheckpointOnlyUnexpired(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))
	if _, err := l.Grant(1, 10, 0); err != nil { // x=10
		t.Fatal(err)
	}
	if _, err := l.Grant(2, 20, 0); err != nil { // x=20
		t.Fatal(err)
	}
	mustNoErr(t, l.Checkpoint(15)) // 租约 1 已过期不写，租约 2 sv=5
	mustNoErr(t, l.Demote(15))
	mustTTL(t, l, 1, 15, 10) // sv=0，从态回落到 g
	mustTTL(t, l, 2, 15, 5)  // sv=5
	mustNoErr(t, l.Promote(20))
	mustTTL(t, l, 1, 20, 13) // x = 20+3+10 = 33
	mustTTL(t, l, 2, 20, 8)  // x = 20+3+5 = 28
}

// TestRenewClearsSaved Renew 清 sv，切换后得到完整 g。
func TestRenewClearsSaved(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))
	if _, err := l.Grant(1, 10, 0); err != nil { // x=10
		t.Fatal(err)
	}
	mustNoErr(t, l.Checkpoint(5))            // sv=5
	if _, err := l.Renew(1, 6); err != nil { // x=16，sv=0
		t.Fatal(err)
	}
	mustNoErr(t, l.Demote(6))
	mustTTL(t, l, 1, 6, 10)     // sv 已清，从态返回 g
	mustNoErr(t, l.Promote(10)) // x = 10+3+10 = 23（完整 g 而非旧 sv）
	mustTTL(t, l, 1, 10, 13)
}

// TestPromoteBacklogRegainsTime Promote 的 x 含 E，积压租约重新获得时间。
func TestPromoteBacklogRegainsTime(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))
	if _, err := l.Grant(1, 5, 0); err != nil { // x=5
		t.Fatal(err)
	}
	mustNoErr(t, l.Demote(10))  // 租约已过期但未被 Tick 撤销，仍在积压
	mustNoErr(t, l.Promote(20)) // x = 20+3+5 = 28
	mustTTL(t, l, 1, 20, 8)
	got, err := l.Tick(27)
	mustNoErr(t, err)
	if len(got) != 0 {
		t.Fatalf("Tick(27) = %+v, want empty", got)
	}
	mustTick(t, l, 28, []Revoked{{ID: 1, Keys: []string{}}})
}

// TestRoleErrors 角色错误：从态拒绝写操作，重复 Promote/Demote 报错。
func TestRoleErrors(t *testing.T) {
	l := mustNew(t, exampleConfig())
	// 初始为从。
	if _, err := l.Grant(1, 10, 0); err != nil {
		mustErrIs(t, err, ErrRole)
	}
	if _, err := l.Renew(1, 0); err != nil {
		mustErrIs(t, err, ErrRole)
	}
	mustErrIs(t, l.Attach("k", 1, 0), ErrRole)
	if _, err := l.Tick(0); err != nil {
		mustErrIs(t, err, ErrRole)
	}
	if _, err := l.Revoke(1, 0); err != nil {
		mustErrIs(t, err, ErrRole)
	}
	mustErrIs(t, l.Checkpoint(0), ErrRole)
	mustErrIs(t, l.Demote(0), ErrRole) // 已是从
	mustNoErr(t, l.Promote(0))
	mustErrIs(t, l.Promote(0), ErrRole) // 已是主
	mustNoErr(t, l.Demote(0))
}

// TestCheckOrder 判定顺序：参数非法 > 时间非法 > 角色错误。
func TestCheckOrder(t *testing.T) {
	l := mustNew(t, exampleConfig()) // 从态
	// 参数非法优先于时间非法与角色错误。
	if _, err := l.Grant(0, 0, -1); err != nil {
		mustErrIs(t, err, ErrInvalidParam)
	}
	// 时间非法优先于角色错误。
	if _, err := l.Grant(1, 1, -1); err != nil {
		mustErrIs(t, err, ErrInvalidTime)
	}
	// 均合法时才报角色错误。
	if _, err := l.Grant(1, 1, 0); err != nil {
		mustErrIs(t, err, ErrRole)
	}
}

// TestRejectedNoStateChange 被拒绝的操作不改变租约、键、T 与积压。
func TestRejectedNoStateChange(t *testing.T) {
	l := mustNew(t, exampleConfig())
	mustNoErr(t, l.Promote(0))
	if _, err := l.Grant(1, 10, 0); err != nil { // x=10
		t.Fatal(err)
	}
	if _, err := l.Grant(2, 20, 0); err != nil { // x=20
		t.Fatal(err)
	}
	mustNoErr(t, l.Attach("k", 1, 0))
	mustNoErr(t, l.Checkpoint(5)) // T=5，租约 1 sv=5，租约 2 sv=15

	assertT := func(want int64) {
		t.Helper()
		if l.T != want {
			t.Fatalf("T = %d, want %d (被拒绝的操作不得推进 T)", l.T, want)
		}
	}
	reject := func(err error, target error) {
		t.Helper()
		mustErrIs(t, err, target)
		assertT(5)
	}

	_, err := l.Grant(0, 10, 5)
	reject(err, ErrInvalidParam) // id < 1
	_, err = l.Grant(3, 0, 5)
	reject(err, ErrInvalidParam) // ttl 越界
	_, err = l.Grant(3, 1001, 5)
	reject(err, ErrInvalidParam)                // ttl > MaxTTL
	reject(l.Attach("", 1, 5), ErrInvalidParam) // 空键
	_, err = l.Grant(3, 10, 4)
	reject(err, ErrInvalidTime) // now < T
	_, err = l.Grant(3, 10, 1_000_000_000_000_001)
	reject(err, ErrInvalidTime) // now > 10^15
	_, err = l.Grant(1, 10, 5)
	reject(err, ErrLeaseExists)
	_, err = l.Renew(9, 5)
	reject(err, ErrLeaseNotFound)
	reject(l.Attach("k2", 9, 5), ErrLeaseNotFound)
	_, err = l.Revoke(9, 5)
	reject(err, ErrLeaseNotFound)
	_, err = l.TTL(9, 5)
	reject(err, ErrLeaseNotFound)

	// 租约满：先判满，不摘除原挂靠。
	mustNoErr(t, l.Attach("a", 2, 5))
	mustNoErr(t, l.Attach("b", 2, 5))
	mustNoErr(t, l.Attach("c", 2, 5))
	mustNoErr(t, l.Attach("d", 2, 5)) // 租约 2 已满（Kmax=4）
	reject(l.Attach("k", 2, 5), ErrFull)
	// k 仍挂在租约 1 上：对租约 1 重复挂靠是无操作成功。
	mustNoErr(t, l.Attach("k", 1, 5))
	assertT(5)

	// 状态未变：租约 1 的键仍是 {k}，租约 2 的键仍是 {a,b,c,d}。
	keys, err := l.Revoke(1, 5)
	mustNoErr(t, err)
	if !reflect.DeepEqual(keys, []string{"k"}) {
		t.Fatalf("Revoke(1) keys = %v, want [k]", keys)
	}
	keys, err = l.Revoke(2, 5)
	mustNoErr(t, err)
	if !reflect.DeepEqual(keys, []string{"a", "b", "c", "d"}) {
		t.Fatalf("Revoke(2) keys = %v, want [a b c d]", keys)
	}
}

// TestConfigValidation 构造参数越界整体拒绝。
func TestConfigValidation(t *testing.T) {
	valid := exampleConfig()
	cases := []Config{
		{MinTTL: 0, MaxTTL: 1000, E: 3, R: 2, Kmax: 4},
		{MinTTL: 1_000_001, MaxTTL: 2_000_000, E: 3, R: 2, Kmax: 4},
		{MinTTL: 5, MaxTTL: 4, E: 3, R: 2, Kmax: 4}, // MaxTTL < MinTTL
		{MinTTL: 5, MaxTTL: 1_000_000_001, E: 3, R: 2, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1000, E: -1, R: 2, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1000, E: 1_000_000_001, R: 2, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1000, E: 3, R: 0, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1000, E: 3, R: 1_000_001, Kmax: 4},
		{MinTTL: 5, MaxTTL: 1000, E: 3, R: 2, Kmax: 0},
		{MinTTL: 5, MaxTTL: 1000, E: 3, R: 2, Kmax: 1_000_001},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d: New(%+v) err = %v, want ErrInvalidParam", i, cfg, err)
		}
	}
	if _, err := New(valid); err != nil {
		t.Fatalf("New(valid) unexpected error: %v", err)
	}
	// 边界值可用。
	if _, err := New(Config{MinTTL: 1, MaxTTL: 1, E: 0, R: 1, Kmax: 1}); err != nil {
		t.Fatalf("New(min boundary) unexpected error: %v", err)
	}
	if _, err := New(Config{MinTTL: 1_000_000, MaxTTL: 1_000_000_000, E: 1_000_000_000, R: 1_000_000, Kmax: 1_000_000}); err != nil {
		t.Fatalf("New(max boundary) unexpected error: %v", err)
	}
}
