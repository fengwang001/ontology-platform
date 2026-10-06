package layercfg

import (
	"errors"
	"reflect"
	"testing"
)

func mustPublish(t *testing.T, s *Store, changes []Change, wantVersion int) {
	t.Helper()
	v, err := s.Publish(changes)
	if err != nil {
		t.Fatalf("Publish(%v) unexpected error: %v", changes, err)
	}
	if v != wantVersion {
		t.Fatalf("Publish version = %d, want %d", v, wantVersion)
	}
}

func expectKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *layercfg.Error, got %v", err)
	}
	if e.Kind != want {
		t.Fatalf("error kind = %d, want %d (%s)", e.Kind, want, e.Msg)
	}
}

func expectUnset(t *testing.T, r Result) {
	t.Helper()
	if r.Present {
		t.Fatalf("expected unset, got %+v", r)
	}
}

func expectList(t *testing.T, r Result, want []string) {
	t.Helper()
	if !r.Present || r.Value.Type != TypeStringList || !reflect.DeepEqual(r.Value.List, want) {
		t.Fatalf("expected list %v, got %+v", want, r)
	}
}

func expectString(t *testing.T, r Result, want string) {
	t.Helper()
	if !r.Present || r.Value.Type != TypeString || r.Value.Str != want {
		t.Fatalf("expected string %q, got %+v", want, r)
	}
}

func gref() Ref { return Ref{Layer: LayerGlobal} }
func eref(env string) Ref {
	return Ref{Layer: LayerEnv, Env: env}
}
func rref(env, region string) Ref {
	return Ref{Layer: LayerRegion, Env: env, Region: region}
}
func iref(env, region, instance string) Ref {
	return Ref{Layer: LayerInstance, Env: env, Region: region, Instance: instance}
}

func setv(ref Ref, key string, v Value) Change {
	return Change{Op: OpSetValue, Ref: ref, Key: key, Value: v}
}
func cancelc(ref Ref, key string) Change { return Change{Op: OpCancel, Ref: ref, Key: key} }
func clearc(ref Ref, key string) Change  { return Change{Op: OpClearWrite, Ref: ref, Key: key} }
func lockc(ref Ref, key string) Change   { return Change{Op: OpLock, Ref: ref, Key: key} }
func unlockc(ref Ref, key string) Change { return Change{Op: OpUnlock, Ref: ref, Key: key} }

func mustReg(t *testing.T, s *Store, key string, typ ValueType, merge MergeMode, required bool) {
	t.Helper()
	if err := s.RegisterKey(SchemaSpec{Key: key, Type: typ, Merge: merge, Required: required}); err != nil {
		t.Fatalf("RegisterKey(%q): %v", key, err)
	}
}

func mustResolve(t *testing.T, s *Store, key string, scope Scope, version int) Result {
	t.Helper()
	r, err := s.Resolve(key, scope, version)
	if err != nil {
		t.Fatalf("Resolve(%q,%+v,v=%d): %v", key, scope, version, err)
	}
	return r
}

// TestFourLayerOverride 四层从宽到窄叠加；缺省三元组只解析到对应层。
func TestFourLayerOverride(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "host", TypeString, MergeReplace, false)
	mustPublish(t, s, []Change{
		setv(gref(), "host", NewString("g")),
		setv(eref("prod"), "host", NewString("e")),
		setv(rref("prod", "cn"), "host", NewString("r")),
		setv(iref("prod", "cn", "i1"), "host", NewString("i")),
	}, 1)

	expectString(t, mustResolve(t, s, "host", Scope{}, -1), "g")
	expectString(t, mustResolve(t, s, "host", Scope{Env: "prod"}, -1), "e")
	expectString(t, mustResolve(t, s, "host", Scope{Env: "prod", Region: "us"}, -1), "e")
	expectString(t, mustResolve(t, s, "host", Scope{Env: "prod", Region: "cn"}, -1), "r")
	expectString(t, mustResolve(t, s, "host", Scope{Env: "prod", Region: "cn", Instance: "i1"}, -1), "i")
	expectString(t, mustResolve(t, s, "host", Scope{Env: "prod", Region: "cn", Instance: "i2"}, -1), "r")
}

