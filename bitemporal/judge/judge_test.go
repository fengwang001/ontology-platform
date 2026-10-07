package judge

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/bitemporal"
	"ontology/bitemporal/registry"
)

func p(v int64) *int64 { return &v }

func bi(start, end *int64) *bitemporal.Interval {
	return &bitemporal.Interval{Start: start, End: end,
		StartClosed: bitemporal.Closed, EndClosed: bitemporal.Open}
}

func newTestEngine() *Engine {
	return NewEngine(registry.New())
}

func axesOf(losses []AxisLoss) []bitemporal.Axis {
	out := make([]bitemporal.Axis, len(losses))
	for i, l := range losses {
		out[i] = l.Axis
	}
	return out
}

// 两条时间轴各自记录与否的全部四种组合，逐个方向判定。
func TestAxisPresenceMatrix(t *testing.T) {
	eng := newTestEngine()

	biRec := bitemporal.Record{ID: "bi",
		Valid:       bi(p(1), p(10)),
		Transaction: bi(p(2), p(8))}
	txRec := bitemporal.Record{ID: "tx", Transaction: bi(p(2), p(8))}

	t.Run("V2双轴降级到V1只事务轴_判定损失且具名有效轴", func(t *testing.T) {
		r := eng.Judge(biRec, "V2", "V1")
		if r.Verdict != VerdictLossy {
			t.Fatalf("判定 = %s, 期望 lossy", r.Verdict)
		}
		if got := axesOf(r.Losses); !reflect.DeepEqual(got,
			[]bitemporal.Axis{bitemporal.ValidTime}) {
			t.Fatalf("损失轴 = %v，必须明确报告为有效时间轴", got)
		}
		for _, l := range r.Losses {
			if l.Axis == bitemporal.TransactionTime {
				t.Fatal("不得把事务时间轴报告为损失")
			}
		}
		if len(r.Fills) != 0 {
			t.Fatal("降级不应产生填充")
		}
	})

	t.Run("V1只事务轴升级到V2双轴_固定规则填充有效轴", func(t *testing.T) {
		r := eng.Judge(txRec, "V1", "V2")
		if r.Verdict != VerdictCompatible {
			t.Fatalf("有确定填充规则时升级应判定兼容，得到 %s", r.Verdict)
		}
		if len(r.Fills) != 1 || r.Fills[0].Axis != bitemporal.ValidTime ||
			r.Fills[0].Rule != FillRuleEntireTimeline {
			t.Fatalf("必须用唯一固定规则填充有效时间轴，得到 %+v", r.Fills)
		}
	})

	t.Run("V2双轴到V2双轴_完全兼容", func(t *testing.T) {
		r := eng.Judge(biRec, "V2", "V2")
		if r.Verdict != VerdictCompatible || len(r.Losses) != 0 || len(r.Fills) != 0 {
			t.Fatalf("同版本同能力应兼容，得到 %+v", r)
		}
	})

	t.Run("V2双轴降级到V4只有效轴_损失事务轴", func(t *testing.T) {
		r := eng.Judge(biRec, "V2", "V4")
		if r.Verdict != VerdictLossy {
			t.Fatalf("判定 = %s，期望 lossy", r.Verdict)
		}
		if got := axesOf(r.Losses); !reflect.DeepEqual(got,
			[]bitemporal.Axis{bitemporal.TransactionTime}) {
			t.Fatalf("损失轴 = %v，必须为事务时间轴", got)
		}
	})

	t.Run("V1事务到V4有效_轴能力互换", func(t *testing.T) {
		r := eng.Judge(txRec, "V1", "V4")
		// 目标不记录事务轴 => 事务轴损失；目标记录有效轴而源没有 => 固定填充。
		if r.Verdict != VerdictLossy {
			t.Fatalf("存在轴损失，应为 lossy，得到 %s", r.Verdict)
		}
		if got := axesOf(r.Losses); !reflect.DeepEqual(got,
			[]bitemporal.Axis{bitemporal.TransactionTime}) {
			t.Fatalf("损失轴 = %v，期望事务轴", got)
		}
		if len(r.Fills) != 1 || r.Fills[0].Axis != bitemporal.ValidTime {
			t.Fatalf("有效轴应被固定填充，得到 %+v", r.Fills)
		}
	})
}

