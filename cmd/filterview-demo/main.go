// 行级过滤视图增量维护演示。
// 运行：go run ./cmd/filterview-demo
package main

import (
	"fmt"
	"strings"

	"ontology/filterview"
)

func printRows(title string, rows []filterview.Row) {
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s=%d", r.Key, r.Value))
	}
	fmt.Printf("%s: [%s]\n", title, strings.Join(parts, ", "))
}

func apply(v *filterview.View, ops []filterview.Op) {
	journal, err := v.Apply(ops)
	if err != nil {
		fmt.Printf(">>> 批次被拒绝: %v（源表/视图/日志保持不变）\n\n", err)
		return
	}
	for i, e := range journal {
		switch e.Op.Kind {
		case filterview.Insert:
			fmt.Printf("输入[%d] INSERT key=%s value=%d\n", i, e.Op.New.Key, e.Op.New.Value)
		case filterview.Delete:
			fmt.Printf("输入[%d] DELETE key=%s value=%d\n", i, e.Op.Old.Key, e.Op.Old.Value)
		case filterview.Update:
			fmt.Printf("输入[%d] UPDATE key=%s %d -> %d\n", i, e.Op.Old.Key, e.Op.Old.Value, e.Op.New.Value)
		}
		fmt.Printf("  判定依据: %s\n", e.Basis)
		if len(e.Changes) == 0 {
			fmt.Println("  净变化: <无>")
		}
		for _, c := range e.Changes {
			fmt.Printf("  净变化: #%d %s key=%s value=%d\n", c.Seq, c.Kind, c.Row.Key, c.Row.Value)
		}
	}
	printRows("当前视图", v.Snapshot())
	fmt.Println()
}

func main() {
	v, err := filterview.New(10, 20)
	if err != nil {
		panic(err)
	}
	fmt.Printf("过滤区间: [%d, %d)（左闭右开）\n\n", v.Low(), v.High())

	// 插入：左端点 10 满足，右端点 20 不满足。
	apply(v, []filterview.Op{
		{Kind: filterview.Insert, New: filterview.Row{Key: "a", Value: 10}},
		{Kind: filterview.Insert, New: filterview.Row{Key: "b", Value: 20}},
		{Kind: filterview.Insert, New: filterview.Row{Key: "c", Value: 15}},
		{Kind: filterview.Insert, New: filterview.Row{Key: "d", Value: 5}},
	})

	// 更新四情形。
	apply(v, []filterview.Op{
		{Kind: filterview.Update, Old: filterview.Row{Key: "a", Value: 10}, New: filterview.Row{Key: "a", Value: 10}},
		{Kind: filterview.Update, Old: filterview.Row{Key: "c", Value: 15}, New: filterview.Row{Key: "c", Value: 16}},
		{Kind: filterview.Update, Old: filterview.Row{Key: "a", Value: 10}, New: filterview.Row{Key: "a", Value: 20}},
		{Kind: filterview.Update, Old: filterview.Row{Key: "d", Value: 5}, New: filterview.Row{Key: "d", Value: 19}},
	})

	// 删除：视图内撤回，视图外无输出。
	apply(v, []filterview.Op{
		{Kind: filterview.Delete, Old: filterview.Row{Key: "c", Value: 16}},
		{Kind: filterview.Delete, Old: filterview.Row{Key: "b", Value: 20}},
	})

	// 非法批次：前像不符，整批拒绝。
	apply(v, []filterview.Op{
		{Kind: filterview.Insert, New: filterview.Row{Key: "x", Value: 11}},
		{Kind: filterview.Update, Old: filterview.Row{Key: "d", Value: 999}, New: filterview.Row{Key: "d", Value: 12}},
	})

	fmt.Println("最终净变化日志（下游按序应用即得正确视图）:")
	for _, c := range v.Log() {
		fmt.Printf("  #%d %s key=%s value=%d  // %s\n", c.Seq, c.Kind, c.Row.Key, c.Row.Value, c.Basis)
	}
}