// TestCancelReplaceThenNarrowRewrite 覆盖型键：取消作废本层及更宽层，更窄层仍可写入。
func TestCancelReplaceThenNarrowRewrite(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "name", TypeString, MergeReplace, false)
	mustPublish(t, s, []Change{
		setv(gref(), "name", NewString("g")),
		setv(eref("prod"), "name", NewString("e")),
	}, 1)
	mustPublish(t, s, []Change{cancelc(rref("prod", "cn"), "name")}, 2)
	expectUnset(t, mustResolve(t, s, "name", Scope{Env: "prod", Region: "cn"}, -1))
	expectString(t, mustResolve(t, s, "name", Scope{Env: "prod"}, -1), "e")

	mustPublish(t, s, []Change{setv(iref("prod", "cn", "i1"), "name", NewString("i"))}, 3)
	expectString(t, mustResolve(t, s, "name", Scope{Env: "prod", Region: "cn", Instance: "i1"}, -1), "i")
	expectUnset(t, mustResolve(t, s, "name", Scope{Env: "prod", Region: "cn", Instance: "i2"}, -1))
}

// TestAppendDedupFirstPosition 追加型列表：重复元素保留首次出现位置。
func TestAppendDedupFirstPosition(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "tags", TypeStringList, MergeAppend, false)
	mustPublish(t, s, []Change{
		setv(gref(), "tags", NewStringList([]string{"a", "b"})),
		setv(eref("prod"), "tags", NewStringList([]string{"b", "c"})),
		setv(rref("prod", "cn"), "tags", NewStringList([]string{"d", "a"})),
	}, 1)
	expectList(t, mustResolve(t, s, "tags", Scope{Env: "prod", Region: "cn"}, -1),
		[]string{"a", "b", "c", "d"})
	expectList(t, mustResolve(t, s, "tags", Scope{Env: "prod", Region: "us"}, -1),
		[]string{"a", "b", "c"})
}

// TestCancelAppendClears 取消标记对追加型列表同样清空累积，窄层之后可重新追加。
func TestCancelAppendClears(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "tags", TypeStringList, MergeAppend, false)
	mustPublish(t, s, []Change{
		setv(gref(), "tags", NewStringList([]string{"a"})),
		cancelc(eref("prod"), "tags"),
		setv(rref("prod", "cn"), "tags", NewStringList([]string{"x", "y"})),
	}, 1)
	expectList(t, mustResolve(t, s, "tags", Scope{Env: "prod", Region: "cn"}, -1),
		[]string{"x", "y"})
	expectList(t, mustResolve(t, s, "tags", Scope{Env: "dev"}, -1), []string{"a"})
}

// TestUnsetVsEmpty 未设置区别于空串与空列表。
func TestUnsetVsEmpty(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "s", TypeString, MergeReplace, false)
	mustReg(t, s, "l", TypeStringList, MergeAppend, false)
	mustPublish(t, s, []Change{
		setv(gref(), "s", NewString("")),
		setv(gref(), "l", NewStringList([]string{})),
	}, 1)
	rs := mustResolve(t, s, "s", Scope{}, -1)
	if !rs.Present || rs.Value.Str != "" {
		t.Fatalf("empty string must be present, got %+v", rs)
	}
	rl := mustResolve(t, s, "l", Scope{}, -1)
	if !rl.Present || len(rl.Value.List) != 0 {
		t.Fatalf("empty list must be present, got %+v", rl)
	}
	// 已写键即使在非已存在环境也继承全局空串；真正未设置用从未写入的键验证。
	mustReg(t, s, "u", TypeString, MergeReplace, false)
	expectUnset(t, mustResolve(t, s, "u", Scope{}, -1))
	expectUnset(t, mustResolve(t, s, "u", Scope{Env: "nope"}, -1))
}

