package lessor

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

var exampleCfg = Config{MinTTL: 5, MaxTTL: 1000, E: 3, R: 2, Kmax: 4}

func mustNew(t *testing.T, cfg Config) *Lessor {
	t.Helper()
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v) 失败: %v", cfg, err)
	}
	return l
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
}

func mustGrant(t *testing.T, l *Lessor, id, ttl, now int64) int64 {
	t.Helper()
	g, err := l.Grant(id, ttl, now)
	if err != nil {
		t.Fatalf("Grant(%d,%d,%d) 意外错误: %v", id, ttl, now, err)
	}
	return g
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("期望错误 %v，实际 %v", want, got)
	}
}

func wantRevoked(t *testing.T, got []Revoked, want ...Revoked) {
	t.Helper()
	if len(got) == 0 {
		got = nil
	}
	if len(want) == 0 {
		want = nil
	}
	for i := range want {
		if want[i].Keys == nil {
			want[i].Keys = []string{}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("撤销序列不符:\n 实际 %+v\n 期望 %+v", got, want)
	}
}

// 规格正文中的第一个例子。
func TestExampleScenario(t *testing.T) {
	l := mustNew(t, exampleCfg)
	must(t, l.Promote(0))
	if g := mustGrant(t, l, 1, 10, 0); g != 10 {
		t.Fatalf("Grant(1,10,0) g=%d，期望 10", g)
	}
	if g := mustGrant(t, l, 2, 2, 1); g != 5 {
		t.Fatalf("Grant(2,2,1) g=%d，期望 max(2,5)=5", g)
	}
	mustGrant(t, l, 3, 8, 1)
	mustGrant(t, l, 4, 5, 2)
	must(t, l.Attach("a", 1, 2))
	must(t, l.Attach("b", 2, 2))
	must(t, l.Attach("c", 2, 2))
	must(t, l.Attach("a", 3, 3)) // a 从租约 1 移到租约 3

	got, err := l.Tick(9)
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 2, Keys: []string{"b", "c"}}, Revoked{ID: 4})

	_, err = l.Renew(3, 9)
	wantErr(t, err, ErrLeaseExpired) // 积压中的租约续约被拒

	got, err = l.Tick(10)
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 3, Keys: []string{"a"}}, Revoked{ID: 1})
}

// 规格正文中的第二个例子：检查点 / 降级 / 提升。
func TestCheckpointPromoteExample(t *testing.T) {
	l := mustNew(t, exampleCfg)
	must(t, l.Promote(0))
	mustGrant(t, l, 7, 100, 0) // x=100
	must(t, l.Checkpoint(30))  // sv=70
	must(t, l.Demote(40))
	if v, err := l.TTL(7, 45); err != nil || v != 70 {
		t.Fatalf("从态 TTL(7,45)=%d,%v，期望 70", v, err)
	}
	must(t, l.Promote(50)) // x = 50+3+70 = 123
	if v, err := l.TTL(7, 50); err != nil || v != 73 {
		t.Fatalf("TTL(7,50)=%d,%v，期望 73", v, err)
	}
	if g, err := l.Renew(7, 60); err != nil || g != 100 {
		t.Fatalf("Renew(7,60) g=%d,%v，期望 100", g, err)
	}
	if v, err := l.TTL(7, 60); err != nil || v != 100 {
		t.Fatalf("Renew 后 TTL(7,60)=%d,%v，期望 100（x=160）", v, err)
	}
}

// g 取 max(ttl, MinTTL)，Renew 恢复的是 g 而非调用时的 ttl。
func TestEffectiveTTLAndRenew(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 10, MaxTTL: 100, E: 0, R: 10, Kmax: 10})
	must(t, l.Promote(0))
	if g := mustGrant(t, l, 1, 3, 0); g != 10 {
		t.Fatalf("g=%d，期望 max(3,10)=10", g)
	}
	if g := mustGrant(t, l, 2, 50, 0); g != 50 {
		t.Fatalf("g=%d，期望 50", g)
	}
	if g, err := l.Renew(1, 5); err != nil || g != 10 {
		t.Fatalf("Renew 返回 g=%d,%v，期望 10（有效 TTL 而非授予的 3）", g, err)
	}
	if v, _ := l.TTL(1, 5); v != 10 {
		t.Fatalf("Renew 后 TTL=%d，期望 10", v)
	}
}

