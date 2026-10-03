package version

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/authz"
	"ontology/lock"
)

var (
	permNone = authz.New()
	permDV   = authz.New(authz.DeleteVersion)
	permBG   = authz.New(authz.BypassGovernance)
	permPR   = authz.New(authz.PutRetention)
	permDVBG = authz.New(authz.DeleteVersion, authz.BypassGovernance)
	permAll  = authz.New(authz.DeleteVersion, authz.BypassGovernance, authz.PutRetention)
)

const noErr = ErrKind(-1)

type opKind int

const (
	kPut opKind = iota
	kDel
	kGet
	kDelVer
	kSetRet
	kSetHold
	kBatch
	kAudit
)

type op struct {
	kind   opKind
	key    string
	ver    uint64
	size   uint64
	now    uint64
	mode   lock.Mode
	until  uint64
	bypass bool
	on     bool
	perms  authz.Set
	items  []Item

	wantVer   uint64
	wantErr   ErrKind
	wantIdx   int
	wantInfo  *Info
	wantAudit []AuditEntry
}

func put(key string, size, now, wantVer uint64) op {
	return op{kind: kPut, key: key, size: size, now: now, wantVer: wantVer, wantErr: noErr, wantIdx: -1}
}

func putErr(key string, size, now uint64, want ErrKind) op {
	return op{kind: kPut, key: key, size: size, now: now, wantErr: want, wantIdx: -1}
}

func del(key string, now, wantVer uint64) op {
	return op{kind: kDel, key: key, now: now, wantVer: wantVer, wantErr: noErr, wantIdx: -1}
}

func delErr(key string, now uint64, want ErrKind) op {
	return op{kind: kDel, key: key, now: now, wantErr: want, wantIdx: -1}
}

func getInfo(key string, want *Info) op {
	return op{kind: kGet, key: key, wantInfo: want, wantErr: noErr, wantIdx: -1}
}

func getErr(key string, want ErrKind) op {
	return op{kind: kGet, key: key, wantErr: want, wantIdx: -1}
}

func delVer(key string, ver uint64, bypass bool, now uint64, perms authz.Set, want ErrKind) op {
	return op{kind: kDelVer, key: key, ver: ver, bypass: bypass, now: now, perms: perms, wantErr: want, wantIdx: -1}
}

func setRet(key string, ver uint64, mode lock.Mode, until uint64, bypass bool, now uint64, perms authz.Set, want ErrKind) op {
	return op{kind: kSetRet, key: key, ver: ver, mode: mode, until: until, bypass: bypass, now: now, perms: perms, wantErr: want, wantIdx: -1}
}

func setHold(key string, ver uint64, on bool, now uint64, perms authz.Set, want ErrKind) op {
	return op{kind: kSetHold, key: key, ver: ver, on: on, now: now, perms: perms, wantErr: want, wantIdx: -1}
}

func batch(items []Item, bypass bool, now uint64, perms authz.Set, want ErrKind, wantIdx int) op {
	return op{kind: kBatch, items: items, bypass: bypass, now: now, perms: perms, wantErr: want, wantIdx: wantIdx}
}

func audit(want ...AuditEntry) op {
	return op{kind: kAudit, wantAudit: want, wantErr: noErr, wantIdx: -1}
}

func runOps(t *testing.T, mode lock.Mode, d uint64, ops []op) *Bucket {
	t.Helper()
	b, err := New(mode, d)
	if err != nil {
		t.Fatalf("New(%v, %d) rejected: %v", mode, d, err)
	}
	for i, o := range ops {
		var gotErr *Error
		switch o.kind {
		case kPut:
			var v uint64
			v, gotErr = b.Put(o.key, o.size, o.now, o.perms)
			if gotErr == nil && v != o.wantVer {
				t.Fatalf("op %d Put: want ver %d, got %d", i, o.wantVer, v)
			}
		case kDel:
			var v uint64
			v, gotErr = b.Delete(o.key, o.now, o.perms)
			if gotErr == nil && v != o.wantVer {
				t.Fatalf("op %d Delete: want ver %d, got %d", i, o.wantVer, v)
			}
		case kGet:
			var info *Info
			info, gotErr = b.Get(o.key)
			if gotErr == nil && !reflect.DeepEqual(info, o.wantInfo) {
				t.Fatalf("op %d Get: want %+v, got %+v", i, o.wantInfo, info)
			}
		case kDelVer:
			gotErr = b.DeleteVersion(o.key, o.ver, o.bypass, o.now, o.perms)
		case kSetRet:
			gotErr = b.SetRetention(o.key, o.ver, o.mode, o.until, o.bypass, o.now, o.perms)
		case kSetHold:
			gotErr = b.SetHold(o.key, o.ver, o.on, o.now, o.perms)
		case kBatch:
			gotErr = b.DeleteVersions(o.items, o.bypass, o.now, o.perms)
		case kAudit:
			got := b.Audit()
			if len(got) != len(o.wantAudit) || (len(got) > 0 && !reflect.DeepEqual(got, o.wantAudit)) {
				t.Fatalf("op %d Audit: want %+v, got %+v", i, o.wantAudit, got)
			}
			continue
		}
		checkErr(t, i, gotErr, o.wantErr, o.wantIdx)
	}
	return b
}