// TestLockConstraints 锁定只约束更窄层：本层与宽层可写，窄层写值/取消拒绝。
func TestLockConstraints(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "k", TypeString, MergeReplace, false)

	mustPublish(t, s, []Change{
		lockc(eref("prod"), "k"),
		setv(eref("prod"), "k", NewString("e")),
	}, 1)
	mustPublish(t, s, []Change{setv(gref(), "k", NewString("g"))}, 2)

	_, err := s.Publish([]Change{setv(rref("prod", "cn"), "k", NewString("r"))})
	expectKind(t, err, ErrLockConflict)
	_, err = s.Publish([]Change{cancelc(rref("prod", "cn"), "k")})
	expectKind(t, err, ErrLockConflict)

	// 不同环境链不受影响。
	mustPublish(t, s, []Change{setv(rref("dev", "cn"), "k", NewString("r"))}, 3)

	// 解锁后窄层可写，重新锁定再次拒绝。
	mustPublish(t, s, []Change{unlockc(eref("prod"), "k")}, 4)
	mustPublish(t, s, []Change{setv(rref("prod", "cn"), "k", NewString("r"))}, 5)
	mustPublish(t, s, []Change{
		lockc(eref("prod"), "k"),
		clearc(rref("prod", "cn"), "k"),
	}, 6)
	_, err = s.Publish([]Change{setv(rref("prod", "cn"), "k", NewString("r2"))})
	expectKind(t, err, ErrLockConflict)
}

// TestLockPreexistingNarrowWrite 锁定时更窄层已有写入必须先清理，否则整次发布拒绝。
func TestLockPreexistingNarrowWrite(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "k", TypeString, MergeReplace, false)
	mustPublish(t, s, []Change{setv(rref("prod", "cn"), "k", NewString("r"))}, 1)
	before := s.Current()
	_, err := s.Publish([]Change{lockc(eref("prod"), "k")})
	expectKind(t, err, ErrLockConflict)
	if s.Current() != before {
		t.Fatalf("failed publish changed version: %d", s.Current())
	}

	// 同次发布锁定并清理窄层写入：合法。
	mustPublish(t, s, []Change{
		lockc(eref("prod"), "k"),
		clearc(rref("prod", "cn"), "k"),
	}, 2)
}

// TestRequiredPropagation 必填沿全局、每个已存在环境/区域/实例传播。
func TestRequiredPropagation(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "req", TypeString, MergeReplace, true)

	// 仅有区域写入：全局视图与新出现的 prod 环境视图均缺必填。
	_, err := s.Publish([]Change{setv(rref("prod", "cn"), "req", NewString("r"))})
	expectKind(t, err, ErrRequiredMissing)

	// 补全局值后：全局视图有值，prod 环境视图继承全局值，区域自带值，故合法。
	mustPublish(t, s, []Change{
		setv(gref(), "req", NewString("g")),
		setv(rref("prod", "cn"), "req", NewString("r")),
	}, 1)

	// 实例层取消会作废区域继承值，实例视图缺必填。
	_, err = s.Publish([]Change{cancelc(iref("prod", "cn", "i1"), "req")})
	expectKind(t, err, ErrRequiredMissing)

	// 直接写实例值合法；新区域 cn2 可继承 env 值（区域视图有值）。
	mustPublish(t, s, []Change{setv(iref("prod", "cn", "i1"), "req", NewString("i"))}, 2)
	mustPublish(t, s, []Change{setv(rref("prod", "cn2"), "req", NewString("r2"))}, 3)

	// env 层取消作废全局继承值，使该环境及继承它的实体缺必填。
	_, err = s.Publish([]Change{cancelc(eref("prod"), "req")})
	expectKind(t, err, ErrRequiredMissing)
}

// TestRollbackAndHistory 回滚产生内容相同的新版本；回滚当前版本无操作；历史版本不删除。
func TestRollbackAndHistory(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "k", TypeString, MergeReplace, false)
	mustPublish(t, s, []Change{setv(gref(), "k", NewString("v1"))}, 1)
	mustPublish(t, s, []Change{setv(gref(), "k", NewString("v2"))}, 2)

	expectString(t, mustResolve(t, s, "k", Scope{}, 1), "v1")
	expectString(t, mustResolve(t, s, "k", Scope{}, 2), "v2")

	nv, err := s.Rollback(1)
	if err != nil || nv != 3 {
		t.Fatalf("rollback(1) = %d,%v", nv, err)
	}
	expectString(t, mustResolve(t, s, "k", Scope{}, -1), "v1")
	expectString(t, mustResolve(t, s, "k", Scope{}, 3), "v1")
	expectString(t, mustResolve(t, s, "k", Scope{}, 2), "v2") // 历史版本仍在

	// 回滚当前版本：无操作、不产生新版本。
	nv2, err := s.Rollback(3)
	if err != nil || nv2 != 3 {
		t.Fatalf("rollback(current) = %d,%v", nv2, err)
	}
	if s.Current() != 3 {
		t.Fatalf("current = %d", s.Current())
	}

	// 不存在的版本报错。
	_, err = s.Rollback(99)
	expectKind(t, err, ErrVersionNotFound)
	_, err = s.Resolve("k", Scope{}, 99)
	expectKind(t, err, ErrVersionNotFound)
}

