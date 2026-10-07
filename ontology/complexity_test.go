package ontology

import (
	"fmt"
	"testing"
)

// 场景 8（可验证复杂度证明）：
// 每条记录的前置钩子固定只访问固定数量的视图条目
// （这里：自己 1 次 Get + 1 次聚合，共常数次探测）。
// 随着批次总长度 N 增长，该记录可见性解析的底层探测次数必须保持常数，
// 且 PrefixScans 恒为 0（实现从不扫描批内已应用列表）。
func TestVisibilityResolutionIsConstantPerAccess(t *testing.T) {
	sizes := []int{128, 1024, 4096}
	type row struct {
		n            int
		probesMid    int
		probesLast   int
		prefixScans  int
		nsPerResolve int64
	}
	var rows []row

	for _, n := range sizes {
		records := make([]Record, n)
		for i := 0; i < n; i++ {
			records[i] = rec(fmt.Sprintf("p%06d", i), int64(i))
		}

		var midProbes, lastProbes int
		reg := testRegistry(func(ctx *HookContext, r Record, view HookView) *HookError {
			// 固定访问：1 次 Get + 1 次 Aggregate（通过 ScopedView 计数）。
			if sv, ok := view.(*ScopedView); ok {
				_ = sv.Exists(r.PK)
				_, _ = sv.Aggregate("sum_v")
				if ctx.Index == n/2 {
					midProbes = sv.c.Probes
				}
				if ctx.Index == n-1 {
					lastProbes = sv.c.Probes
				}
			}
			return nil
		}, nil)
		st := NewStore(reg)
		res := NewImporter(reg, 1).Import(st, Batch{
			Type: testTypeName, Semantic: SemAllOrNothing, Records: records,
		})
		if !res.Committed {
			t.Fatalf("n=%d did not commit", n)
		}

		// 所有记录的 PrefixScans 必须恒为 0。
		maxScans := 0
		for _, rr := range res.Records {
			if rr.Access.PrefixScans > maxScans {
				maxScans = rr.Access.PrefixScans
			}
		}
		rows = append(rows, row{
			n: n, probesMid: midProbes, probesLast: lastProbes,
			prefixScans: maxScans,
		})
		dump(t, fmt.Sprintf("complexity-n=%d", n),
			fmt.Sprintf("batch length %d, each hook does 1 Get + 1 Aggregate", n),
			fmt.Sprintf("midProbes=%d lastProbes=%d prefixScans=%d",
				midProbes, lastProbes, maxScans),
			"probes constant (2), prefixScans 0",
			"解析只做常数次哈希探测，从不随批次长度扫描前缀")
	}

	// 结构化断言：探测次数在所有 N 下相等（常数，具体值取决于
	// 命中 pending 还是快照，均为至多两次 O(1) 哈希探测），扫描恒 0。
	const wantProbes = 3 // Exists: pending miss + snapshot hit；Aggregate: 1
	for _, r := range rows {
		if r.probesMid != wantProbes || r.probesLast != wantProbes {
			t.Fatalf("n=%d probes not constant: mid=%d last=%d",
				r.n, r.probesMid, r.probesLast)
		}
		if r.prefixScans != 0 {
			t.Fatalf("n=%d implementation scanned batch prefix", r.n)
		}
	}
}

// 补充证明：钩子访问 k 条不同记录时，探测数只随 k 增长，
// 与「之前总共有多少条记录」无关。
func TestProbesTrackAccessedNotPosition(t *testing.T) {
	n := 2000
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		records[i] = rec(fmt.Sprintf("q%04d", i), 1)
	}
	const accessK = 5
	var lastProbes int
	reg := testRegistry(func(ctx *HookContext, r Record, view HookView) *HookError {
		if sv, ok := view.(*ScopedView); ok && ctx.Index == n-1 {
			for k := 0; k < accessK; k++ {
				// 访问位置之前实际存在的 5 个主键（与 N 无关）。
				_ = sv.Exists(fmt.Sprintf("q%04d", ctx.Index-1-k))
			}
			lastProbes = sv.c.Probes
		}
		return nil
	}, nil)
	st := NewStore(reg)
	if res := NewImporter(reg, 1).Import(st, Batch{
		Type: testTypeName, Semantic: SemAllOrNothing, Records: records,
	}); !res.Committed {
		t.Fatal(res.FirstError())
	}
	// 每条 Exists 至多 2 次哈希探测 => 上界 2*k；与 n=2000 无关。
	if lastProbes <= 0 || lastProbes > 2*accessK {
		t.Fatalf("probes=%d should be bounded by 2*accessed=%d regardless of N=%d",
			lastProbes, 2*accessK, n)
	}
	dump(t, "complexity-accessed-only",
		fmt.Sprintf("N=%d, last hook accesses only %d prior records", n, accessK),
		lastProbes, fmt.Sprintf("1..%d (bounded by 2*%d)", 2*accessK, accessK),
		"开销只与实际访问记录数相关，与该记录之前的总条数无关")
}