func checkErr(t *testing.T, i int, got *Error, want ErrKind, wantIdx int) {
	t.Helper()
	if want == noErr {
		if got != nil {
			t.Fatalf("op %d: unexpected error %v (index %d)", i, got.Kind, got.Index)
		}
		return
	}
	if got == nil {
		t.Fatalf("op %d: want error %v, got success", i, want)
	}
	if got.Kind != want || got.Index != wantIdx {
		t.Fatalf("op %d: want (%v, index %d), got (%v, index %d)", i, want, wantIdx, got.Kind, got.Index)
	}
}

func TestScenarios(t *testing.T) {
	scenarios := []struct {
		name string
		mode lock.Mode
		d    uint64
		ops  []op
	}{
		{
			name: "规格例1：版本链与删除标记",
			mode: lock.Governance,
			ops: []op{
				put("a", 10, 1, 1),
				del("a", 2, 2),
				put("a", 20, 3, 3),
				getInfo("a", &Info{Key: "a", Ver: 3, Size: 20}),
				delVer("a", 3, false, 4, permDV, noErr),
				getErr("a", ErrMarkedDeleted),
				delVer("a", 2, false, 5, permDV, noErr),
				getInfo("a", &Info{Key: "a", Ver: 1, Size: 10}),
			},
		},
		{
			name: "规格例2：GOVERNANCE 绕过入审计",
			mode: lock.Governance,
			ops: []op{
				put("b", 1, 10, 1),
				setRet("b", 1, lock.Governance, 200, false, 10, permPR, noErr),
				getInfo("b", &Info{Key: "b", Ver: 1, Size: 1, Mode: lock.Governance, RetainUntil: 200}),
				delVer("b", 1, false, 150, permDV, ErrGovernanceRetention),
				delVer("b", 1, true, 150, permDV, ErrGovernanceRetention), // 缺 BypassGovernance 权限
				delVer("b", 1, true, 150, permDVBG, noErr),
				audit(AuditEntry{Key: "b", Ver: 1, Now: 150, OldRetainUntil: 200}),
				getErr("b", ErrNotExist),
			},
		},
		{
			name: "规格例2：COMPLIANCE 恰到期可删",
			mode: lock.Compliance,
			ops: []op{
				put("b", 1, 10, 1),
				setRet("b", 1, lock.Compliance, 200, false, 10, permPR, noErr),
				delVer("b", 1, true, 199, permDVBG, ErrComplianceRetention),
				delVer("b", 1, false, 200, permDV, noErr), // now == retainUntil 视为已到期
				audit(),
				getErr("b", ErrNotExist),
			},
		},
		{
			name: "边界：now 为 until-1 生效、等于 until 到期",
			mode: lock.Governance,
			ops: []op{
				put("a", 1, 0, 1),
				setRet("a", 1, lock.Governance, 100, false, 0, permPR, noErr),
				delVer("a", 1, false, 99, permDV, ErrGovernanceRetention),
				delVer("a", 1, true, 99, permDVBG, noErr), // 生效中绕过，入审计
				audit(AuditEntry{Key: "a", Ver: 1, Now: 99, OldRetainUntil: 100}),
				put("b", 1, 100, 2),
				setRet("b", 2, lock.Governance, 100, false, 100, permPR, ErrInvalidParam), // until<=now 参数非法
				setRet("b", 2, lock.Governance, 200, false, 100, permPR, noErr),
				delVer("b", 2, false, 200, permDV, noErr), // 恰等于 until，无审计
				audit(AuditEntry{Key: "a", Ver: 1, Now: 99, OldRetainUntil: 100}),
			},
		},
		{
			name: "规格例：SetRetention 迁移",
			mode: lock.Governance,
			ops: []op{
				put("a", 1, 0, 1),
				setRet("a", 1, lock.Governance, 100, false, 0, permPR, noErr),
				setRet("a", 1, lock.Governance, 100, false, 20, permPR, noErr),                  // 不小于原值
				setRet("a", 1, lock.Compliance, 99, false, 20, permPR, ErrGovernanceRetention),  // 缩短需绕过
				setRet("a", 1, lock.Compliance, 150, false, 20, permPR, noErr),                  // 升级且更长
				setRet("a", 1, lock.Governance, 200, false, 20, permPR, ErrComplianceRetention), // 合规不可降级
				setRet("a", 1, lock.Governance, 160, false, 150, permPR, noErr),                 // 到期视同无保留
				getInfo("a", &Info{Key: "a", Ver: 1, Size: 1, Mode: lock.Governance, RetainUntil: 160}),
			},
		},
		{
			name: "GOVERNANCE 缩短与清除需绕过",
			mode: lock.Governance,
			ops: []op{
				put("a", 1, 0, 1),
				setRet("a", 1, lock.Governance, 100, false, 0, permPR, noErr),
				setRet("a", 1, lock.Governance, 50, false, 10, permPR, ErrGovernanceRetention),
				setRet("a", 1, lock.Governance, 50, true, 10, permPR.With(authz.BypassGovernance), noErr),
				getInfo("a", &Info{Key: "a", Ver: 1, Size: 1, Mode: lock.Governance, RetainUntil: 50}),
				setRet("a", 1, lock.None, 0, false, 20, permPR, ErrGovernanceRetention),                            // 清除需绕过
				setRet("a", 1, lock.None, 1_000_000_000_001, true, 20, permPR.With(authz.BypassGovernance), noErr), // NONE 时 until 忽略
				getInfo("a", &Info{Key: "a", Ver: 1, Size: 1}),
				delVer("a", 1, false, 20, permDV, noErr),
				audit(),
			},
		},
		{
			name: "到期的 COMPLIANCE 可重设为更短",
			mode: lock.Compliance,
			ops: []op{
				put("a", 1, 0, 1),
				setRet("a", 1, lock.Compliance, 100, false, 0, permPR, noErr),
				setRet("a", 1, lock.Compliance, 120, false, 50, permPR, noErr), // 生效中不缩短
				setRet("a", 1, lock.Compliance, 110, false, 50, permPR, ErrComplianceRetention),
				setRet("a", 1, lock.Compliance, 130, false, 125, permPR, noErr),          // 已到期，任意设置
				setRet("a", 1, lock.None, 0, false, 125, permPR, ErrComplianceRetention), // 新保留又生效
				setRet("a", 1, lock.None, 0, false, 130, permPR, noErr),                  // 到期后可清除
				getInfo("a", &Info{Key: "a", Ver: 1, Size: 1}),
			},
		},
		{
			name: "删除标记不可加锁但永远可删",
			mode: lock.Governance,
			ops: []op{
				put("a", 1, 1, 1),
				del("a", 2, 2),
				setRet("a", 2, lock.Governance, 100, false, 3, permPR, ErrVersionNotFound),
				setHold("a", 2, true, 3, permPR, ErrVersionNotFound),
				del("a", 4, 3), // 当前已是标记也照加
				getErr("a", ErrMarkedDeleted),
				delVer("a", 3, false, 5, permDV, noErr),
				delVer("a", 2, false, 5, permDV, noErr),
				getInfo("a", &Info{Key: "a", Ver: 1, Size: 1}),
			},
		},
		{
			name: "法律保留压过已到期保留",
			mode: lock.Compliance,
			ops: []op{
				put("a", 1, 0, 1),
				setRet("a", 1, lock.Compliance, 50, false, 0, permPR, noErr),
				setHold("a", 1, true, 0, permPR, noErr),
				delVer("a", 1, false, 100, permDV, ErrLegalHold), // 保留已到期仍被法律保留阻止
				setHold("a", 1, false, 100, permPR, noErr),
				delVer("a", 1, false, 100, permDV, noErr),
			},
		},
		{
			name: "法律保留优先于合规保留",
			mode: lock.Compliance,
			ops: []op{
				put("a", 1, 0, 1),
				setRet("a", 1, lock.Compliance, 500, false, 0, permPR, noErr),
				setHold("a", 1, true, 0, permPR, noErr),
				delVer("a", 1, false, 10, permDV, ErrLegalHold),
			},
		},
		{
			name: "批量失败不留痕",
			mode: lock.Governance,
			ops: []op{
				put("a", 1, 1, 1),
				put("b", 1, 2, 2),
				setRet("b", 2, lock.Compliance, 1000, false, 3, permPR, noErr),
				setRet("a", 1, lock.Governance, 100, false, 3, permPR, noErr),
				batch([]Item{{"a", 1}, {"b", 2}}, true, 10, permAll, ErrComplianceRetention, 1),
				audit(),
				put("c", 1, 5, 3), // 时钟未推进到 10，序号未消耗
				getInfo("a", &Info{Key: "a", Ver: 1, Size: 1, Mode: lock.Governance, RetainUntil: 100}),
				getInfo("b", &Info{Key: "b", Ver: 2, Size: 1, Mode: lock.Compliance, RetainUntil: 1000}),
			},
		},
		{
			name: "批量成功按下标升序入审计",
			mode: lock.Governance,
			ops: []op{
				put("a", 1, 1, 1),
				put("b", 1, 2, 2),
				setRet("a", 1, lock.Governance, 100, false, 3, permPR, noErr),
				setRet("b", 2, lock.Governance, 200, false, 3, permPR, noErr),
				batch([]Item{{"b", 2}, {"a", 1}}, true, 10, permDVBG, noErr, -1),
				audit(
					AuditEntry{Key: "b", Ver: 2, Now: 10, OldRetainUntil: 200},
					AuditEntry{Key: "a", Ver: 1, Now: 10, OldRetainUntil: 100},
				),
				getErr("a", ErrNotExist),
				getErr("b", ErrNotExist),
			},
		},
		{
			name: "批量参数与批级检查",
			mode: lock.Governance,
			ops: []op{
				put("a", 1, 10, 1),
				batch(nil, false, 10, permDV, ErrInvalidParam, -1),                          // 空批
				batch([]Item{{"a", 1}, {"a", 1}}, false, 10, permDV, ErrInvalidParam, -1),   // 批内重复
				batch([]Item{{"a", 0}}, false, 10, permDV, ErrInvalidParam, -1),             // ver 非正
				batch([]Item{{"", 1}}, false, 10, permDV, ErrInvalidParam, -1),              // 空键
				batch([]Item{{"a", 1}}, false, 5, permDV, ErrClockRollback, -1),             // 时钟回退
				batch([]Item{{"a", 1}}, false, 10, permNone, ErrNoPermission, -1),           // 无权限不带下标
				batch([]Item{{"a", 1}, {"x", 9}}, false, 10, permDV, ErrVersionNotFound, 1), // 带下标
				batch([]Item{{"a", 1}}, false, 10, permDV, noErr, -1),
			},
		},
		{
			name: "拒绝次序：参数>时钟>权限>不存在>锁",
			mode: lock.Governance,
			ops: []op{
				put("a", 1, 10, 1),
				putErr("", 0, 5, ErrInvalidParam),                   // 参数非法压过时钟回退
				putErr("a", 1_000_000_000_001, 10, ErrInvalidParam), // size 越界
				putErr("a", 1, 1_000_000_000_001, ErrInvalidParam),  // now 越界
				putErr("a", 1, 5, ErrClockRollback),
				delErr("", 5, ErrInvalidParam),
				delErr("a", 5, ErrClockRollback),
				delVer("a", 0, false, 10, permDV, ErrInvalidParam),     // ver 非正
				delVer("a", 1, false, 5, permNone, ErrClockRollback),   // 时钟压过权限
				delVer("a", 999, false, 10, permNone, ErrNoPermission), // 权限压过不存在
				delVer("a", 999, false, 10, permDV, ErrVersionNotFound),
				getErr("", ErrInvalidParam),
				getErr("missing", ErrNotExist),
				setRet("a", 1, lock.Mode(7), 100, false, 10, permPR, ErrInvalidParam),                  // 非法模式
				setRet("a", 1, lock.Governance, 1_000_000_000_001, false, 10, permPR, ErrInvalidParam), // until 越界
				setRet("a", 1, lock.Governance, 100, false, 5, permNone, ErrClockRollback),             // 时钟压过权限
				setRet("a", 999, lock.Governance, 100, false, 10, permNone, ErrNoPermission),
				setRet("a", 999, lock.Governance, 100, false, 10, permPR, ErrVersionNotFound),
				setHold("a", 999, true, 10, permNone, ErrNoPermission),
				setHold("a", 999, true, 10, permPR, ErrVersionNotFound),
			},
		},
		{
			name: "默认保留：D=100 COMPLIANCE",
			mode: lock.Compliance,
			d:    100,
			ops: []op{
				put("a", 1, 5, 1),
				getInfo("a", &Info{Key: "a", Ver: 1, Size: 1, Mode: lock.Compliance, RetainUntil: 105}),
				delVer("a", 1, true, 104, permDVBG, ErrComplianceRetention),
				delVer("a", 1, false, 105, permDV, noErr),
				del("a", 106, 2), // 删除标记不受任何锁约束
				getErr("a", ErrMarkedDeleted),
			},
		},
		{
			name: "bypass 多余时不入审计",
			mode: lock.Governance,
			ops: []op{
				put("a", 1, 1, 1), // 无保留
				delVer("a", 1, true, 2, permDVBG, noErr),
				put("b", 1, 3, 2),
				setRet("b", 2, lock.Governance, 10, false, 3, permPR, noErr),
				delVer("b", 2, true, 10, permDVBG, noErr), // 已到期，bypass 多余
				audit(),
			},
		},
		{
			name: "时钟相同允许、被拒绝操作不占号",
			mode: lock.Governance,
			ops: []op{
				put("a", 1, 5, 1),
				put("b", 1, 5, 2), // now 等于上次，允许
				putErr("c", 1, 4, ErrClockRollback),
				delVer("c", 1, false, 5, permNone, ErrNoPermission), // 被拒绝不占号
				put("c", 1, 5, 3),
			},
		},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			runOps(t, sc.mode, sc.d, sc.ops)
		})
	}
}

