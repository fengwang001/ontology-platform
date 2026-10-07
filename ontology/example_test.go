package ontology_test

import (
	"fmt"

	"ontology/ontology"
)

// Example 演示一次端到端使用：声明带两侧基数的链接类型、登记实例、
// 单条创建/删除，以及两种批量导入语义的差异。
func Example() {
	l := ontology.NewLedger()
	_ = l.RegisterLinkType(ontology.LinkTypeSpec{
		Name:        "authored",
		SourceType:  "Person",
		TargetType:  "Article",
		SourceBound: ontology.AtMost(2), // 一位作者至多两篇
		TargetBound: ontology.ExactlyOne(),
	})
	_ = l.CreateObject("Person", "alice")
	_ = l.CreateObject("Article", "a1")
	_ = l.CreateObject("Article", "a2")
	_ = l.CreateObject("Article", "a3")

	if err := l.CreateLink("authored", "alice", "a1"); err != nil {
		fmt.Println("create failed:", err)
	}

	imp := ontology.NewImporter(l)
	pairs := []ontology.InstancePair{
		{Source: "alice", Target: "a2"}, // 第 2 篇：恰好等于上限
		{Source: "alice", Target: "a3"}, // 批内累积后起点超限（超出一个）
	}

	report := imp.Import("authored", pairs, ontology.ModeBestEffort)
	for _, r := range report.Results {
		if r.Err != nil {
			fmt.Printf("index %d: %s (%s)\n", r.Index, r.Status, r.Err.Code)
		} else {
			fmt.Printf("index %d: %s\n", r.Index, r.Status)
		}
	}
	// Output:
	// index 0: accepted
	// index 1: rejected (source_cardinality_exceeded)
}