// x 恰等于 now 即已过期。
func TestExpiryBoundary(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 100, E: 0, R: 10, Kmax: 10})
	must(t, l.Promote(0))
	mustGrant(t, l, 1, 10, 0) // x=10
	must(t, l.Attach("k", 1, 5))
	wantErr(t, l.Attach("z", 1, 10), ErrLeaseExpired) // x == now
	_, err := l.Renew(1, 10)
	wantErr(t, err, ErrLeaseExpired)
	got, err := l.Tick(10)
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 1, Keys: []string{"k"}})
}

// 积压中的租约 Renew 被拒，却仍被下次 Tick 按 (x, id) 序撤销。
func TestBacklogRenewRejectedThenTickOrder(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 100, E: 0, R: 1, Kmax: 10})
	must(t, l.Promote(0))
	mustGrant(t, l, 1, 5, 0) // x=5
	mustGrant(t, l, 2, 5, 0) // x=5
	mustGrant(t, l, 3, 6, 0) // x=6

	got, err := l.Tick(6) // R=1，只撤销 id 最小的 1，2、3 积压
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 1})

	_, err = l.Renew(2, 6)
	wantErr(t, err, ErrLeaseExpired) // 积压中，x=5 <= 6

	got, err = l.Tick(7) // 同 x 按 id 升序：先 2 后 3
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 2})
	got, err = l.Tick(7)
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 3})
}

// 同 x 按 id 升序撤销。
func TestSameExpiryOrderedByID(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 100, E: 0, R: 10, Kmax: 10})
	must(t, l.Promote(0))
	mustGrant(t, l, 9, 5, 0)
	mustGrant(t, l, 3, 5, 0)
	mustGrant(t, l, 7, 5, 0)
	got, err := l.Tick(5)
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 3}, Revoked{ID: 7}, Revoked{ID: 9})
}

// R 限速下积压跨多次 Tick。
func TestRateLimitBacklog(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 100, E: 0, R: 2, Kmax: 10})
	must(t, l.Promote(0))
	for id := int64(1); id <= 5; id++ {
		mustGrant(t, l, id, 4, 0) // 全部 x=4
	}
	got, err := l.Tick(4)
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 1}, Revoked{ID: 2})
	got, err = l.Tick(4)
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 3}, Revoked{ID: 4})
	got, err = l.Tick(4)
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 5})
	got, err = l.Tick(4)
	must(t, err)
	wantRevoked(t, got)
}

// 键移动后原租约撤销不带走它；被撤销租约的键不再挂靠。
func TestKeyMoveNotTakenByOldLease(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 100, E: 0, R: 10, Kmax: 10})
	must(t, l.Promote(0))
	mustGrant(t, l, 1, 5, 0)
	mustGrant(t, l, 2, 50, 0)
	must(t, l.Attach("k", 1, 0))
	must(t, l.Attach("k", 2, 1)) // k 移到租约 2
	got, err := l.Tick(5)        // 撤销租约 1，不应带走 k
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 1})
	if _, ok := l.keyTo["k"]; !ok || l.keyTo["k"] != 2 {
		t.Fatalf("k 应仍挂在租约 2，实际 keyTo=%v", l.keyTo)
	}
	got, err = l.Tick(50)
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 2, Keys: []string{"k"}})
	if _, ok := l.keyTo["k"]; ok {
		t.Fatalf("租约 2 撤销后 k 不应再挂靠")
	}
}

