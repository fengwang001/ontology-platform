package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 场景 9（核心差分测试）：
// 在大量随机「初始状态 + 随机批量导入序列」上，
// 将真实 Importer 与独立的 NaiveModel 逐条对照：
//   - 每条记录的成功/失败、错误类别与原因码；
//   - FirstError 的归一化结果；
//   - 提交标志；
//   - 序列结束后的最终全量状态。
// 两语义、参数非法、前置失败、后置失败均由同一随机序列覆盖。

// diffHooks 返回参数化的钩子集合，规则只依赖可见状态，保证确定性：
//   - 前置：拒绝规则
//     1) 同批次/已存在主键计数达到阈值（用来观察顺序可见性）
//     2) 特定 v 值（v%97==0）触发确定性失败
//   - 后置：最终 sum_v 达到阈值即整批拒绝
func diffHooks(postRejectSum int64) (PreHook, PostHook) {
	pre := func(ctx *HookContext, r Record, view HookView) *HookError {
		v := r.Fields["v"].(int64)
		if v > 0 && v%97 == 0 {
			return preError(ctx.Index, "pre_v_mod97",
				fmt.Sprintf("v=%d hits deterministic pre failure", v))
		}
		// 依赖派生聚合：当前前缀计数 >= cap 时拒绝，
		// 该判定必须按列表顺序确定性解析。
		if cnt, _ := view.Aggregate("cnt"); cnt >= 8 {
			return preError(ctx.Index, "pre_cap_reached", "prefix count cap")
		}
		return nil
	}
	post := func(ctx *HookContext, view HookView) *HookError {
		if s, _ := view.Aggregate("sum_v"); s >= float64(postRejectSum) {
			return postError("post_sum_cap",
				fmt.Sprintf("final sum %.0f >= %d", s, postRejectSum))
		}
		return nil
	}
	return pre, post
}

type diffCase struct {
	sem     Semantics
	records []Record
}

func randomInitial(rng *rand.Rand) []Record {
	n := rng.Intn(15)
	out := make([]Record, n)
	for i := 0; i < n; i++ {
		// 初始状态避免触发 mod97，保证起点干净。
		out[i] = rec(fmt.Sprintf("init%02d", i), int64(1+rng.Intn(50)))
	}
	return out
}

func randomBatch(rng *rand.Rand) []Record {
	n := 1 + rng.Intn(14)
	out := make([]Record, 0, n)
	for i := 0; i < n; i++ {
		pk := fmt.Sprintf("k%02d", rng.Intn(12))
		v := int64(rng.Intn(120)) // 可能落在 mod97 或非 int（见下）
		// 5% 概率制造字段类型不符（参数非法）。
		if rng.Intn(20) == 0 {
			out = append(out, Record{PK: pk,
				Fields: map[string]Value{"v": fmt.Sprintf("str%d", v)}})
			continue
		}
		out = append(out, rec(pk, v))
	}
	// 8% 概率制造批内主键重复（整批参数非法）。
	if len(out) >= 2 && rng.Intn(12) == 0 {
		out[len(out)-1] = rec(out[0].PK, 5)
	}
	return out
}

func resultKinds(res []RecordResult) []string {
	out := make([]string, len(res))
	for i, r := range res {
		if r.Err == nil {
			out[i] = string(r.Status)
		} else {
			out[i] = string(r.Status) + ":" + string(r.Err.Kind) + "/" + r.Err.Code
		}
	}
	return out
}

func firstErrSignature(e *HookError) string {
	if e == nil {
		return "<nil>"
	}
	return string(e.Kind) + "/" + e.Code
}

