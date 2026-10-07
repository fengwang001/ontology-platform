package ontology

import (
	"reflect"
	"testing"
)

// 前置钩子：记录每条记录被触发时看到的 cnt 聚合值，
// 用于证明可见内容随列表前缀逐步增长，
// 而非批次开始前的静态快照或结束后的最终状态。
func countingPre(seen *[]float64, failPK map[string]string) PreHook {
	return func(ctx *HookContext, r Record, view HookView) *HookError {
		cnt, _ := view.Aggregate("cnt")
		*seen = append(*seen, cnt)
		if code, bad := failPK[r.PK]; bad {
			return preError(ctx.Index, code, "forced failure on "+r.PK)
		}
		return nil
	}
}

// 场景 1：批内可见性严格按列表顺序。
func TestVisibilityIsPrefixOrdered(t *testing.T) {
	var seen []float64
	reg := testRegistry(countingPre(&seen, nil), nil)
	st := NewStore(reg)
	im := NewImporter(reg, 1)

	records := []Record{rec("a", 1), rec("b", 2), rec("c", 3)}
	dump(t, "visibility-order", join(recsToStrings(records)), nil,
		"cnt seen by each pre-hook = [0 1 2]",
		"前置钩子依序看到 0,1,2 条已存在实例；既非快照(0,0,0)也非最终态(3,3,3)")

	res := im.Import(st, Batch{
		Type: testTypeName, Semantic: SemAllOrNothing, Records: records,
	})
	if !res.Committed {
		t.Fatalf("batch should commit: %+v", res.FirstError())
	}
	want := []float64{0, 1, 2}
	dump(t, "visibility-order", join(recsToStrings(records)),
		seen, want, "每条记录触发时的 cnt 必须等于其下标（前缀长度）")
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("prefix visibility mismatch: got %v want %v", seen, want)
	}
	if len(st.CurrentInstances(testTypeName)) != 3 {
		t.Fatalf("expected 3 committed instances")
	}
}

// 场景 2：同输入下全有全无 vs 尽力而为的差异。
func TestSemanticsDiffer(t *testing.T) {
	records := []Record{rec("a", 1), rec("b", 2), rec("c", 3)}
	fail := map[string]string{"b": "bad_b"}

	// 全有全无：b 失败 => a、c 都不生效。
	var seenAll []float64
	regAll := testRegistry(countingPre(&seenAll, fail), nil)
	stAll := NewStore(regAll)
	resAll := NewImporter(regAll, 1).Import(stAll, Batch{
		Type: testTypeName, Semantic: SemAllOrNothing, Records: records,
	})
	dump(t, "all-or-nothing", join(recsToStrings(records)),
		resultsSummary(resAll.Records),
		"a APPLIED->rolled back, b FAILED(pre), c never processed; committed=false",
		"任意一条前置失败则整批撤销，c 不被处理")
	if resAll.Committed {
		t.Fatalf("all-or-nothing must not commit")
	}
	if got := len(stAll.CurrentInstances(testTypeName)); got != 0 {
		t.Fatalf("all-or-nothing left %d instances, want 0", got)
	}
	if seenAll[len(seenAll)-1] == 2 {
		t.Fatalf("c must not be processed after b fails, saw %v", seenAll)
	}

	// 尽力而为：b 被跳过，a、c 生效；b 不进入 c 的可见范围。
	var seenBE []float64
	regBE := testRegistry(countingPre(&seenBE, fail), nil)
	stBE := NewStore(regBE)
	resBE := NewImporter(regBE, 1).Import(stBE, Batch{
		Type: testTypeName, Semantic: SemBestEffort, Records: records,
	})
	dump(t, "best-effort", join(recsToStrings(records)),
		resultsSummary(resBE.Records),
		"a APPLIED, b FAILED(pre), c APPLIED; committed=true",
		"失败记录被跳过且不影响后续；成功记录正常提交")
	if !resBE.Committed {
		t.Fatalf("best-effort should commit successful records")
	}
	inst := stBE.CurrentInstances(testTypeName)
	if _, ok := inst["b"]; ok {
		t.Fatalf("failed record b must not exist")
	}
	if _, ok := inst["a"]; !ok {
		t.Fatalf("a should be applied")
	}
	if _, ok := inst["c"]; !ok {
		t.Fatalf("c should be applied despite b failing")
	}
	// c 的前置钩子看到的 cnt 应为 1（只有 a；b 被跳过）。
	dump(t, "best-effort-visibility", join(recsToStrings(records)),
		seenBE, []float64{0, 1, 1},
		"c 看到的 cnt=1：失败的 b 不计入后续同批次可见范围")
	if !reflect.DeepEqual(seenBE, []float64{0, 1, 1}) {
		t.Fatalf("failed record leaked into later visibility: %v", seenBE)
	}
}