// 键升序返回；Attach 已满先判不摘除。
func TestKeysSortedAndFull(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 100, E: 0, R: 10, Kmax: 2})
	must(t, l.Promote(0))
	mustGrant(t, l, 1, 50, 0)
	mustGrant(t, l, 2, 50, 0)
	must(t, l.Attach("b", 1, 0))
	must(t, l.Attach("a", 1, 0))
	must(t, l.Attach("c", 2, 0))
	wantErr(t, l.Attach("c", 1, 1), ErrLeaseFull) // 满：先判，c 仍挂在租约 2
	if l.keyTo["c"] != 2 {
		t.Fatalf("判满不得摘除，c 应仍挂在租约 2")
	}
	must(t, l.Attach("a", 1, 1)) // 已在该租约上，无操作成功
	keys, err := l.Revoke(1, 2)
	must(t, err)
	if !reflect.DeepEqual(keys, []string{"a", "b"}) {
		t.Fatalf("Revoke 返回键 %v，期望升序 [a b]", keys)
	}
	wantErr(t, l.Attach("c", 1, 4), ErrLeaseMissing)
}

// Checkpoint 只写未过期租约。
func TestCheckpointOnlyUnexpired(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 100, E: 0, R: 10, Kmax: 10})
	must(t, l.Promote(0))
	mustGrant(t, l, 1, 10, 0) // x=10
	mustGrant(t, l, 2, 5, 0)  // x=5，Checkpoint(7) 时已过期
	must(t, l.Checkpoint(7))
	if sv := l.leases[1].sv; sv != 3 {
		t.Fatalf("租约 1 sv=%d，期望 3", sv)
	}
	if sv := l.leases[2].sv; sv != 0 {
		t.Fatalf("已过期租约 2 sv=%d，期望保持 0", sv)
	}
}

// Renew 清 sv，使切换后得到完整 g。
func TestRenewClearsCheckpoint(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 100, E: 2, R: 10, Kmax: 10})
	must(t, l.Promote(0))
	mustGrant(t, l, 1, 40, 0) // x=40
	must(t, l.Checkpoint(10)) // sv=30
	if g, err := l.Renew(1, 20); err != nil || g != 40 {
		t.Fatalf("Renew g=%d,%v，期望 40", g, err)
	}
	if sv := l.leases[1].sv; sv != 0 {
		t.Fatalf("Renew 应清零 sv，实际 %d", sv)
	}
	must(t, l.Demote(30))
	must(t, l.Promote(35)) // sv=0，用 g：x = 35+2+40 = 77
	if v, _ := l.TTL(1, 35); v != 42 {
		t.Fatalf("TTL=%d，期望 77-35=42（完整 g 而非旧 sv=30）", v)
	}
}

// Promote 的 x 含 E，且积压租约重新获得时间。
func TestPromoteBacklogAndGrace(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 100, E: 5, R: 1, Kmax: 10})
	must(t, l.Promote(0))
	mustGrant(t, l, 1, 8, 0) // x=8
	mustGrant(t, l, 2, 8, 0) // x=8
	must(t, l.Checkpoint(4)) // sv=4（两个租约）

	got, err := l.Tick(8) // R=1：撤销 1，2 积压
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 1})

	_, err = l.Renew(2, 8)
	wantErr(t, err, ErrLeaseExpired) // 积压中续约被拒

	must(t, l.Demote(9))
	if v, err := l.TTL(2, 9); err != nil || v != 4 {
		t.Fatalf("从态 TTL(2,9)=%d,%v，期望 sv=4", v, err)
	}
	must(t, l.Promote(10)) // 积压租约重获时间：x = 10+5+4 = 19
	if v, err := l.TTL(2, 10); err != nil || v != 9 {
		t.Fatalf("TTL(2,10)=%d,%v，期望 9（x=19 含 E=5 与 sv=4）", v, err)
	}
	got, err = l.Tick(18)
	must(t, err)
	wantRevoked(t, got) // x=19，未到期
	got, err = l.Tick(19)
	must(t, err)
	wantRevoked(t, got, Revoked{ID: 2})
}