func TestRandomDifferentialAgainstNaiveModel(t *testing.T) {
	const trials = 400
	rng := rand.New(rand.NewSource(20261007))

	var printed int
	for trial := 0; trial < trials; trial++ {
		pre, post := diffHooks(300)
		reg := testRegistry(pre, post)

		// 相同的初始状态：真实存储 与 朴素模型。
		initial := randomInitial(rng)
		stReal := NewStore(reg)
		// 先把初始记录以一次导入落库（两边用同一份）。
		NewImporter(reg, 1).Import(stReal, Batch{
			Type: testTypeName, Semantic: SemAllOrNothing, Records: initial,
		})
		naive := NewNaiveModel(reg, stReal)

		// 生成一组（4~9 个）随机批次，两语义交替。
		var cases []diffCase
		var inputsDesc []string
		steps := 4 + rng.Intn(6)
		for s := 0; s < steps; s++ {
			b := randomBatch(rng)
			sem := SemBestEffort
			if s%2 == 0 {
				sem = SemAllOrNothing
			}
			cases = append(cases, diffCase{sem: sem, records: b})
			inputsDesc = append(inputsDesc,
				fmt.Sprintf("%s[%s]", sem, join(recsToStrings(b))))
		}

		workers := 1 + rng.Intn(8)
		var realResults, naiveResults [][]string
		var realFirst, naiveFirst []string
		var realCommit, naiveCommit []bool

		for _, c := range cases {
			rr := NewImporter(reg, workers).Import(stReal, Batch{
				Type: testTypeName, Semantic: c.sem, Records: c.records,
			})
			nr := naive.Run(Batch{
				Type: testTypeName, Semantic: c.sem, Records: c.records,
			})
			realResults = append(realResults, resultKinds(rr.Records))
			naiveResults = append(naiveResults, resultKinds(nr.Records))
			realFirst = append(realFirst, firstErrSignature(rr.FirstError()))
			naiveFirst = append(naiveFirst, firstErrSignature(nr.FirstError()))
			realCommit = append(realCommit, rr.Committed)
			naiveCommit = append(naiveCommit, nr.Committed)

			// 每一步立即对比全量状态，尽早定位首个分叉点。
			if !reflect.DeepEqual(stReal.CurrentInstances(testTypeName),
				naive.State(testTypeName)) {
				t.Errorf("trial %d state diverges after step %d (%s)",
					trial, len(realResults)-1, c.sem)
				rs := stReal.CurrentInstances(testTypeName)
				ns := naive.State(testTypeName)
				for k, v := range rs {
					if !reflect.DeepEqual(v, ns[k]) {
						t.Errorf("  real key %s=%v naive=%v", k, v, ns[k])
					}
				}
				for k, v := range ns {
					if _, ok := rs[k]; !ok {
						t.Errorf("  only-naive key %s=%v", k, v)
					}
				}
				t.Fatalf("first divergence input: %s",
					join(recsToStrings(c.records)))
			}
		}

		finalReal := stReal.CurrentInstances(testTypeName)
		finalNaive := naive.State(testTypeName)

		ok := reflect.DeepEqual(realResults, naiveResults) &&
			reflect.DeepEqual(realFirst, naiveFirst) &&
			reflect.DeepEqual(realCommit, naiveCommit) &&
			reflect.DeepEqual(finalReal, finalNaive)

		if !ok || printed < 3 {
			dump(t, fmt.Sprintf("random-trial-%d (workers=%d)", trial, workers),
				join(inputsDesc),
				map[string]any{
					"kinds":  realResults,
					"first":  realFirst,
					"commit": realCommit,
					"finalN": len(finalReal),
				},
				map[string]any{
					"kinds":  naiveResults,
					"first":  naiveFirst,
					"commit": naiveCommit,
					"finalN": len(finalNaive),
				},
				"逐条判定/首错归一化/提交标志/最终全量状态 与朴素模型完全一致")
			printed++
		}

		if !ok {
			t.Errorf("trial %d detail:", trial)
			if !reflect.DeepEqual(realResults, naiveResults) {
				for s := range realResults {
					if !reflect.DeepEqual(realResults[s], naiveResults[s]) {
						t.Errorf("  step %d kinds\n  real=%v\n  naive=%v",
							s, realResults[s], naiveResults[s])
					}
				}
			}
			if !reflect.DeepEqual(finalReal, finalNaive) {
				t.Errorf("  final state differs: realLen=%d naiveLen=%d",
					len(finalReal), len(finalNaive))
				for k := range finalReal {
					if !reflect.DeepEqual(finalReal[k], finalNaive[k]) {
						t.Errorf("    key %s real=%v naive=%v",
							k, finalReal[k], finalNaive[k])
					}
				}
				for k := range finalNaive {
					if _, ok := finalReal[k]; !ok {
						t.Errorf("    key %s missing in real; naive=%v", k, finalNaive[k])
					}
				}
			}
			t.Fatalf("differential mismatch at trial %d", trial)
		}
	}
	t.Logf("differential trials passed: %d", trials)
}
