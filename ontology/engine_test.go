package ontology

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func mustEngine(t *testing.T) *Engine {
	t.Helper()
	eng := NewEngine()
	if err := eng.RegisterObjectType(ObjectType{
		Name:  "Doc",
		Attrs: map[string]struct{}{"level": {}, "dept": {}, "title": {}},
	}); err != nil {
		t.Fatalf("RegisterObjectType: %v", err)
	}
	return eng
}

func levelRule(tag string, threshold int64) TagRule {
	return TagRule{
		Tag: tag, ObjectType: "Doc",
		Expr: BinOp{Op: OpGe, L: Attr{"level"}, R: Const{threshold}},
	}
}

func mustWrite(t *testing.T, eng *Engine, subject, id string, attrs map[string]Value) uint64 {
	t.Helper()
	v, _, err := eng.Write(subject, "Doc", id, attrs)
	if err != nil {
		t.Fatalf("Write(%v): %v", attrs, err)
	}
	return v
}

// 标签状态翻转：写入改变属性取值后，后续读取立即按新标签状态裁决。
func TestTagFlipImmediateVisibility(t *testing.T) {
	eng := mustEngine(t)
	if err := eng.ReplaceRules([]TagRule{levelRule("classified", 3)}); err != nil {
		t.Fatalf("ReplaceRules: %v", err)
	}
	eng.SetGrants([]Grant{
		{Tag: "classified", Subject: "alice", Read: EffectDeny, Write: EffectAllow},
	})
	mustWrite(t, eng, "alice", "d1", map[string]Value{"level": int64(1), "title": "spec"})

	if v, _, err := eng.Read("alice", "Doc", "d1", "title", Latest()); err != nil || v != "spec" {
		t.Fatalf("翻转前应可读, got v=%v err=%v", v, err)
	}
	// 写入使 level 越过阈值：标签出现，alice 立即失去读权限。
	mustWrite(t, eng, "alice", "d1", map[string]Value{"level": int64(5)})
	if _, dec, err := eng.Read("alice", "Doc", "d1", "title", Latest()); err != ErrDenied {
		t.Fatalf("翻转后应立即拒绝, got err=%v", err)
	} else {
		if len(dec.CarriedTags) != 1 || dec.CarriedTags[0] != "classified" {
			t.Fatalf("判定依据应包含 classified, got %v", dec.CarriedTags)
		}
	}
	// 再次写回：标签消失，读权限立即恢复。
	mustWrite(t, eng, "alice", "d1", map[string]Value{"level": int64(0)})
	if _, _, err := eng.Read("alice", "Doc", "d1", "title", Latest()); err != nil {
		t.Fatalf("翻回后应恢复可读: %v", err)
	}
}

// 可重复读：快照内的多次读取结果与快照一致，不受快照外并发写入影响。
func TestRepeatableReadSnapshotStability(t *testing.T) {
	eng := mustEngine(t)
	if err := eng.ReplaceRules([]TagRule{levelRule("classified", 3)}); err != nil {
		t.Fatalf("ReplaceRules: %v", err)
	}
	eng.SetGrants([]Grant{
		{Tag: "classified", Subject: "alice", Read: EffectDeny, Write: EffectAllow},
	})
	mustWrite(t, eng, "alice", "d1", map[string]Value{"level": int64(1), "title": "v1"})
	snap := eng.BeginSnapshot()

	// 快照外的并发写入：翻转标签并改值。
	const writers = 8
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, _, err := eng.Write("alice", "Doc", "d1", map[string]Value{
					"level": int64(3 + n), "title": fmt.Sprintf("w%d-%d", n, i),
				}); err != nil {
					t.Errorf("concurrent write: %v", err)
				}
			}
		}(w)
	}
	// 快照内反复读取：取值与标签状态必须始终等于快照时刻的结论。
	for i := 0; i < 200; i++ {
		v, dec, err := eng.Read("alice", "Doc", "d1", "title", snap)
		if err != nil {
			t.Fatalf("快照内读取被拒绝（快照时刻标签未携带）: %v", err)
		}
		if v != "v1" {
			t.Fatalf("快照内读到快照外的值: %v", v)
		}
		if len(dec.CarriedTags) != 0 {
			t.Fatalf("快照内标签状态应始终为空, got %v", dec.CarriedTags)
		}
		ok, _, err := eng.TagState("Doc", "d1", "classified", snap)
		if err != nil || ok {
			t.Fatalf("快照内标签状态应始终为不携带, got ok=%v err=%v", ok, err)
		}
	}
	wg.Wait()
	// 快照外：最新读必须看到并发写入后的标签状态。
	ok, _, err := eng.TagState("Doc", "d1", "classified", Latest())
	if err != nil || !ok {
		t.Fatalf("最新读应看到翻转后的标签, got ok=%v err=%v", ok, err)
	}
}

