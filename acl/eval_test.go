package acl

import (
	"reflect"
	"testing"
)

// 拒绝条目只覆盖已授予位时被忽略，已授予位不会被收回。
func TestDenyCoveringOnlyGrantedBitsIgnored(t *testing.T) {
	s := mustStore(t, 4, 8)
	mustSetACL(t, s, "/", []ACE{
		{Allow: false, Principal: "u", Mask: 0b0011, Flags: 0}, // X 重排后在前，但只覆盖已授予位
		{Allow: true, Principal: "u", Mask: 0b0011, Flags: 0},
	}, false)
	// X(/) = [deny 0011, allow 0011]；拒绝先到时 G=0，会命中，因此改用继承顺序构造：
	// 父节点放允许，子节点放拒绝，使允许先于拒绝被处理。
	s2 := mustStore(t, 4, 8)
	mustAdd(t, s2, "c", "/", true)
	mustSetACL(t, s2, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0011, Flags: FlagCI},
	}, false)
	mustSetACL(t, s2, "c", []ACE{
		{Allow: false, Principal: "u", Mask: 0b0010, Flags: 0},
	}, false)
	// E(c) = [deny u 0010 (c), allow u 0011 CI (/)] —— 显式拒绝仍在显式允许之前，
	// 但此处显式只有拒绝；拒绝先到且 G=0，0010 & 0011 & ~0 != 0，命中拒绝。
	checkResult(t, mustEval(t, s2, []string{"u"}, "c", 0b0011), ResultDeniedHit, 0, "c", false, 0)

	// 让允许先授予 0010，再由继承的拒绝检查同一位：应被忽略。
	s3 := mustStore(t, 4, 8)
	mustAdd(t, s3, "c", "/", true)
	mustSetACL(t, s3, "/", []ACE{
		{Allow: false, Principal: "u", Mask: 0b0010, Flags: FlagCI},
	}, false)
	mustSetACL(t, s3, "c", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0011, Flags: 0},
	}, false)
	// E(c) = [allow u 0011 (c), deny u 0010 CI (/)]
	res := mustEval(t, s3, []string{"u"}, "c", 0b0011)
	checkResult(t, res, ResultGranted, 0, "c", false, 0b0011)
}

// 拒绝排在允许之后，命中未授予位时仍判拒绝。
func TestDenyAfterAllowStillDenies(t *testing.T) {
	s := mustStore(t, 4, 8)
	mustAdd(t, s, "c", "/", true)
	mustSetACL(t, s, "/", []ACE{
		{Allow: false, Principal: "u", Mask: 0b0100, Flags: FlagCI},
	}, false)
	mustSetACL(t, s, "c", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0011, Flags: 0},
	}, false)
	// E(c) = [allow u 0011 (c), deny u 0100 CI (/)]；R=0111 时先得 0011，再命中 0100。
	checkResult(t, mustEval(t, s, []string{"u"}, "c", 0b0111), ResultDeniedHit, 1, "/", true, 0b0011)
}

// 显式列表内拒绝先于允许（X 的重排）。
func TestExplicitDeniesBeforeAllows(t *testing.T) {
	s := mustStore(t, 4, 8)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: 0},
		{Allow: false, Principal: "u", Mask: 0b0010, Flags: 0},
		{Allow: true, Principal: "u", Mask: 0b0100, Flags: 0},
		{Allow: false, Principal: "u", Mask: 0b1000, Flags: 0},
	}, false)
	want := []EffectiveEntry{
		{Allow: false, Principal: "u", Mask: 0b0010, Flags: 0, Source: "/"},
		{Allow: false, Principal: "u", Mask: 0b1000, Flags: 0, Source: "/"},
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: 0, Source: "/"},
		{Allow: true, Principal: "u", Mask: 0b0100, Flags: 0, Source: "/"},
	}
	if got := mustEffective(t, s, "/"); !reflect.DeepEqual(got, want) {
		t.Fatalf("E(/) = %+v, want %+v", got, want)
	}
}

// 继承部分保持父有效列表自身顺序，不做全局拒绝优先重排：祖父的拒绝排在父的允许之后。
func TestInheritedOrderPreserved(t *testing.T) {
	s := mustStore(t, 4, 8)
	mustAdd(t, s, "a", "/", true)
	mustAdd(t, s, "b", "a", true)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: FlagCI},
		{Allow: false, Principal: "u", Mask: 0b0010, Flags: FlagCI},
	}, false)
	mustSetACL(t, s, "a", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0100, Flags: FlagCI},
	}, false)
	// E(/) = [deny 0010 CI, allow 0001 CI]（X 重排）
	// E(a) = [allow 0100 CI (a), deny 0010 CI (/), allow 0001 CI (/)]
	// E(b) = [deny 0010 CI (/)->b, allow 0100 CI (a), allow 0001 CI (/)]？
	// 注意 E(b) 的显式部分为空，继承自 E(a) 整体保持顺序：
	wantA := []EffectiveEntry{
		{Allow: true, Principal: "u", Mask: 0b0100, Flags: FlagCI, Source: "a"},
		{Allow: false, Principal: "u", Mask: 0b0010, Flags: FlagCI, Source: "/"},
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: FlagCI, Source: "/"},
	}
	if got := mustEffective(t, s, "a"); !reflect.DeepEqual(got, wantA) {
		t.Fatalf("E(a) = %+v, want %+v", got, wantA)
	}
	// E(b)：继承自 E(a)，每条 CI 保留，顺序不变；祖父的拒绝排在父的允许之后。
	wantB := []EffectiveEntry{
		{Allow: true, Principal: "u", Mask: 0b0100, Flags: FlagCI, Source: "a"},
		{Allow: false, Principal: "u", Mask: 0b0010, Flags: FlagCI, Source: "/"},
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: FlagCI, Source: "/"},
	}
	if got := mustEffective(t, s, "b"); !reflect.DeepEqual(got, wantB) {
		t.Fatalf("E(b) = %+v, want %+v", got, wantB)
	}
	// R=0111：下标 0 得 0100，下标 1 命中拒绝（0010 未授予）。
	checkResult(t, mustEval(t, s, []string{"u"}, "b", 0b0111), ResultDeniedHit, 1, "/", true, 0b0100)
}

