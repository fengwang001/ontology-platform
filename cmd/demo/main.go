// Command demo 脚本化演示 incjoin：两表内连接的增量差分维护。
//
// 运行：go run ./cmd/demo
// 日志会打印每批的输入、输出差分、提交/拒绝的判定依据。
package main

import (
	"fmt"
	"log"
	"os"

	"ontology/incjoin"
)

type stdLogger struct{ l *log.Logger }

func (s stdLogger) Logf(format string, args ...any) { s.l.Printf(format, args...) }

func main() {
	logger := log.New(os.Stdout, "  ", log.LstdFlags|log.Lmicroseconds)
	j := incjoin.NewJoiner(incjoin.Options{
		MaxResultTuples: 100,
		Logger:          stdLogger{logger},
	})

	// apply 打印批次标题、应用并显式报告结果。
	step := 0
	apply := func(title string, ch incjoin.Change) {
		step++
		fmt.Printf("\n=== 步骤 %d: %s ===\n", step, title)
		diff, err := j.Apply(ch)
		if err != nil {
			fmt.Printf("结果: 拒绝 -> %v\n", err)
			return
		}
		fmt.Printf("结果: 提交, 输出差分 %d 条:\n", len(diff))
		for _, e := range diff {
			fmt.Printf("    %s\n", e)
		}
	}

	// 1) 同时向两张表插入：多重集 L: a×2,b×1；R: x×3。
	apply("同时插入两张表", incjoin.Change{
		Left:  []incjoin.Row{{Key: "k1", Value: "a", Mult: 2}, {Key: "k1", Value: "b", Mult: 1}},
		Right: []incjoin.Row{{Key: "k1", Value: "x", Mult: 3}},
	})

	// 2) 只更新左侧：a 2->4，差分 = (4-2)*3 = +6。
	apply("左行 a 重数 2->4", incjoin.Change{
		Left: []incjoin.Row{{Key: "k1", Value: "a", Mult: 2}},
	})

	// 3) 右侧删除部分：x 3->1，差分 = 4*(1-3) + 1*(1-3) = -10。
	apply("右行 x 重数 3->1", incjoin.Change{
		Right: []incjoin.Row{{Key: "k1", Value: "x", Mult: -2}},
	})

	// 4) 非法：删除量超过现存（x 现存 1，删 5），整批拒绝。
	apply("非法删除（批后负重数）", incjoin.Change{
		Right: []incjoin.Row{{Key: "k1", Value: "x", Mult: -5}},
	})

	// 5) 非法：变更符号为 0。
	apply("非法符号（mult=0）", incjoin.Change{
		Left: []incjoin.Row{{Key: "k1", Value: "a", Mult: 0}},
	})

	// 6) 非法：空值。
	apply("非法空值", incjoin.Change{
		Left: []incjoin.Row{{Key: "k2", Value: "", Mult: 1}},
	})

	// 7) 超限：插入会使结果总元组数超过上限 100。
	apply("结果元组数超限", incjoin.Change{
		Right: []incjoin.Row{{Key: "k1", Value: "HUGE", Mult: 100}},
	})

	// 最终全量视图：被拒绝的批均未生效，结果只反映步骤 1-3。
	fmt.Println("\n=== 最终全量连接视图（与从基表全量重算一致）===")
	for _, e := range j.Snapshot() {
		fmt.Printf("    %s\n", e)
	}
	fmt.Println("=== 左表 ===")
	for _, r := range j.LeftSnapshot() {
		fmt.Printf("    %s\n", r)
	}
	fmt.Println("=== 右表 ===")
	for _, r := range j.RightSnapshot() {
		fmt.Printf("    %s\n", r)
	}
}
