// 命令行演示：构造若干实例与基数约束，执行一批跨实例联合版本前置更新，
// 并打印每个批次的判定记录。
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	s := ontology.NewStore()
	s.SetLinkLimit("member", 2)
	for _, id := range []ontology.InstanceID{"group-1", "user-1", "user-2", "user-3"} {
		s.Create(id)
	}

	batches := []ontology.Batch{
		{ID: "b1-join", Items: []ontology.Item{
			{Instance: "group-1", Expect: 1, AddLinks: []ontology.LinkRef{{Type: "member", Target: "user-1"}}},
			{Instance: "user-1", Expect: 1, SetAttrs: map[string]string{"dept": "eng"}},
		}},
		{ID: "b2-stale", Items: []ontology.Item{
			{Instance: "group-1", Expect: 1}, // 版本已被 b1 推进，前置不满足
			{Instance: "user-2", Expect: 1},
		}},
		{ID: "b3-overflow", Items: []ontology.Item{
			{Instance: "group-1", Expect: 2, AddLinks: []ontology.LinkRef{
				{Type: "member", Target: "user-2"},
				{Type: "member", Target: "user-3"},
			}},
		}},
		{ID: "b4-dup", Items: []ontology.Item{
			{Instance: "user-1", Expect: 2},
			{Instance: "user-1", Expect: 2},
		}},
	}
	for _, b := range batches {
		d := s.ApplyBatch(b)
		fmt.Printf("%-12s -> %-24s %s (读取版本 %v)\n", d.BatchID, d.Outcome, d.Detail, d.Observed)
	}

	fmt.Println("\n最终状态：")
	for _, id := range []ontology.InstanceID{"group-1", "user-1", "user-2", "user-3"} {
		snap, _ := s.SnapshotOf(id)
		fmt.Printf("  %-8s 版本=%d 属性=%v 关联=%v\n", id, snap.Version, snap.Attrs, snap.Links)
	}
}