// 从态 TTL 口径：sv>0 取 sv，否则取 g；只读不改水位 T。
func TestFollowerTTL(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 3, MaxTTL: 100, E: 0, R: 10, Kmax: 10})
	must(t, l.Promote(0))
	mustGrant(t, l, 1, 50, 0) // g=50
	mustGrant(t, l, 2, 2, 0)  // g=max(2,3)=3
	must(t, l.Checkpoint(10)) // sv: 1->40, 2->0（x=3 已过期）
	must(t, l.Demote(20))
	if v, err := l.TTL(1, 25); err != nil || v != 40 {
		t.Fatalf("从态 TTL(1)=%d,%v，期望 sv=40", v, err)
	}
	if v, err := l.TTL(2, 25); err != nil || v != 3 {
		t.Fatalf("从态 TTL(2)=%d,%v，期望 g=3（sv=0 时取 g）", v, err)
	}
	if l.now != 20 {
		t.Fatalf("TTL 为只读，不应改水位 T，实际 T=%d", l.now)
	}
}

// 角色错误：要求主而当前为从、重复 Promote、重复 Demote。
func TestRoleErrors(t *testing.T) {
	l := mustNew(t, exampleCfg)
	// 初始为从：主操作全部报角色错误
	if _, err := l.Grant(1, 10, 0); !errors.Is(err, ErrNotPrimary) {
		t.Fatalf("从态 Grant 应报角色错误，实际 %v", err)
	}
	wantErr(t, l.Attach("a", 1, 0), ErrNotPrimary)
	if _, err := l.Tick(0); !errors.Is(err, ErrNotPrimary) {
		t.Fatalf("从态 Tick 应报角色错误，实际 %v", err)
	}
	if _, err := l.Revoke(1, 0); !errors.Is(err, ErrNotPrimary) {
		t.Fatalf("从态 Revoke 应报角色错误，实际 %v", err)
	}
	wantErr(t, l.Checkpoint(0), ErrNotPrimary)
	wantErr(t, l.Demote(0), ErrAlreadyFoll)
	must(t, l.Promote(0))
	wantErr(t, l.Promote(0), ErrAlreadyPrim)
	must(t, l.Demote(1))
	wantErr(t, l.Demote(1), ErrAlreadyFoll)
	must(t, l.Promote(1))
}

// 判定顺序：参数非法 -> 时间非法 -> 角色错误。
func TestCheckOrder(t *testing.T) {
	l := mustNew(t, exampleCfg)
	// 从态 + 非法 id：参数非法优先于角色错误
	if _, err := l.Grant(0, 10, 0); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("非法 id 应优先报参数非法，实际 %v", err)
	}
	// 从态 + 空 key：参数非法优先
	wantErr(t, l.Attach("", 1, 0), ErrEmptyKey)
	must(t, l.Promote(0))
	mustGrant(t, l, 1, 10, 5)
	// 非法 now（小于水位）+ 不存在的租约：时间非法优先
	if _, err := l.Renew(99, 4); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("now<T 应报时间非法，实际 %v", err)
	}
	// 越界 now
	if _, err := l.Renew(1, maxNow+1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("now>10^15 应报时间非法，实际 %v", err)
	}
	// ttl 越界
	if _, err := l.Grant(2, 0, 5); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("ttl=0 应报参数非法，实际 %v", err)
	}
	if _, err := l.Grant(2, 1001, 5); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("ttl>MaxTTL 应报参数非法，实际 %v", err)
	}
	// Grant 已存在
	if _, err := l.Grant(1, 10, 5); !errors.Is(err, ErrLeaseExists) {
		t.Fatalf("重复 Grant 应报租约已存在，实际 %v", err)
	}
	// Revoke / TTL 不存在
	if _, err := l.Revoke(99, 5); !errors.Is(err, ErrLeaseMissing) {
		t.Fatalf("Revoke 不存在应报租约不存在，实际 %v", err)
	}
	if _, err := l.TTL(99, 5); !errors.Is(err, ErrLeaseMissing) {
		t.Fatalf("TTL 不存在应报租约不存在，实际 %v", err)
	}
}

