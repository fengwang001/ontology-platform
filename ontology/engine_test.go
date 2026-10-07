package ontology

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func baseSchema() map[string]AttrType {
	return map[string]AttrType{
		"name":   {Kind: KindString, MaxLen: 64},
		"ssn":    {Kind: KindString, MaxLen: 32},
		"dept":   {Kind: KindString, MaxLen: 32},
		"salary": {Kind: KindInt, HasRange: true, Min: 0, Max: 1 << 40},
		"level":  {Kind: KindInt, HasRange: true, Min: 0, Max: 10},
	}
}

func baseInstance() Instance {
	return Instance{ID: "emp-1", Attrs: map[string]Value{
		"name":   StringValue("alice"),
		"ssn":    StringValue("123-45-6789"),
		"dept":   StringValue("eng"),
		"salary": IntValue(100000),
		"level":  IntValue(5),
	}}
}

func newTestEngine() *Engine {
	e := NewEngine(false)
	e.SetSchema(baseSchema())
	return e
}

func errKinds(res Result) []ErrKind {
	kinds := make([]ErrKind, 0, len(res.Errors))
	for _, e := range res.Errors {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

func hasErrKind(res Result, k ErrKind) bool {
	for _, e := range res.Errors {
		if e.Kind == k {
			return true
		}
	}
	return false
}

// 拒绝可见性时，无论登记多少脱敏策略，属性必须整体缺失。
func TestDenyOverridesMasking(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ssn", Effect: Deny})
	e.RegisterMasking(MaskingPolicy{ID: "m1", Subject: "*", Attr: "ssn", Strength: 1,
		Rule: MaskingRule{Kind: RuleRedact}})
	e.RegisterMasking(MaskingPolicy{ID: "m2", Subject: "*", Attr: "ssn", Strength: 9,
		Rule: MaskingRule{Kind: RuleHash}})
	res := e.Present("alice", baseInstance())
	if _, ok := res.Values["ssn"]; ok {
		t.Fatalf("被拒绝的属性不得呈现: %v", res.Values["ssn"])
	}
	if res.Outcome["ssn"].Present {
		t.Fatal("被拒绝的属性 Outcome.Present 必须为 false")
	}
}

// 允许可见且命中脱敏策略时，呈现派生值且原始值不泄露。
func TestAllowWithMasking(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ssn", Effect: Allow})
	e.RegisterMasking(MaskingPolicy{ID: "m1", Subject: "*", Attr: "ssn", Strength: 1,
		Rule: MaskingRule{Kind: RuleRedact}})
	res := e.Present("bob", baseInstance())
	got, ok := res.Values["ssn"]
	if !ok {
		t.Fatal("允许可见的属性必须呈现")
	}
	if got.V != "***" {
		t.Fatalf("应呈现脱敏值，实际 %v", got.V)
	}
	for attr, v := range res.Values {
		if s, ok := v.V.(string); ok && strings.Contains(s, "123-45-6789") {
			t.Fatalf("原始值经属性 %s 泄露", attr)
		}
	}
}

// 可见性判定条件必须基于被引用属性的原始值，即使该属性自身被脱敏。
func TestVisibilityConditionUsesRawValue(t *testing.T) {
	e := newTestEngine()
	// level 对 subject 脱敏为常量 0；name 的可见性条件是 level==5（原始值）。
	e.RegisterMasking(MaskingPolicy{ID: "m1", Subject: "*", Attr: "level", Strength: 1,
		Rule: MaskingRule{Kind: RuleConstant, ParamValue: IntValue(0)}})
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "name", Effect: Allow,
		Cond: &Condition{Attr: "level", Op: OpEq, Value: IntValue(5)}})
	e.RegisterVisibility(VisibilityPolicy{ID: "v2", Subject: "*", Attr: "level", Effect: Allow})
	res := e.Present("carol", baseInstance())
	if _, ok := res.Values["name"]; !ok {
		t.Fatal("条件应基于原始值 level==5 判定为允许，name 必须呈现")
	}
	if res.Values["level"].V != int64(0) {
		t.Fatalf("level 应呈现脱敏值 0，实际 %v", res.Values["level"].V)
	}
	// 原始值 5 不得出现在任何呈现值中。
	for attr, v := range res.Values {
		if i, ok := v.V.(int64); ok && i == 5 {
			t.Fatalf("level 原始值经属性 %s 泄露", attr)
		}
	}
}