func TestBoundarySemantics(t *testing.T) {
	eng := newTestEngine()
	rec := bitemporal.Record{ID: "r",
		Valid:       bi(p(1), p(10)),
		Transaction: bi(p(2), p(8))}

	t.Run("V2[)到V3[]端点闭合差异改变归属_不兼容", func(t *testing.T) {
		r := eng.Judge(rec, "V2", "V3")
		if r.Verdict != VerdictIncompatible {
			t.Fatalf("判定 = %s，期望 incompatible", r.Verdict)
		}
		if len(r.BoundaryIssues) != 2 {
			t.Fatalf("两条轴都应有归属差异，得到 %d", len(r.BoundaryIssues))
		}
		for _, issue := range r.BoundaryIssues {
			if !issue.ChangesMembership || !issue.HasWitness {
				t.Fatalf("必须给出改变归属的见证点，得到 %+v", issue)
			}
		}
	})

	t.Run("仅无界端点闭合位不同_归属不变_兼容", func(t *testing.T) {
		// 自定义版本 V2C：记录双轴、约定为 []，记录使用两端无界区间，
		// [] 与 [) 的闭合差异在无界端点上没有任何有限查询点可观察。
		unbounded := bitemporal.Record{ID: "u",
			Valid:       &bitemporal.Interval{},
			Transaction: &bitemporal.Interval{}}
		r := eng.Judge(unbounded, "V2", "V3")
		if r.Verdict != VerdictCompatible {
			t.Fatalf("无界端点闭合差异不应导致不兼容或损失，得到 %s %+v",
				r.Verdict, r.BoundaryIssues)
		}
	})

	t.Run("V3V与V2仅有效轴闭合不同_事务轴不影响判定", func(t *testing.T) {
		r := eng.Judge(rec, "V2", "V3V")
		if r.Verdict != VerdictIncompatible {
			t.Fatalf("有效轴右端点翻转应判不兼容，得到 %s", r.Verdict)
		}
		if len(r.BoundaryIssues) != 1 ||
			r.BoundaryIssues[0].Axis != bitemporal.ValidTime {
			t.Fatalf("应只报告有效轴差异，得到 %+v", r.BoundaryIssues)
		}
	})
}

func TestRejectedRecord(t *testing.T) {
	eng := newTestEngine()

	t.Run("自洽性错误先于边界不兼容", func(t *testing.T) {
		// V2 -> V3 本会因边界语义被判不兼容；但记录有效轴起点晚于终点，
		// 必须先按记录自身不自洽拒绝并与版本不兼容区分。
		rec := bitemporal.Record{ID: "bad",
			Valid:       bi(p(20), p(10)),
			Transaction: bi(p(2), p(8))}
		r := eng.Judge(rec, "V2", "V3")
		if r.Verdict != VerdictRejected {
			t.Fatalf("判定 = %s，期望 rejected", r.Verdict)
		}
		if len(r.RejectErrors) != 1 ||
			r.RejectErrors[0].Axis != bitemporal.ValidTime {
			// 事务轴自洽，只报有效轴。
			t.Fatalf("拒绝原因应精确指向有效轴，得到 %+v", r.RejectErrors)
		}
	})

	t.Run("自洽性错误先于未知版本", func(t *testing.T) {
		rec := bitemporal.Record{ID: "bad", Valid: bi(p(9), p(1))}
		r := eng.Judge(rec, "V2", "VX")
		if r.Verdict != VerdictRejected {
			t.Fatalf("判定 = %s，自洽性必须最先判定", r.Verdict)
		}
	})
}

func TestUnknownVersion(t *testing.T) {
	eng := newTestEngine()
	rec := bitemporal.Record{ID: "r",
		Valid:       bi(p(1), p(10)),
		Transaction: bi(p(2), p(8))}
	r := eng.Judge(rec, "V2", "V999")
	if r.Verdict != VerdictUnknownVersion || len(r.UnknownReasons) == 0 {
		t.Fatalf("未知版本应单列判定，得到 %+v", r)
	}
	r = eng.Judge(rec, "V0", "V999")
	if r.Verdict != VerdictUnknownVersion || len(r.UnknownReasons) != 2 {
		t.Fatalf("源与目标均未知时应给出两条原因，得到 %+v", r)
	}
}

func TestJudgeDoesNotMutateAndIsStable(t *testing.T) {
	eng := newTestEngine()
	rec := bitemporal.Record{ID: "r",
		Valid:       bi(p(1), p(10)),
		Transaction: bi(p(2), p(8))}
	snapValid, snapTx := *rec.Valid, *rec.Transaction

	first := eng.Judge(rec, "V2", "V1")
	const goroutines = 64
	var wg sync.WaitGroup
	results := make([]Result, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i] = eng.Judge(rec, "V2", "V1")
		}()
	}
	wg.Wait()
	for i := range results {
		if !reflect.DeepEqual(results[i], first) {
			t.Fatalf("第 %d 次并发判定与首次不一致", i)
		}
	}
	if *rec.Valid != snapValid || *rec.Transaction != snapTx {
		t.Fatal("判定过程修改了被判定记录")
	}

	// 同一条记录反复执行同一次迁移判定，结果不变。
	for i := 0; i < 100; i++ {
		if got := eng.Judge(rec, "V2", "V1"); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次重复判定结果发生变化", i)
		}
	}
}

func TestJudgeBatchLinearShape(t *testing.T) {
	eng := newTestEngine()
	rec := bitemporal.Record{ID: "r", Valid: bi(p(1), p(10)), Transaction: bi(p(2), p(8))}
	for _, n := range []int{0, 1, 100} {
		recs := make([]bitemporal.Record, n)
		for i := range recs {
			recs[i] = rec
			recs[i].ID = fmt.Sprintf("r%d", i)
		}
		got := eng.JudgeBatch(recs, "V2", "V1")
		if len(got) != n {
			t.Fatalf("n=%d 结果数 = %d", n, len(got))
		}
	}
}