// TestErrorPriorityAndAtomicity 错误类别优先级与失败发布的原子性。
func TestErrorPriorityAndAtomicity(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "n", TypeInt, MergeReplace, false)

	// 参数非法（层限定非法）优先于键未登记。
	_, err := s.Publish([]Change{{Op: OpSetValue, Ref: Ref{Layer: LayerEnv}, Key: "ghost", Value: NewInt(1)}})
	expectKind(t, err, ErrInvalidArgument)

	// 键未登记优先于类型错误。
	_, err = s.Publish([]Change{setv(gref(), "ghost", NewString("x"))})
	expectKind(t, err, ErrKeyNotRegistered)

	// 类型/范围非法。
	_, err = s.Publish([]Change{setv(gref(), "n", NewString("x"))})
	expectKind(t, err, ErrTypeOrRange)

	mustReg(t, s, "r", TypeInt, MergeReplace, false)
	// append 仅允许字符串列表：登记阶段即参数非法。
	err = s.RegisterKey(SchemaSpec{Key: "bad", Type: TypeInt, Merge: MergeAppend})
	expectKind(t, err, ErrInvalidArgument)

	// 同发布冲突优先于锁定冲突：同层同键两条写值。
	_, err = s.Publish([]Change{
		setv(gref(), "n", NewInt(1)),
		setv(gref(), "n", NewInt(2)),
	})
	expectKind(t, err, ErrConflict)

	// 混合批次中只要有一条非法，全部不生效（版本不变，值不存在）。
	before := s.Current()
	_, err = s.Publish([]Change{
		setv(gref(), "n", NewInt(1)),
		setv(gref(), "r", NewString("bad")),
	})
	expectKind(t, err, ErrTypeOrRange)
	if s.Current() != before {
		t.Fatalf("failed publish changed version")
	}
	expectUnset(t, mustResolve(t, s, "n", Scope{}, -1))

	// Resolve 参数非法优先于版本/键错误。
	_, err = s.Resolve("n", Scope{Region: "cn"}, 99)
	expectKind(t, err, ErrInvalidArgument)
	_, err = s.Resolve("ghost", Scope{}, 99)
	expectKind(t, err, ErrKeyNotRegistered)
}

// TestIntRange 整数取值范围校验。
func TestIntRange(t *testing.T) {
	s := NewStore()
	mustRegR(t, s, "port", 1, 65535, false)
	_, err := s.Publish([]Change{setv(gref(), "port", NewInt(0))})
	expectKind(t, err, ErrTypeOrRange)
	mustPublish(t, s, []Change{setv(gref(), "port", NewInt(8080))}, 1)
}

// TestClearAndOtherSlotSamePublish 同批对同键既清值槽又动锁槽时，
// 清除必须真实生效，不能因中途删除后重新克隆而回滚（随机对照发现的缺陷回归）。
func TestClearAndOtherSlotSamePublish(t *testing.T) {
	s := NewStore()
	mustReg(t, s, "req", TypeString, MergeReplace, false)
	mustPublish(t, s, []Change{setv(gref(), "req", NewString("g"))}, 1)
	// 同批：清除全局值 + 全局层锁定。清除后全局无值，锁定本身合法。
	mustPublish(t, s, []Change{
		clearc(gref(), "req"),
		lockc(gref(), "req"),
	}, 2)
	expectUnset(t, mustResolve(t, s, "req", Scope{}, -1))

	// 反向顺序同样生效。
	mustPublish(t, s, []Change{unlockc(gref(), "req")}, 3)
	mustPublish(t, s, []Change{setv(gref(), "req", NewString("g2"))}, 4)
	mustPublish(t, s, []Change{
		lockc(gref(), "req"),
		clearc(gref(), "req"),
	}, 5)
	expectUnset(t, mustResolve(t, s, "req", Scope{}, -1))
}

func mustRegR(t *testing.T, s *Store, key string, min, max int64, required bool) {
	t.Helper()
	err := s.RegisterKey(SchemaSpec{Key: key, Type: TypeInt, Merge: MergeReplace, Required: required,
		Range: &IntRange{Min: min, Max: max}})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
}