// 多标签冲突合并：全部 3x3 组合，结论唯一且与授权登记顺序无关。
func TestConflictMergeAllCombinations(t *testing.T) {
	effects := []Effect{EffectUnset, EffectAllow, EffectDeny}
	expect := func(a, b Effect) bool {
		if a == EffectDeny || b == EffectDeny {
			return false
		}
		if a == EffectAllow || b == EffectAllow {
			return true
		}
		return false // 携带标签但无任何适用授权：封闭拒绝
	}
	for _, ea := range effects {
		for _, eb := range effects {
			name := fmt.Sprintf("A=%s/B=%s", ea, eb)
			t.Run(name, func(t *testing.T) {
				eng := mustEngine(t)
				rules := []TagRule{
					{Tag: "TA", ObjectType: "Doc", Expr: Const{true}},
					{Tag: "TB", ObjectType: "Doc", Expr: Const{true}},
				}
				if err := eng.ReplaceRules(rules); err != nil {
					t.Fatalf("ReplaceRules: %v", err)
				}
				// 两种相反的授权登记顺序，结论必须一致。
				for _, grants := range [][]Grant{
					{{Tag: "TA", Subject: "s", Read: ea}, {Tag: "TB", Subject: "s", Read: eb}},
					{{Tag: "TB", Subject: "s", Read: eb}, {Tag: "TA", Subject: "s", Read: ea}},
				} {
					eng.SetGrants(grants)
					_, dec, err := eng.Read("s", "Doc", "d1", "title", Latest())
					got := err == nil
					if want := expect(ea, eb); got != want {
						t.Fatalf("grants=%v: got allowed=%v want=%v (dec=%+v)", grants, got, want, dec)
					}
					if got != dec.Allowed {
						t.Fatalf("返回值与判定日志不一致: err=%v dec=%+v", err, dec)
					}
				}
			})
		}
	}
}

// 可见范围：授权的 Scope 限定其只作用于列出的属性。
func TestGrantScope(t *testing.T) {
	eng := mustEngine(t)
	if err := eng.ReplaceRules([]TagRule{{Tag: "T", ObjectType: "Doc", Expr: Const{true}}}); err != nil {
		t.Fatalf("ReplaceRules: %v", err)
	}
	eng.SetGrants([]Grant{
		{Tag: "T", Subject: "s", Read: EffectAllow, Scope: []string{"title"}},
	})
	if _, _, err := eng.Read("s", "Doc", "d1", "title", Latest()); err != nil {
		t.Fatalf("Scope 内属性应可读: %v", err)
	}
	if _, _, err := eng.Read("s", "Doc", "d1", "dept", Latest()); err != ErrDenied {
		t.Fatalf("Scope 外属性应被拒绝, got %v", err)
	}
}

// 错误类别与固定优先级：未知属性 > 循环依赖 > 快照过期。
func TestErrorPriority(t *testing.T) {
	eng := mustEngine(t)
	mustWriteUnrestricted(t, eng, "d1", map[string]Value{"level": int64(1)})

	// 制造过期快照。
	snap := eng.BeginSnapshot()
	for i := 0; i < 20; i++ {
		mustWriteUnrestricted(t, eng, "d1", map[string]Value{"level": int64(i)})
	}
	eng.Prune()

	// 未知属性 + 过期快照同时成立：必须报未知属性。
	_, _, err := eng.Read("s", "Doc", "d1", "nosuch", snap)
	if k, ok := KindOf(err); !ok || k != ErrKindUnknownAttribute {
		t.Fatalf("应优先报未知属性, got %v", err)
	}
	// 仅快照过期：报快照过期。
	_, _, err = eng.Read("s", "Doc", "d1", "level", snap)
	if k, ok := KindOf(err); !ok || k != ErrKindSnapshotExpired {
		t.Fatalf("应报快照过期, got %v", err)
	}
	// 规则校验：未知属性优先于循环依赖。
	bad := []TagRule{
		{Tag: "A", ObjectType: "Doc", Expr: Attr{"ghost"}},
		{Tag: "B", ObjectType: "Doc", Expr: TagRef{"C"}},
		{Tag: "C", ObjectType: "Doc", Expr: TagRef{"B"}},
	}
	if err := eng.ReplaceRules(bad); err == nil {
		t.Fatal("应拒绝非法规则集")
	} else if k, _ := KindOf(err); k != ErrKindUnknownAttribute {
		t.Fatalf("未知属性应优先于循环依赖, got %v", err)
	}
	// 仅循环依赖。
	cyclic := []TagRule{
		{Tag: "B", ObjectType: "Doc", Expr: TagRef{"C"}},
		{Tag: "C", ObjectType: "Doc", Expr: TagRef{"B"}},
	}
	if err := eng.ReplaceRules(cyclic); err == nil {
		t.Fatal("应检测出循环依赖")
	} else if k, _ := KindOf(err); k != ErrKindCyclicDependency {
		t.Fatalf("应报循环依赖, got %v", err)
	}
	// 被拒绝的规则替换不影响已有规则（无副作用）。
	if err := eng.ReplaceRules([]TagRule{levelRule("classified", 3)}); err != nil {
		t.Fatalf("合法规则集应被接受: %v", err)
	}
}