// 无策略命中时遵循默认结论。
func TestDefaultVisibility(t *testing.T) {
	deny := newTestEngine()
	if res := deny.Present("d", baseInstance()); len(res.Values) != 0 {
		t.Fatalf("默认拒绝时不得呈现任何属性: %v", res.Values)
	}
	allow := NewEngine(true)
	allow.SetSchema(baseSchema())
	res := allow.Present("d", baseInstance())
	if len(res.Values) != 5 {
		t.Fatalf("默认允许时应呈现全部 5 个属性: %v", res.Values)
	}
}

// 同一属性同时命中允许与拒绝：无法调和的直接冲突。
func TestVisibilityConflict(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "dept", Effect: Allow})
	e.RegisterVisibility(VisibilityPolicy{ID: "v2", Subject: "*", Attr: "dept", Effect: Deny})
	res := e.Present("e", baseInstance())
	if !hasErrKind(res, ErrVisibilityConflict) {
		t.Fatalf("应报告可见性冲突，实际错误 %v", res.Errors)
	}
	if _, ok := res.Values["dept"]; ok {
		t.Fatal("冲突的属性不得呈现")
	}
}

// 多条脱敏策略按强度合并：强度最大者胜出。
func TestMaskingStrengthMerge(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ssn", Effect: Allow})
	e.RegisterMasking(MaskingPolicy{ID: "m-weak", Subject: "*", Attr: "ssn", Strength: 1,
		Rule: MaskingRule{Kind: RuleTruncate, Param: 3}})
	e.RegisterMasking(MaskingPolicy{ID: "m-strong", Subject: "*", Attr: "ssn", Strength: 5,
		Rule: MaskingRule{Kind: RuleRedact}})
	res := e.Present("f", baseInstance())
	if res.Values["ssn"].V != "***" {
		t.Fatalf("强度最大的规则应胜出，实际 %v", res.Values["ssn"].V)
	}
}

// 同强度不同规则：取策略 ID 字典序最小者，与登记顺序无关。
func TestMaskingTieBreakDeterministic(t *testing.T) {
	build := func(order []string) *Engine {
		e := newTestEngine()
		e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ssn", Effect: Allow})
		rules := map[string]MaskingPolicy{
			"m-a": {ID: "m-a", Subject: "*", Attr: "ssn", Strength: 3,
				Rule: MaskingRule{Kind: RuleRedact}},
			"m-b": {ID: "m-b", Subject: "*", Attr: "ssn", Strength: 3,
				Rule: MaskingRule{Kind: RuleTruncate, Param: 2}},
		}
		for _, id := range order {
			e.RegisterMasking(rules[id])
		}
		return e
	}
	r1 := build([]string{"m-a", "m-b"}).Present("g", baseInstance())
	r2 := build([]string{"m-b", "m-a"}).Present("g", baseInstance())
	if r1.Values["ssn"] != r2.Values["ssn"] {
		t.Fatalf("登记顺序不得影响结果: %v vs %v", r1.Values["ssn"], r2.Values["ssn"])
	}
	if r1.Values["ssn"].V != "***" {
		t.Fatalf("同强度应取 ID 最小者 m-a 的 redact，实际 %v", r1.Values["ssn"].V)
	}
}

// 脱敏依赖循环：必须可区分地报告且求值终止。
func TestMaskingCycle(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ssn", Effect: Allow})
	e.RegisterVisibility(VisibilityPolicy{ID: "v2", Subject: "*", Attr: "dept", Effect: Allow})
	e.RegisterMasking(MaskingPolicy{ID: "m1", Subject: "*", Attr: "ssn", Strength: 1,
		Rule: MaskingRule{Kind: RuleFromAttr, InputAttr: "dept"}})
	e.RegisterMasking(MaskingPolicy{ID: "m2", Subject: "*", Attr: "dept", Strength: 1,
		Rule: MaskingRule{Kind: RuleFromAttr, InputAttr: "ssn"}})
	res := e.Present("h", baseInstance()) // 若未检测循环，此调用不会返回
	if !hasErrKind(res, ErrMaskingCycle) {
		t.Fatalf("应报告脱敏循环，实际错误 %v", res.Errors)
	}
	if _, ok := res.Values["ssn"]; ok {
		t.Fatal("循环涉及的属性不得呈现")
	}
	if _, ok := res.Values["dept"]; ok {
		t.Fatal("循环涉及的属性不得呈现")
	}
}

