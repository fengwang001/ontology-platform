// Command server 演示增量分组聚合组件 ontology.Aggregator 的行为：
// 插入、同组更新、改分组键、删除以及非法批次的原子拒绝，
// 并逐条打印净变化日志与当前聚合视图。
package main

import (
	"errors"
	"fmt"

	"ontology/ontology"
)

func main() {
	// maxGroups=0 表示不限制组数。
	agg := ontology.New(0)

	apply := func(title string, ops ...ontology.Op) {
		fmt.Printf("==== %s ====\n", title)
		for _, op := range ops {
			fmt.Printf("  输入 %+v\n", op)
		}
		entries, err := agg.Apply(ops)
		if err != nil {
			var re *ontology.RejectError
			if errors.As(err, &re) {
				fmt.Printf("  整批拒绝 @%d: %v\n", re.Index, errors.Unwrap(err))
			} else {
				fmt.Printf("  整批拒绝: %v\n", err)
			}
			return
		}
		for _, e := range entries {
			kind := "撤回"
			if e.Kind == ontology.EntryAdd {
				kind = "写入"
			}
			fmt.Printf("  日志 seq=%-3d %s 组=%-4q 行=%-3q 值Δ=%+3d 计数Δ=%+2d => 组求和=%+3d 组计数=%d\n",
				e.Seq, kind, e.Group, e.RowID, e.Value, e.Count, e.SumAfter, e.CountAfter)
		}
	}

	// 1) 插入：g1 的和为 0 但计数为 2，组必须保留。
	apply("插入（g1 求和为 0、计数为 2，仍保留）",
		ontology.Op{Kind: ontology.OpInsert, RowID: "a", Group: "g1", Value: 5},
		ontology.Op{Kind: ontology.OpInsert, RowID: "b", Group: "g1", Value: -5},
		ontology.Op{Kind: ontology.OpInsert, RowID: "c", Group: "g2", Value: 7},
	)

	// 2) 同组更新：先撤回旧值，再写入新值。
	apply("同组更新 a: g1 5 -> 3",
		ontology.Op{Kind: ontology.OpUpdate, RowID: "a", Group: "g1", Value: 3},
	)

	// 3) 改分组键：先输出旧组 g1 的撤回，再输出新组 g3 的加入。
	apply("改分组键 a: g1 -> g3",
		ontology.Op{Kind: ontology.OpUpdate, RowID: "a", Group: "g3", Value: 3},
	)

	// 4) 删除：撤回后 g2 计数归零，从视图消失。
	apply("删除 c（g2 变空后消失）",
		ontology.Op{Kind: ontology.OpDelete, RowID: "c"},
	)

	// 5) 非法批次：重复插入，整批原子拒绝，前面即便合法也不落盘。
	apply("非法批次（重复插入 a，整批拒绝）",
		ontology.Op{Kind: ontology.OpInsert, RowID: "z", Group: "g9", Value: 100},
		ontology.Op{Kind: ontology.OpInsert, RowID: "a", Group: "g9", Value: 1},
	)

	fmt.Println("==== 最终聚合视图（不含计数为 0 的组）====")
	for _, v := range agg.Snapshot() {
		fmt.Printf("  组=%-4q 求和=%+3d 计数=%d\n", v.Group, v.Sum, v.Count)
	}
	fmt.Printf("已提交日志总数: %d\n", len(agg.Log()))
}
