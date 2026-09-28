// Command server 在撤回式变更流上演示按分组增量维护去重计数：
// 顺序提交多批变更，打印每批的输入条目、判定依据、输出净变化与批后视图。
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	svc := ontology.NewDedupService(100)

	batches := [][]ontology.Change{
		// 同一值多次插入：g1 的 a 多重性升到 2，去重计数只 +1。
		{
			{Group: "g1", Value: "a", Kind: ontology.KindInsert},
			{Group: "g1", Value: "a", Kind: ontology.KindInsert},
			{Group: "g1", Value: "b", Kind: ontology.KindInsert},
		},
		// 撤回一份：多重性 2 -> 1，仍为正，去重计数不变。
		{
			{Group: "g1", Value: "a", Kind: ontology.KindRetract},
		},
		// 批内先增后删（g2 的 x），折叠后 g2 净变化为 0；g1 的 a 1 -> 0，计数 -1。
		{
			{Group: "g2", Value: "x", Kind: ontology.KindInsert},
			{Group: "g2", Value: "x", Kind: ontology.KindRetract},
			{Group: "g1", Value: "a", Kind: ontology.KindRetract},
		},
		// 颠倒顺序：先撤回再插入同一值，批内此前状态为零 -> 整批拒绝。
		{
			{Group: "g1", Value: "c", Kind: ontology.KindRetract},
			{Group: "g1", Value: "c", Kind: ontology.KindInsert},
		},
		// 各类非法输入：空组名、空值、非法符号、撤回不存在的值。
		{{Group: "", Value: "z", Kind: ontology.KindInsert}},
		{{Group: "g1", Value: "", Kind: ontology.KindInsert}},
		{{Group: "g1", Value: "z", Kind: ontology.ChangeKind(0)}},
		{{Group: "g1", Value: "missing", Kind: ontology.KindRetract}},
	}

	for _, batch := range batches {
		res := svc.Apply(batch)
		seq := svc.Journal().Len() - 1
		entry := svc.Journal().Entries()[seq]
		fmt.Println(ontology.FormatLogEntry(entry))
		fmt.Println("---")
		_ = res
	}
}