// 自引用循环同样必须检测。
func TestMaskingSelfCycle(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ssn", Effect: Allow})
	e.RegisterMasking(MaskingPolicy{ID: "m1", Subject: "*", Attr: "ssn", Strength: 1,
		Rule: MaskingRule{Kind: RuleFromAttr, InputAttr: "ssn"}})
	res := e.Present("i", baseInstance())
	if !hasErrKind(res, ErrMaskingCycle) {
		t.Fatalf("自引用应报告脱敏循环，实际错误 %v", res.Errors)
	}
}

// 非循环的派生依赖：使用被依赖属性脱敏后的派生值。
func TestFromAttrUsesDerivedValue(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ssn", Effect: Allow})
	e.RegisterVisibility(VisibilityPolicy{ID: "v2", Subject: "*", Attr: "dept", Effect: Allow})
	e.RegisterMasking(MaskingPolicy{ID: "m1", Subject: "*", Attr: "ssn", Strength: 1,
		Rule: MaskingRule{Kind: RuleRedact}})
	e.RegisterMasking(MaskingPolicy{ID: "m2", Subject: "*", Attr: "dept", Strength: 1,
		Rule: MaskingRule{Kind: RuleFromAttr, InputAttr: "ssn"}})
	res := e.Present("j", baseInstance())
	if res.Values["dept"].V != "***" {
		t.Fatalf("dept 应取 ssn 脱敏后的派生值 ***，实际 %v", res.Values["dept"].V)
	}
}

// 派生值违反声明类型：可区分报告，且不影响其他属性与其他主体。
func TestTypeViolationIsolated(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "name", Effect: Allow})
	e.RegisterVisibility(VisibilityPolicy{ID: "v2", Subject: "*", Attr: "dept", Effect: Allow})
	// 仅对 subject "mallory" 生效的违约规则：name 派生出 int。
	e.RegisterMasking(MaskingPolicy{ID: "m1", Subject: "mallory", Attr: "name", Strength: 1,
		Rule: MaskingRule{Kind: RuleConstant, ParamValue: IntValue(7)}})

	res := e.Present("mallory", baseInstance())
	if !hasErrKind(res, ErrTypeViolation) {
		t.Fatalf("应报告类型违约，实际错误 %v", res.Errors)
	}
	if _, ok := res.Values["name"]; ok {
		t.Fatal("违约属性不得呈现")
	}
	if res.Values["dept"].V != "eng" {
		t.Fatalf("其他属性不得受影响，实际 dept=%v", res.Values["dept"])
	}

	other := e.Present("grace", baseInstance())
	if other.Values["name"].V != "alice" {
		t.Fatalf("其他主体不得受影响，实际 name=%v", other.Values["name"])
	}
	if len(other.Errors) != 0 {
		t.Fatalf("其他主体不应有错误: %v", other.Errors)
	}
}

