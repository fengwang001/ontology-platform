// Command topndemo 在一组示例撤回式变更上演示 TopNTracker，
// 逐条打印输入、判定依据与输出（离开 / 进入）条目。
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	const n = 3
	const maxLive = 6

	tracker, err := ontology.NewTopNTracker(n, maxLive)
	if err != nil {
		panic(err)
	}

	// 场景：并列排序、高分插入挤掉榜尾、各类非法输入、撤回榜首后由榜外补位。
	changes := []ontology.Change{
		{Kind: ontology.KindAdd, Row: ontology.Row{Key: "a", Score: 100}},
		{Kind: ontology.KindAdd, Row: ontology.Row{Key: "b", Score: 90}},
		{Kind: ontology.KindAdd, Row: ontology.Row{Key: "c", Score: 90}},      // 与 b 并列，键序在后
		{Kind: ontology.KindAdd, Row: ontology.Row{Key: "d", Score: 95}},      // 插入第 2，c 被挤到榜外
		{Kind: ontology.KindAdd, Row: ontology.Row{Key: "a", Score: 1}},       // 键已存在
		{Kind: ontology.KindRetract, Row: ontology.Row{Key: "a", Score: 99}},  // 分数不符
		{Kind: ontology.KindRetract, Row: ontology.Row{Key: "z", Score: 1}},   // 键不存在
		{Kind: ontology.KindRetract, Row: ontology.Row{Key: "a", Score: 100}}, // 撤回榜首，c 补位
		{Kind: ontology.KindAdd, Row: ontology.Row{Key: "", Score: 1}},        // 空键
		{Kind: ontology.KindUnknown, Row: ontology.Row{Key: "x"}},             // 非法种类
	}

	for i, c := range changes {
		res := tracker.Apply(c)
		fmt.Printf("#%d 输入: %s\n", i+1, formatChange(c))
		if !res.Accepted {
			fmt.Printf("    判定: 拒绝 (%s)\n", ontology.ReasonError(res.Reason))
			fmt.Println("    输出: 无（状态与日志不变）")
			continue
		}
		fmt.Println("    判定: 接受")
		if len(res.Left) == 0 && len(res.Entered) == 0 {
			fmt.Println("    输出: 榜单无变化")
		}
		for _, e := range res.Left {
			fmt.Printf("    输出: 离开 rank=%d key=%q score=%d\n", e.Rank, e.Key, e.Score)
		}
		for _, e := range res.Entered {
			fmt.Printf("    输出: 进入 rank=%d key=%q score=%d\n", e.Rank, e.Key, e.Score)
		}
	}

	fmt.Println("\n最终前", n, "名:")
	for _, e := range tracker.TopN() {
		fmt.Printf("  rank=%d key=%q score=%d\n", e.Rank, e.Key, e.Score)
	}
}

// formatChange 将变更格式化为可读的单行描述。
func formatChange(c ontology.Change) string {
	kind := "Unknown"
	switch c.Kind {
	case ontology.KindAdd:
		kind = "Add"
	case ontology.KindRetract:
		kind = "Retract"
	}
	return fmt.Sprintf("%s(key=%q, score=%d)", kind, c.Row.Key, c.Row.Score)
}
