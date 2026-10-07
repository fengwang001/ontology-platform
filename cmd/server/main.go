// 本体平台并发写冲突判定演示程序。
// 演示：混合写入的原子拒绝、等价写、可合并属性的顺序无关合并，
// 以及判定日志的独立重放核验。
package main

import (
	"fmt"
	"log"

	"ontology/ontology"
)

func main() {
	typ := &ontology.ObjectType{
		Name: "ticket",
		Properties: []ontology.PropertySpec{
			{Name: "status", Mergeable: false},
			{Name: "assignee", Mergeable: false},
			{Name: "priority", Mergeable: true, MergeRule: ontology.RuleMaxInt},
			{Name: "tags", Mergeable: true, MergeRule: ontology.RuleSetUnion},
			{Name: "summary", Mergeable: true, MergeRule: ontology.RuleLWW},
		},
	}
	initial := map[string]ontology.Value{
		"status":   "open",
		"assignee": "nobody",
		"priority": int64(0),
		"tags":     []string{},
		"summary":  ontology.LWWValue{},
	}

	store := ontology.NewStore()
	if err := store.CreateInstance("T1", typ, initial); err != nil {
		log.Fatal(err)
	}

	apply := func(req ontology.WriteRequest) {
		res, err := store.Apply(req)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%-4s base=%d -> %-24s version=%d conflict=%s\n",
			req.RequestID, req.BaseVersion, res.Outcome, res.Version, res.ConflictProperty)
	}

	fmt.Println("== 并发写入判定演示 ==")
	apply(ontology.WriteRequest{RequestID: "w1", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]ontology.Value{"status": "closed", "priority": int64(3)}})
	// 与 w1 并发（基线同为 0）：status 冲突 → 整条拒绝，priority 不会单独生效。
	apply(ontology.WriteRequest{RequestID: "w2", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]ontology.Value{"status": "done", "priority": int64(9)}})
	// 等价写：与 w1 声明相同的新值 → 成功且为 no-op。
	apply(ontology.WriteRequest{RequestID: "w3", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]ontology.Value{"status": "closed"}})
	// 纯可合并写入：自动合并。
	apply(ontology.WriteRequest{RequestID: "w4", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]ontology.Value{
			"priority": int64(7), "tags": []string{"urgent"},
			"summary": ontology.LWWValue{Clock: 5, Writer: "bob", Data: "hello"},
		}})
	apply(ontology.WriteRequest{RequestID: "w5", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]ontology.Value{
			"priority": int64(5), "tags": []string{"backend"},
			"summary": ontology.LWWValue{Clock: 2, Writer: "amy", Data: "stale"},
		}})

	version, values, _ := store.Snapshot("T1")
	fmt.Printf("\n最终版本=%d 状态=%v\n", version, values)

	if err := ontology.Verify(typ, initial, 0, store.Log().Entries()); err != nil {
		log.Fatalf("日志重放核验失败: %v", err)
	}
	fmt.Printf("日志重放核验通过（%d 条记录），历史扫描次数=%d\n",
		len(store.Log().Entries()), store.HistoryScans())
}