// 策略引用不存在的属性：可区分报告。
func TestMissingReference(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ghost", Effect: Allow})
	e.RegisterVisibility(VisibilityPolicy{ID: "v2", Subject: "*", Attr: "name", Effect: Allow,
		Cond: &Condition{Attr: "nope", Op: OpEq, Value: IntValue(1)}})
	inst := baseInstance()
	inst.Attrs["ghost"] = StringValue("boo") // 实例存在但 schema 未声明
	res := e.Present("k", inst)
	if !hasErrKind(res, ErrMissingRef) {
		t.Fatalf("应报告引用缺失，实际错误 %v", res.Errors)
	}
	count := 0
	for _, err := range res.Errors {
		if err.Kind == ErrMissingRef {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("应有两处引用缺失（ghost 与条件引用的 nope），实际 %d: %v", count, res.Errors)
	}
}

// 错误必须按固定优先顺序汇报：引用缺失 < 可见性冲突 < 脱敏循环 < 类型违约。
func TestErrorPrecedenceOrder(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v-ghost", Subject: "*", Attr: "ghost", Effect: Allow})
	e.RegisterVisibility(VisibilityPolicy{ID: "v-allow", Subject: "*", Attr: "dept", Effect: Allow})
	e.RegisterVisibility(VisibilityPolicy{ID: "v-deny", Subject: "*", Attr: "dept", Effect: Deny})
	e.RegisterVisibility(VisibilityPolicy{ID: "v-level", Subject: "*", Attr: "level", Effect: Allow})
	e.RegisterMasking(MaskingPolicy{ID: "m-cycle", Subject: "*", Attr: "level", Strength: 1,
		Rule: MaskingRule{Kind: RuleFromAttr, InputAttr: "level"}})
	e.RegisterVisibility(VisibilityPolicy{ID: "v-name", Subject: "*", Attr: "name", Effect: Allow})
	e.RegisterMasking(MaskingPolicy{ID: "m-bad", Subject: "*", Attr: "name", Strength: 1,
		Rule: MaskingRule{Kind: RuleConstant, ParamValue: IntValue(7)}})

	inst := baseInstance()
	inst.Attrs["ghost"] = StringValue("boo")
	res := e.Present("l", inst)
	want := []ErrKind{ErrMissingRef, ErrVisibilityConflict, ErrMaskingCycle, ErrTypeViolation}
	got := errKinds(res)
	if len(got) != len(want) {
		t.Fatalf("期望 4 类错误各一，实际 %v", res.Errors)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("错误顺序应为 %v，实际 %v", want, got)
		}
	}
}

// 运行期策略变更原子生效：后续呈现立即反映新策略，
// 且单次请求内不得出现新旧混杂的中间状态。
func TestAtomicPolicyChange(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v-ssn", Subject: "*", Attr: "ssn", Effect: Allow})
	e.RegisterVisibility(VisibilityPolicy{ID: "v-dept", Subject: "*", Attr: "dept", Effect: Allow})

	oldRes := e.Present("m", baseInstance())
	if oldRes.Values["ssn"].V != "123-45-6789" || oldRes.Values["dept"].V != "eng" {
		t.Fatalf("变更前结果异常: %v", oldRes.Values)
	}

	batch := PolicyBatch{
		RegisterVisibility: []VisibilityPolicy{
			{ID: "v-ssn", Subject: "*", Attr: "ssn", Effect: Deny},
		},
		RegisterMasking: []MaskingPolicy{
			{ID: "m-dept", Subject: "*", Attr: "dept", Strength: 1,
				Rule: MaskingRule{Kind: RuleRedact}},
		},
	}
	revert := PolicyBatch{
		RegisterVisibility: []VisibilityPolicy{
			{ID: "v-ssn", Subject: "*", Attr: "ssn", Effect: Allow},
		},
		Unregister: []string{"m-dept"},
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				e.Apply(batch)
				e.Apply(revert)
			}
		}
	}()

	for i := 0; i < 2000; i++ {
		res := e.Present("m", baseInstance())
		ssn, ssnOK := res.Values["ssn"]
		dept := res.Values["dept"]
		stateOld := ssnOK && ssn.V == "123-45-6789" && dept.V == "eng"
		stateNew := !ssnOK && dept.V == "***"
		if !stateOld && !stateNew {
			close(stop)
			wg.Wait()
			t.Fatalf("出现新旧策略混杂的中间状态: ssn=%v dept=%v", ssn, dept)
		}
	}
	close(stop)
	wg.Wait()

	e.Apply(batch)
	newRes := e.Present("m", baseInstance())
	if _, ok := newRes.Values["ssn"]; ok {
		t.Fatal("变更后 ssn 应立即被拒绝呈现")
	}
	if newRes.Values["dept"].V != "***" {
		t.Fatalf("变更后 dept 应立即脱敏，实际 %v", newRes.Values["dept"])
	}
}