// 被拒绝的写入不得推进版本时钟、不得改变属性取值。
func TestRejectedWriteHasNoSideEffects(t *testing.T) {
	eng := mustEngine(t)
	if err := eng.ReplaceRules([]TagRule{levelRule("classified", 3)}); err != nil {
		t.Fatalf("ReplaceRules: %v", err)
	}
	eng.SetGrants([]Grant{
		{Tag: "classified", Subject: "mallory", Read: EffectAllow, Write: EffectDeny},
	})
	mustWriteUnrestricted(t, eng, "d1", map[string]Value{"level": int64(9), "title": "orig"})
	before := eng.BeginSnapshot().Version()

	if _, _, err := eng.Write("mallory", "Doc", "d1", map[string]Value{"title": "hacked"}); err != ErrDenied {
		t.Fatalf("应被拒绝, got %v", err)
	}
	if _, _, err := eng.Write("mallory", "Doc", "d1", map[string]Value{"ghost": 1}); err == nil {
		t.Fatal("未知属性写入应被拒绝")
	} else if k, _ := KindOf(err); k != ErrKindUnknownAttribute {
		t.Fatalf("应报未知属性, got %v", err)
	}
	after := eng.BeginSnapshot().Version()
	if before != after {
		t.Fatalf("被拒绝的写入推进了时钟: before=%d after=%d", before, after)
	}
	if v, _, err := eng.Read("mallory", "Doc", "d1", "title", Latest()); err != nil || v != "orig" {
		t.Fatalf("被拒绝的写入改变了取值: v=%v err=%v", v, err)
	}
}

func mustWriteUnrestricted(t *testing.T, eng *Engine, id string, attrs map[string]Value) uint64 {
	t.Helper()
	v, _, err := eng.Write("root", "Doc", id, attrs)
	if err != nil {
		t.Fatalf("Write(%v): %v", attrs, err)
	}
	return v
}

// 判定开销可观测证明：单次标签判定读取的属性数量只等于该规则
// （含 TagRef 传递闭包）实际引用的属性数，与已登记规则总数无关。
func TestEvalCostIndependentOfRuleCount(t *testing.T) {
	eng := mustEngine(t)
	mustWriteUnrestricted(t, eng, "d1", map[string]Value{
		"level": int64(5), "dept": "eng", "title": "x",
	})
	base := []TagRule{
		{Tag: "T1", ObjectType: "Doc", Expr: BinOp{Op: OpGe, L: Attr{"level"}, R: Const{int64(3)}}},
		{Tag: "T2", ObjectType: "Doc", Expr: BinOp{Op: OpAnd,
			L: TagRef{"T1"}, R: BinOp{Op: OpEq, L: Attr{"dept"}, R: Const{"eng"}}}},
	}
	if err := eng.ReplaceRules(base); err != nil {
		t.Fatalf("ReplaceRules: %v", err)
	}
	_, reads1, err := eng.TagState("Doc", "d1", "T1", Latest())
	if err != nil {
		t.Fatalf("TagState: %v", err)
	}
	_, reads2before, err := eng.TagState("Doc", "d1", "T2", Latest())
	if err != nil {
		t.Fatalf("TagState: %v", err)
	}
	if reads1 != 1 {
		t.Fatalf("T1 只引用 level，应读取 1 个属性, got %d", reads1)
	}
	if reads2before != 2 {
		t.Fatalf("T2 传递引用 level+dept，应读取 2 个属性, got %d", reads2before)
	}
	// 再登记 100 条无关规则，同一判定的属性读取数必须不变。
	extra := append([]TagRule{}, base...)
	for i := 0; i < 100; i++ {
		extra = append(extra, TagRule{
			Tag: fmt.Sprintf("noise-%d", i), ObjectType: "Doc",
			Expr: BinOp{Op: OpEq, L: Attr{"title"}, R: Const{fmt.Sprintf("v%d", i)}},
		})
	}
	if err := eng.ReplaceRules(extra); err != nil {
		t.Fatalf("ReplaceRules: %v", err)
	}
	_, reads1after, _ := eng.TagState("Doc", "d1", "T1", Latest())
	_, reads2after, _ := eng.TagState("Doc", "d1", "T2", Latest())
	if reads1after != reads1 || reads2after != reads2before {
		t.Fatalf("属性读取数随规则总数增长: T1 %d->%d, T2 %d->%d",
			reads1, reads1after, reads2before, reads2after)
	}
}