// 被拒绝的操作不得改变任何租约、键挂靠、T 与积压。
func TestRejectedKeepsState(t *testing.T) {
	l := mustNew(t, exampleCfg)
	must(t, l.Promote(0))
	mustGrant(t, l, 1, 10, 0)
	must(t, l.Attach("a", 1, 0))
	snap := l.snapshot()

	reject := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s 应被拒绝", name)
		}
		if got := l.snapshot(); !reflect.DeepEqual(got, snap) {
			t.Fatalf("%s 被拒绝后状态被改变:\n 之前 %v\n 之后 %v", name, snap, got)
		}
	}
	_, err := l.Grant(0, 10, 0)
	reject("Grant 非法 id", err)
	_, err = l.Grant(2, 0, 0)
	reject("Grant 非法 ttl", err)
	_, err = l.Grant(1, 10, 0)
	reject("Grant 已存在", err)
	_, err = l.Renew(99, 0)
	reject("Renew 不存在", err)
	reject("Attach 不存在", l.Attach("b", 99, 0))
	reject("Attach 空 key", l.Attach("", 1, 0))
	_, err = l.Revoke(99, 0)
	reject("Revoke 不存在", err)
	_, err = l.TTL(99, 0)
	reject("TTL 不存在", err)
	reject("重复 Promote", l.Promote(0))
	_, err = l.Tick(0 - 1)
	reject("Tick 非法时间", err)
}

// snapshot 捕获可观测状态：水位、角色、租约字段、键挂靠、堆大小。
func (l *Lessor) snapshot() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := fmt.Sprintf("T=%d primary=%v heap=%d keyTo=%v", l.now, l.primary, l.exp.Len(), l.keyTo)
	for id := int64(1); id <= 100; id++ {
		if ls, ok := l.leases[id]; ok {
			s += fmt.Sprintf(" lease%d{g=%d sv=%d x=%d keys=%v}", id, ls.g, ls.sv, ls.x, ls.keys)
		}
	}
	return s
}

// 配置越界整体拒绝。
func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{MinTTL: 0, MaxTTL: 10, E: 0, R: 1, Kmax: 1},
		{MinTTL: 1_000_001, MaxTTL: 2_000_000, E: 0, R: 1, Kmax: 1},
		{MinTTL: 5, MaxTTL: 4, E: 0, R: 1, Kmax: 1},
		{MinTTL: 1, MaxTTL: 1_000_000_001, E: 0, R: 1, Kmax: 1},
		{MinTTL: 1, MaxTTL: 10, E: -1, R: 1, Kmax: 1},
		{MinTTL: 1, MaxTTL: 10, E: 1_000_000_001, R: 1, Kmax: 1},
		{MinTTL: 1, MaxTTL: 10, E: 0, R: 0, Kmax: 1},
		{MinTTL: 1, MaxTTL: 10, E: 0, R: 1_000_001, Kmax: 1},
		{MinTTL: 1, MaxTTL: 10, E: 0, R: 1, Kmax: 0},
		{MinTTL: 1, MaxTTL: 10, E: 0, R: 1, Kmax: 1_000_001},
	}
	for i, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Fatalf("第 %d 组非法配置 %+v 未被拒绝", i, cfg)
		}
	}
	ok := []Config{
		{MinTTL: 1, MaxTTL: 1, E: 0, R: 1, Kmax: 1},
		{MinTTL: 1_000_000, MaxTTL: 1_000_000_000, E: 1_000_000_000, R: 1_000_000, Kmax: 1_000_000},
	}
	for i, cfg := range ok {
		if _, err := New(cfg); err != nil {
			t.Fatalf("第 %d 组合法配置 %+v 被拒绝: %v", i, cfg, err)
		}
	}
}