func TestNew(t *testing.T) {
	if _, err := New(lock.None, 0); err == nil || err.Kind != ErrInvalidParam {
		t.Fatalf("New(None, 0): want invalid param, got %v", err)
	}
	if _, err := New(lock.Governance, 1_000_000_001); err == nil || err.Kind != ErrInvalidParam {
		t.Fatalf("New(GOVERNANCE, 1e9+1): want invalid param, got %v", err)
	}
	if _, err := New(lock.Compliance, 1_000_000_000); err != nil {
		t.Fatalf("New(COMPLIANCE, 1e9): want success, got %v", err)
	}
}

// TestTouched 证明 Get 与"删除当前版本后重定当前版本"触碰的版本记录数
// 不超过 2，与全桶版本总数及该键版本数无关。
func TestTouched(t *testing.T) {
	for _, total := range []int{100, 10000} {
		t.Run(fmt.Sprintf("total=%d", total), func(t *testing.T) {
			b, err := New(lock.Governance, 0)
			if err != nil {
				t.Fatal(err)
			}
			// 一半版本铺在其它键上，一半铺在目标键上。
			for i := 0; i < total/2; i++ {
				if _, err := b.Put("other"+string(rune(i)), 1, 0, permNone); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < total-total/2; i++ {
				if _, err := b.Put("target", 1, 0, permNone); err != nil {
					t.Fatal(err)
				}
			}
			b.touched = 0
			if _, err := b.Get("target"); err != nil {
				t.Fatal(err)
			}
			if b.touched > 2 {
				t.Fatalf("Get touched %d records, want <= 2 (total=%d)", b.touched, total)
			}
			// 删除当前版本（链尾），重定当前版本。
			cur := b.chains["target"].current.ver
			b.touched = 0
			if err := b.DeleteVersion("target", cur, false, 0, permDV); err != nil {
				t.Fatal(err)
			}
			if b.touched > 2 {
				t.Fatalf("delete-current touched %d records, want <= 2 (total=%d)", b.touched, total)
			}
		})
	}
}

// TestConcurrent 并发 Put 的结果等价于某个串行顺序：版本号恰为 1..N 各一次。
func TestConcurrent(t *testing.T) {
	b, err := New(lock.Governance, 0)
	if err != nil {
		t.Fatal(err)
	}
	const n = 64
	vers := make([]uint64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := b.Put("k", 1, 0, permNone)
			if err != nil {
				t.Error(err)
				return
			}
			vers[i] = v
		}(i)
	}
	wg.Wait()
	seen := map[uint64]bool{}
	for _, v := range vers {
		if v < 1 || v > n || seen[v] {
			t.Fatalf("version numbers not contiguous 1..%d: %v", n, vers)
		}
		seen[v] = true
	}
}