// 同一请求的不同位由不同条目补齐，G 恰等于 R 时提前返回。
func TestBitsAccumulatedAcrossEntries(t *testing.T) {
	s := mustStore(t, 4, 8)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: 0},
		{Allow: true, Principal: "u", Mask: 0b0110, Flags: 0},
		{Allow: true, Principal: "u", Mask: 0b1000, Flags: 0},
	}, false)
	// X 保持允许间相对顺序；R=1111 时三条各补一位段，第三条使 G==R 提前返回。
	res := mustEval(t, s, []string{"u"}, "/", 0b1111)
	checkResult(t, res, ResultGranted, 2, "/", false, 0b1111)
	if s.lastEvalEntries != 3 {
		t.Fatalf("lastEvalEntries = %d, want 3", s.lastEvalEntries)
	}
	// R=0011：第一条得 0001，第二条补 0010 后 G==R，提前返回，不读第三条。
	res = mustEval(t, s, []string{"u"}, "/", 0b0011)
	checkResult(t, res, ResultGranted, 1, "/", false, 0b0011)
	if s.lastEvalEntries != 2 {
		t.Fatalf("lastEvalEntries = %d, want 2 (early return)", s.lastEvalEntries)
	}
}

// 隐式拒绝与命中拒绝可区分；隐式拒绝无决定性条目且带出 G。
func TestImplicitVsExplicitDeny(t *testing.T) {
	s := mustStore(t, 4, 8)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: 0},
		{Allow: false, Principal: "u", Mask: 0b1000, Flags: 0},
	}, false)
	res := mustEval(t, s, []string{"u"}, "/", 0b0011)
	checkResult(t, res, ResultImplicitDeny, -1, "", false, 0b0001)
	res = mustEval(t, s, []string{"u"}, "/", 0b1001)
	checkResult(t, res, ResultDeniedHit, 0, "/", false, 0)
	// 主体不在 token 中的条目被跳过。
	res = mustEval(t, s, []string{"v"}, "/", 0b0001)
	checkResult(t, res, ResultImplicitDeny, -1, "", false, 0)
}

// IO 条目不参与本节点判定，但仍留在 E(n) 并向下传播。
func TestIOSkippedLocallyButPropagates(t *testing.T) {
	s := mustStore(t, 4, 8)
	mustAdd(t, s, "c", "/", true)
	mustAdd(t, s, "o", "c", false)
	mustSetACL(t, s, "c", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: FlagOI | FlagIO},
	}, false)
	// c 上：IO 条目被跳过 → 隐式拒绝。
	eff := mustEffective(t, s, "c")
	if len(eff) != 1 || eff[0].Flags != FlagOI|FlagIO {
		t.Fatalf("E(c) = %+v, want single OI|IO entry", eff)
	}
	checkResult(t, mustEval(t, s, []string{"u"}, "c", 0b0001), ResultImplicitDeny, -1, "", false, 0)
	// 传播到对象 o：OI 副本标志 0，参与判定。
	effO := mustEffective(t, s, "o")
	wantO := []EffectiveEntry{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: 0, Source: "c"},
	}
	if !reflect.DeepEqual(effO, wantO) {
		t.Fatalf("E(o) = %+v, want %+v", effO, wantO)
	}
	checkResult(t, mustEval(t, s, []string{"u"}, "o", 0b0001), ResultGranted, 0, "c", true, 0b0001)
}

// 保护开关阻断继承，但保留本节点显式条目。
func TestProtectionBlocksInheritance(t *testing.T) {
	s := mustStore(t, 4, 8)
	mustAdd(t, s, "c", "/", true)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0001, Flags: FlagOI | FlagCI},
	}, false)
	mustSetACL(t, s, "c", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0010, Flags: 0},
	}, true)
	want := []EffectiveEntry{
		{Allow: true, Principal: "u", Mask: 0b0010, Flags: 0, Source: "c"},
	}
	if got := mustEffective(t, s, "c"); !reflect.DeepEqual(got, want) {
		t.Fatalf("E(c) = %+v, want %+v", got, want)
	}
	checkResult(t, mustEval(t, s, []string{"u"}, "c", 0b0001), ResultImplicitDeny, -1, "", false, 0)
	// 取消保护后继承恢复。
	mustSetACL(t, s, "c", []ACE{
		{Allow: true, Principal: "u", Mask: 0b0010, Flags: 0},
	}, false)
	checkResult(t, mustEval(t, s, []string{"u"}, "c", 0b0001), ResultGranted, 1, "/", true, 0b0001)
}