// 防泄漏：规则基于主体不可读属性的真实取值完成判定，
// 但错误信息、判定日志与返回值均不包含该取值。
func TestNoLeakThroughRuleEvaluation(t *testing.T) {
	eng := mustEngine(t)
	secret := "top-secret-project-omega"
	if err := eng.ReplaceRules([]TagRule{
		{Tag: "omega", ObjectType: "Doc",
			Expr: BinOp{Op: OpEq, L: Attr{"dept"}, R: Const{secret}}},
	}); err != nil {
		t.Fatalf("ReplaceRules: %v", err)
	}
	eng.SetGrants([]Grant{
		// alice 对携带 omega 标签的实例：dept 不可读，title 可读。
		{Tag: "omega", Subject: "alice", Read: EffectAllow, Scope: []string{"title"}},
		{Tag: "omega", Subject: "alice", Read: EffectDeny, Scope: []string{"dept"}},
	})
	mustWriteUnrestricted(t, eng, "d1", map[string]Value{"dept": secret, "title": "hello"})

	// 判定仍基于 dept 的真实取值：omega 被携带，title 可读。
	if _, dec, err := eng.Read("alice", "Doc", "d1", "title", Latest()); err != nil {
		t.Fatalf("title 应可读: %v", err)
	} else if len(dec.CarriedTags) != 1 || dec.CarriedTags[0] != "omega" {
		t.Fatalf("应携带 omega, got %v", dec.CarriedTags)
	}
	// alice 读 dept 被拒绝，且错误不含真实取值。
	_, dec, err := eng.Read("alice", "Doc", "d1", "dept", Latest())
	if err != ErrDenied {
		t.Fatalf("dept 应被拒绝, got %v", err)
	}
	if strings.Contains(dec.Err, secret) {
		t.Fatalf("判定记录泄漏了属性取值: %q", dec.Err)
	}
	// 全量日志任何字段都不得包含真实取值。
	for _, d := range eng.Log() {
		blob := fmt.Sprintf("%+v", d)
		if strings.Contains(blob, secret) {
			t.Fatalf("判定日志泄漏了属性取值: %s", blob)
		}
	}
}

// 判定日志完整记录每次调用的输入、输出与判定依据。
func TestDecisionLogCompleteness(t *testing.T) {
	eng := mustEngine(t)
	if err := eng.ReplaceRules([]TagRule{levelRule("classified", 3)}); err != nil {
		t.Fatalf("ReplaceRules: %v", err)
	}
	eng.SetGrants([]Grant{
		{Tag: "classified", Subject: "alice", Read: EffectDeny},
	})
	mustWriteUnrestricted(t, eng, "d1", map[string]Value{"level": int64(7), "title": "t"})
	eng.Read("alice", "Doc", "d1", "title", Latest())

	log := eng.Log()
	if len(log) != 2 {
		t.Fatalf("应有 2 条日志（1 写 1 读）, got %d", len(log))
	}
	read := log[1]
	if read.Op != "read" || read.Subject != "alice" || read.InstanceID != "d1" || read.Attr != "title" {
		t.Fatalf("日志未完整记录输入: %+v", read)
	}
	if read.Allowed || read.Err == "" {
		t.Fatalf("日志未记录最终输出: %+v", read)
	}
	if len(read.CarriedTags) != 1 || read.CarriedTags[0] != "classified" {
		t.Fatalf("日志未记录携带标签: %+v", read)
	}
	if len(read.Basis) != 1 || read.Basis[0].Tag != "classified" || read.Basis[0].Effect != EffectDeny {
		t.Fatalf("日志未记录授权依据: %+v", read)
	}
	if read.AttrReads != 1 {
		t.Fatalf("日志未记录判定开销: %+v", read)
	}
}
