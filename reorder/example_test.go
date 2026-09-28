package reorder_test

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"ontology/reorder"
)

// Example 演示乱序事件的重排：相同时间按到达顺序稳定排序，迟到事件走旁路。
func Example() {
	// 传入任意 *slog.Logger 即可逐条记录输入、判定依据（与水线的比较）与两路输出计数；
	// 此处丢弃以保持示例输出稳定，真实使用可传 os.Stdout 或结构化采集器。
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// 容量 10，允许乱序跨度 5：watermark = 最大事件时间 - 5。
	b, err := reorder.NewBuffer(10, 5, logger)
	if err != nil {
		panic(err)
	}

	for _, e := range []reorder.Event{
		{ID: "A", Time: 10}, // 缓冲，watermark -> 5
		{ID: "B", Time: 12}, // 缓冲，watermark -> 7
		{ID: "C", Time: 6},  // 6 <= 7：迟到，立即进旁路
		{ID: "D", Time: 20}, // watermark -> 15，释放 A、B 到主输出
		{ID: "E", Time: 8},  // 8 <= 15：迟到，进旁路
	} {
		if _, err := b.Accept(e); err != nil {
			fmt.Println("rejected:", reorder.ReasonOf(err))
		}
	}
	b.Flush() // D 仍在缓冲，排空到主输出

	fmt.Print("main:  ")
	fmt.Println(strings.Join(func() []string {
		out := make([]string, len(b.MainOutput()))
		for i, e := range b.MainOutput() {
			out[i] = fmt.Sprintf("%s(t=%d)", e.ID, e.Time)
		}
		return out
	}(), " "))
	fmt.Print("side:  ")
	fmt.Println(strings.Join(func() []string {
		out := make([]string, len(b.SideOutput()))
		for i, e := range b.SideOutput() {
			out[i] = fmt.Sprintf("%s(t=%d)", e.ID, e.Time)
		}
		return out
	}(), " "))
	// Output:
	// main:  A(t=10) B(t=12) D(t=20)
	// side:  C(t=6) E(t=8)
}
