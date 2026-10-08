// cmd/server 演示批量导入子系统：构造一个小型本体，
// 分别用 BestEffort 与 Atomic 模式执行导入并打印逐条目报告。
package main

import (
	"fmt"

	"ontology/ontology"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func printReport(title string, rep ontology.Report) {
	fmt.Println("== " + title + " ==")
	if rep.Rejected {
		fmt.Println("  请求被整体拒绝:", rep.RejectReason)
		return
	}
	for _, r := range rep.Results {
		fmt.Printf("  条目 %-4s 判定 %-20s 依据 %s\n", r.EntryID, r.Verdict, r.Reason)
	}
	if rep.RolledBack {
		fmt.Println("  已整体回滚")
	}
	for _, f := range rep.RollbackFailures {
		fmt.Printf("  回滚失败: 条目 %s 实例 %s 错误 %s\n", f.EntryID, f.RID, f.Err)
	}
}

func main() {
	store := ontology.NewStore()
	must(store.RegisterObjectType(ontology.ObjectType{
		RID: "Person",
		Properties: []ontology.PropertySpec{
			{Name: "name", Type: ontology.PropString, Required: true},
			{Name: "mentor", Type: ontology.PropObjectRef, RefObjectType: "Person"},
		},
	}))
	must(store.RegisterLinkType(ontology.LinkType{
		RID: "Manages", SourceObjectType: "Person", TargetObjectType: "Person",
		MaxTargetsPerSource: 1,
	}))

	logger := func(rec ontology.LogRecord) {
		fmt.Printf("  [日志] 条目=%s 判定=%s 依据=%s\n", rec.Entry.ID, rec.Result.Verdict, rec.Result.Reason)
	}
	imp := ontology.NewImporter(store, logger)

	fmt.Println("-- BestEffort：部分失败不影响其余条目 --")
	rep1 := imp.Import(ontology.Request{
		Mode: ontology.BestEffort,
		Entries: []ontology.Entry{
			{ID: "e1", Kind: ontology.EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "Alice"}},
			{ID: "e2", Kind: ontology.EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "Bob", "mentor": ontology.EntryRef("e1")}},
			{ID: "e3", Kind: ontology.EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{}}, // 缺少必填属性 name
			{ID: "e4", Kind: ontology.EntryLink, LinkTypeRID: "Manages",
				Source: ontology.EntryRef("e1"), Target: ontology.EntryRef("e2")},
			{ID: "e5", Kind: ontology.EntryLink, LinkTypeRID: "Manages",
				Source: ontology.EntryRef("e1"), Target: ontology.EntryRef("e3")},
		},
	})
	printReport("BestEffort 报告", rep1)

	fmt.Println()
	fmt.Println("-- Atomic：任一失败触发整体回滚 --")
	rep2 := imp.Import(ontology.Request{
		Mode: ontology.Atomic,
		Entries: []ontology.Entry{
			{ID: "a1", Kind: ontology.EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "Carol"}},
			{ID: "a2", Kind: ontology.EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": 42}}, // 类型错误
		},
	})
	printReport("Atomic 报告", rep2)

	fmt.Println()
	fmt.Printf("最终图状态: 对象 %d 个, 链接 %d 条\n", store.ObjectCount(), store.LinkCountAll())
}