// 并发登记、变更与呈现：结果必须等价于某个串行顺序。
func TestConcurrentLinearizable(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v-ssn", Subject: "*", Attr: "ssn", Effect: Allow})
	const policies = 64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < policies; i++ {
				id := fmt.Sprintf("m-%d-%d", g, i)
				e.RegisterMasking(MaskingPolicy{ID: id, Subject: "*", Attr: "ssn",
					Strength: i, Rule: MaskingRule{Kind: RuleRedact}})
				e.Present("n", baseInstance())
				if i%3 == 0 {
					e.Unregister(id)
				}
			}
		}(g)
	}
	wg.Wait()

	// 最终状态：每个 goroutine 留下 i%3!=0 的策略；强度最大者胜出。
	res := e.Present("n", baseInstance())
	maxStrength := -1
	for g := 0; g < 8; g++ {
		for i := 0; i < policies; i++ {
			if i%3 != 0 && i > maxStrength {
				maxStrength = i
			}
		}
	}
	found := false
	for _, id := range res.Outcome["ssn"].PolicyIDs {
		var g, i int
		if n, _ := fmt.Sscanf(id, "m-%d-%d", &g, &i); n == 2 && i == maxStrength {
			found = true
		}
	}
	if !found {
		t.Fatalf("最终强度 %d 的策略应参与裁决: %v", maxStrength, res.Outcome["ssn"].PolicyIDs)
	}
}

// 呈现请求不得改变策略状态：重复呈现结果一致。
func TestPresentIsReadOnly(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ssn", Effect: Deny})
	r1 := e.Present("o", baseInstance())
	r2 := e.Present("o", baseInstance())
	if len(r1.Values) != len(r2.Values) || len(r1.Errors) != len(r2.Errors) {
		t.Fatal("呈现请求不得影响后续结果")
	}
	if _, ok := r2.Values["ssn"]; ok {
		t.Fatal("拒绝策略不得因呈现请求而改变")
	}
}

// 考察的策略数量不得随系统中策略总数增长。
func TestExaminedIndependentOfTotal(t *testing.T) {
	build := func(extra int) *Engine {
		e := newTestEngine()
		e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ssn", Effect: Allow})
		e.RegisterMasking(MaskingPolicy{ID: "m1", Subject: "*", Attr: "ssn", Strength: 1,
			Rule: MaskingRule{Kind: RuleRedact}})
		for i := 0; i < extra; i++ {
			attr := fmt.Sprintf("other-%d", i)
			e.RegisterVisibility(VisibilityPolicy{ID: "vx-" + attr, Subject: "*", Attr: attr, Effect: Deny})
			e.RegisterMasking(MaskingPolicy{ID: "mx-" + attr, Subject: "nobody", Attr: attr,
				Strength: 1, Rule: MaskingRule{Kind: RuleRedact}})
		}
		return e
	}
	small := build(0).Present("p", baseInstance())
	large := build(2000).Present("p", baseInstance())
	if small.Examined != large.Examined {
		t.Fatalf("考察策略数不得随总数增长: %d vs %d", small.Examined, large.Examined)
	}
}

// 每次调用都必须完整记录输入、输出与裁决依据。
func TestCallLoggerRecordsEverything(t *testing.T) {
	e := newTestEngine()
	e.RegisterVisibility(VisibilityPolicy{ID: "v1", Subject: "*", Attr: "ssn", Effect: Allow})
	e.RegisterMasking(MaskingPolicy{ID: "m1", Subject: "*", Attr: "ssn", Strength: 1,
		Rule: MaskingRule{Kind: RuleRedact}})
	var buf strings.Builder
	e.SetLogger(NewJSONLogger(&buf))

	inst := baseInstance()
	res := e.Present("logger-subject", inst)

	line := buf.String()
	if !strings.Contains(line, `"logger-subject"`) {
		t.Fatal("日志必须记录主体")
	}
	if !strings.Contains(line, "123-45-6789") {
		t.Fatal("日志必须记录输入实例")
	}
	if !strings.Contains(line, "v1") || !strings.Contains(line, "m1") {
		t.Fatal("日志必须记录据以裁决的策略 ID")
	}
	if !strings.Contains(line, `\u002a\u002a\u002a`) && !strings.Contains(line, "***") {
		t.Fatal("日志必须记录最终输出")
	}
	if res.Outcome["ssn"].PolicyIDs == nil {
		t.Fatal("结果必须携带裁决依据")
	}
}
