package batchimport

import (
	"fmt"
	"testing"
)

// TestVisibilityCostIndependentOfBatchLength 可验证地证明：
// 单条记录解析批内可见范围的开销（实际命中覆盖层的步数）只与该记录之前
// 实际被访问到的记录数相关，不随批次总长度增长。
//
// 证明方式：构造总长度 N 从 20 翻到 5120 的批次，令最后一条记录的前置
// 钩子只通过 Get 访问固定 3 个已暂存主键。报告中的 VisibilitySteps
// 必须恒等于 3，与 N 无关；同时底层 Get 是 O(1) 哈希探测（见 view.get）。
func TestVisibilityCostIndependentOfBatchLength(t *testing.T) {
	for _, n := range []int{20, 160, 1280, 5120} {
		r := NewRegistry()
		ot := &ObjectType{
			Name:       typeWidget,
			FieldTypes: map[string]string{"amount": "int64", "label": "string"},
		}
		// 普通记录不读视图；只有末尾探针记录固定访问前 3 条主键。
		ot.PreHooks = append(ot.PreHooks, func(ctx *HookContext, rec Record) error {
			if rec.ID != "probe" {
				return nil
			}
			for _, id := range []string{"w0", "w1", "w2"} {
				if _, ok := ctx.Get(typeWidget, id); !ok {
					return preHookFailure(ctx.Index(), "probe missing "+id)
				}
			}
			return nil
		})
		r.RegisterObjectType(ot)

		records := make([]Record, 0, n)
		for i := 0; i < n-1; i++ {
			records = append(records, Record{
				Type: typeWidget, ID: fmt.Sprintf("w%d", i),
				Fields: mustWidget(1, "x"),
			})
		}
		records = append(records, Record{
			Type: typeWidget, ID: "probe",
			Fields: mustWidget(1, "probe"),
		})

		rep := r.Batch(records, AllOrNothing, 4)
		if !rep.Committed {
			t.Fatalf("N=%d 依据: 探针批次应全部成功, got %v", n, rep.Err)
		}
		probe := rep.Records[n-1]
		ordinary := rep.Records[n/2]
		fmt.Printf("COST-PROOF batchLength=%d probeVisibilitySteps=%d middleRecordSteps=%d 依据: 步数须与 N 无关\n",
			n, probe.VisibilitySteps, ordinary.VisibilitySteps)
		if probe.VisibilitySteps != 3 {
			t.Fatalf("N=%d 依据: 探针固定访问 3 个已暂存记录, 步数必须恒为 3, got %d",
				n, probe.VisibilitySteps)
		}
		if ordinary.VisibilitySteps != 0 {
			t.Fatalf("N=%d 依据: 不访问视图的记录步数必须恒为 0, got %d",
				n, ordinary.VisibilitySteps)
		}
	}
}

// TestVisibilityStepsScaleOnlyWithAccessedRecords 补充证明正向方向：
// 访问的已暂存记录数翻倍时，步数才随之翻倍——步数度量确实在刻画访问数。
func TestVisibilityStepsScaleOnlyWithAccessedRecords(t *testing.T) {
	for _, k := range []int{1, 4, 16} {
		r := NewRegistry()
		ot := &ObjectType{
			Name:       typeWidget,
			FieldTypes: map[string]string{"amount": "int64", "label": "string"},
		}
		ot.PreHooks = append(ot.PreHooks, func(ctx *HookContext, rec Record) error {
			if rec.ID != "probe" {
				return nil
			}
			for j := 0; j < k; j++ {
				if _, ok := ctx.Get(typeWidget, fmt.Sprintf("w%d", j)); !ok {
					return preHookFailure(ctx.Index(), "missing")
				}
			}
			return nil
		})
		r.RegisterObjectType(ot)

		const n = 400 // 批次总长度固定且远大于 k
		records := make([]Record, 0, n)
		for i := 0; i < n-1; i++ {
			records = append(records, Record{
				Type: typeWidget, ID: fmt.Sprintf("w%d", i), Fields: mustWidget(1, "x"),
			})
		}
		records = append(records, Record{
			Type: typeWidget, ID: "probe", Fields: mustWidget(1, "p"),
		})
		rep := r.Batch(records, AllOrNothing, 1)
		if !rep.Committed {
			t.Fatalf("k=%d 批次应成功: %v", k, rep.Err)
		}
		got := rep.Records[n-1].VisibilitySteps
		fmt.Printf("COST-PROOF batchLength=%d accessed=%d steps=%d 依据: 步数 = 实际访问数\n",
			n, k, got)
		if got != k {
			t.Fatalf("依据: 访问 %d 条已暂存记录时步数必须为 %d（与 N=%d 无关）, got %d",
				k, k, n, got)
		}
	}
}