// 每次 Tick 检视的堆顶数不超过本次撤销数加一，且在租约数 100 与
// 10000 两档（大多数租约未到期）下相同。
func TestTickHeapInspection(t *testing.T) {
	for _, n := range []int64{100, 10000} {
		l := mustNew(t, Config{MinTTL: 1, MaxTTL: 1_000_000_000, E: 0, R: 1_000_000, Kmax: 1})
		must(t, l.Promote(0))
		for id := int64(1); id <= n; id++ {
			ttl := int64(1_000_000_000) // 大多数未到期
			if id <= 5 {
				ttl = 1 // 前 5 个 x=1，会到期
			}
			if _, err := l.Grant(id, ttl, 0); err != nil {
				t.Fatalf("n=%d Grant(%d) 失败: %v", n, id, err)
			}
		}
		got, err := l.Tick(10)
		must(t, err)
		if len(got) != 5 {
			t.Fatalf("n=%d 撤销数=%d，期望 5", n, len(got))
		}
		if l.tickInspected != len(got)+1 {
			t.Fatalf("n=%d 检视堆顶数=%d，期望 撤销数+1=%d", n, l.tickInspected, len(got)+1)
		}
		// 全部到期时：堆空收尾，检视数 == 撤销数
		got, err = l.Tick(1_000_000_000)
		must(t, err)
		if len(got) != int(n)-5 {
			t.Fatalf("n=%d 第二次撤销数=%d，期望 %d", n, len(got), n-5)
		}
		if l.tickInspected != len(got) {
			t.Fatalf("n=%d 堆空时检视数=%d，期望等于撤销数 %d", n, l.tickInspected, len(got))
		}
	}
}

// 堆与租约集始终一致：Renew/Revoke/Promote 后无过期堆项。
func TestHeapConsistency(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 100, E: 7, R: 3, Kmax: 10})
	must(t, l.Promote(0))
	for id := int64(1); id <= 20; id++ {
		mustGrant(t, l, id, id, 0)
	}
	check := func(what string) {
		t.Helper()
		if l.exp.Len() != len(l.leases) {
			t.Fatalf("%s 后堆大小=%d 租约数=%d，不一致", what, l.exp.Len(), len(l.leases))
		}
		for id, ls := range l.leases {
			idx, ok := l.exp.pos[id]
			if !ok || l.exp.items[idx] != ls {
				t.Fatalf("%s 后租约 %d 不在堆中或位置错误", what, id)
			}
		}
		for i := 1; i < l.exp.Len(); i++ {
			p := (i - 1) / 2
			if l.exp.Less(i, p) {
				t.Fatalf("%s 后堆序被破坏于下标 %d", what, i)
			}
		}
	}
	check("Grant")
	if _, err := l.Renew(20, 10); err != nil { // x: 20 -> 30
		t.Fatal(err)
	}
	check("Renew")
	if _, err := l.Revoke(5, 50); err != nil {
		t.Fatal(err)
	}
	check("Revoke")
	if _, err := l.Tick(50); err != nil {
		t.Fatal(err)
	}
	check("Tick")
	must(t, l.Demote(60))
	must(t, l.Promote(70))
	check("Promote")
}

// 并发调用：结果等价于某个串行顺序，且键挂靠不变量保持。
func TestConcurrent(t *testing.T) {
	l := mustNew(t, Config{MinTTL: 1, MaxTTL: 1000, E: 3, R: 4, Kmax: 8})
	must(t, l.Promote(0))
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				now := int64(i)
				id := int64((w+i)%16 + 1)
				key := fmt.Sprintf("k%d", (w*7+i)%32)
				switch (w + i) % 6 {
				case 0:
					_, _ = l.Grant(id, int64(i%50+1), now)
				case 1:
					_, _ = l.Renew(id, now)
				case 2:
					_ = l.Attach(key, id, now)
				case 3:
					_, _ = l.Tick(now)
				case 4:
					_, _ = l.Revoke(id, now)
				default:
					_, _ = l.TTL(id, now)
				}
			}
		}(w)
	}
	wg.Wait()
	// 不变量：每个键至多挂在一个租约上，且 keyTo 与租约键集互逆
	for k, id := range l.keyTo {
		ls, ok := l.leases[id]
		if !ok {
			t.Fatalf("keyTo[%q]=%d 指向不存在的租约", k, id)
		}
		if _, ok := ls.keys[k]; !ok {
			t.Fatalf("keyTo[%q]=%d 但租约键集中没有", k, id)
		}
	}
	for id, ls := range l.leases {
		for k := range ls.keys {
			if l.keyTo[k] != id {
				t.Fatalf("租约 %d 的键 %q 与 keyTo 不一致", id, k)
			}
		}
	}
}