// 场景 3：后置钩子失败导致整批（含全部单条）一起撤销。
func TestPostHookFailureRollsBackWholeBatch(t *testing.T) {
	var preSeen []float64
	reg := testRegistry(countingPre(&preSeen, nil), func(ctx *HookContext, view HookView) *HookError {
		if sum, _ := view.Aggregate("sum_v"); sum > 5 {
			return postError("sum_too_large", "post hook rejects final state")
		}
		return nil
	})
	st := NewStore(reg)
	records := []Record{rec("a", 1), rec("b", 2), rec("c", 3)} // sum=6 > 5
	res := NewImporter(reg, 1).Import(st, Batch{
		Type: testTypeName, Semantic: SemAllOrNothing, Records: records,
	})
	dump(t, "post-hook-rollback", join(recsToStrings(records)),
		resultsSummary(res.Records),
		"all records reverted; PostError=post_hook/sum_too_large; committed=false",
		"全部前置通过后，后置钩子在提交前看到最终态并拒绝 => 整批撤销")
	if res.Committed || res.PostError == nil ||
		res.PostError.Kind != KindPostHook || res.PostError.Code != "sum_too_large" {
		t.Fatalf("post-hook failure misreported: %+v", res)
	}
	if got := len(st.CurrentInstances(testTypeName)); got != 0 {
		t.Fatalf("post-hook failure must leave zero instances, got %d", got)
	}
	for _, rr := range res.Records {
		if rr.Status != StatusFailed || rr.Err == nil || rr.Err.Code != "batch_rolled_back" {
			t.Fatalf("record %s not marked rolled back: %+v", rr.PK, rr)
		}
	}
}

// 尽力而为语义下不触发后置钩子。
func TestPostHookSkippedUnderBestEffort(t *testing.T) {
	called := false
	reg := testRegistry(func(ctx *HookContext, r Record, view HookView) *HookError { return nil },
		func(ctx *HookContext, view HookView) *HookError {
			called = true
			return postError("should_not_happen", "")
		})
	st := NewStore(reg)
	res := NewImporter(reg, 1).Import(st, Batch{
		Type: testTypeName, Semantic: SemBestEffort,
		Records: []Record{rec("a", 100)},
	})
	if !res.Committed || called {
		t.Fatalf("post hook must not run under best-effort: called=%v committed=%v",
			called, res.Committed)
	}
}

// 场景 4：错误报告优先级——参数非法 > 前置钩子 > 后置钩子，三类可区分。
func TestErrorPriorityAndKinds(t *testing.T) {
	records := []Record{
		rec("dup", 1),
		{PK: "dup", Fields: map[string]Value{"v": "not-an-int"}}, // 主键重复且类型错误
		rec("c", 3),
	}
	reg := testRegistry(
		func(ctx *HookContext, r Record, view HookView) *HookError {
			return preError(ctx.Index, "pre_always", "")
		},
		func(ctx *HookContext, view HookView) *HookError {
			return postError("post_always", "")
		})
	st := NewStore(reg)
	res := NewImporter(reg, 1).Import(st, Batch{
		Type: testTypeName, Semantic: SemAllOrNothing, Records: records,
	})
	first := res.FirstError()
	dump(t, "error-priority-duplicate", join(recsToStrings(records)),
		first, "param_invalid/duplicate_pk",
		"主键重复最先报告，即使同时存在类型错误、前置、后置失败")
	if first.Kind != KindParamInvalid || first.Code != "duplicate_pk" {
		t.Fatalf("want duplicate_pk param error, got %+v", first)
	}

	// 无重复但有类型错误 + 前置失败：类型错误（参数非法）优先。
	recs2 := []Record{rec("a", 1), recBadType("b"), rec("c", 3)}
	res2 := NewImporter(reg, 1).Import(st, Batch{
		Type: testTypeName, Semantic: SemAllOrNothing, Records: recs2,
	})
	first2 := res2.FirstError()
	dump(t, "error-priority-type", join(recsToStrings(recs2)),
		first2, "param_invalid/field_type_mismatch",
		"字段类型不符优先于前置钩子失败")
	if first2.Kind != KindParamInvalid || first2.Code != "field_type_mismatch" {
		t.Fatalf("want field_type_mismatch, got %+v", first2)
	}

	// 仅后置失败：报后置。
	reg3 := testRegistry(
		func(ctx *HookContext, r Record, view HookView) *HookError { return nil },
		func(ctx *HookContext, view HookView) *HookError {
			return postError("post_only", "")
		})
	st3 := NewStore(reg3)
	res3 := NewImporter(reg3, 1).Import(st3, Batch{
		Type: testTypeName, Semantic: SemAllOrNothing,
		Records: []Record{rec("a", 1)},
	})
	if f := res3.FirstError(); f.Kind != KindPostHook || f.Code != "post_only" {
		t.Fatalf("want post_only, got %+v", f)
	}
}

func recBadType(pk string) Record {
	// v 声明为 int，这里给 string，触发字段类型不符。
	return Record{PK: pk, Fields: map[string]Value{"v": "not-an-int"}}
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += " | "
		}
		out += s
	}
	return out
}
