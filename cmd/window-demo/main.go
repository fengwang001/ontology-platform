// Command window-demo 用一个固定序列演示翻滚窗口计数器的完整行为：
// 开窗、水位线触发、容限内迟到修正、超容限清除与丢弃。
// 运行：go run ./cmd/window-demo
package main

import (
	"fmt"
	"log/slog"
	"os"

	"ontology/window"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	c, err := window.New(window.Config{
		WindowSize:      10, // 窗口 [n*10, n*10+10)
		WatermarkDelay:  0,  // 水位线 = 最大事件时间
		AllowedLateness: 5,  // 触发后保留到 end+5
		MaxOpenWindows:  100,
		Logger:          log,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "new counter: %v\n", err)
		os.Exit(1)
	}

	// 固定演示序列：乱序到达，含触发、容限内修正与超容限丢弃。
	events := []struct {
		key string
		t   int64
	}{
		{"b", -1},  // 负时间戳：属于 [-10,0)
		{"a", 1},   // [0,10) count 1
		{"a", 9},   // [0,10) count 2
		{"a", 10},  // wm=10：触发 [0,10) count 2；[10,20) count 1
		{"a", 5},   // wm=10 <= 15：容限内迟到，修正 [0,10) count 3
		{"a", 15},  // wm=15：边界，[0,10) 仍保留；[10,20) count 2
		{"a", 4},   // wm=15 == gcEnd=15：边界仍算容限内，修正 count 4
		{"a", 16},  // wm=16 严格越过 15：清除 [0,10)
		{"a", 3},   // 窗口已清除：丢弃
		{"b", 100}, // 推进水位线：触发/清除 b 的 [-10,0)
	}
	for _, e := range events {
		outcome, err := c.Add(e.key, e.t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "add(%q,%d) rejected: %v\n", e.key, e.t, err)
			os.Exit(1)
		}
		fmt.Printf("add(%q,%d) -> outcome=%s\n\n", e.key, e.t, outcomeName(outcome))
	}

	fmt.Println("================ 最终结果 ================")
	for _, r := range c.Results() {
		fmt.Printf("#%d %s window=[%d,%d) count=%d kind=%s\n",
			r.Seq, r.Key, r.WindowStart, r.WindowEnd, r.Count, kindName(r.Kind))
	}
	fmt.Printf("watermark=%d dropped=%d active_windows=%d\n",
		c.Watermark(), c.DroppedEvents(), len(c.ActiveWindows()))
}

func outcomeName(o window.Outcome) string {
	switch o {
	case window.OutcomeAccepted:
		return "accepted"
	case window.OutcomeDropped:
		return "dropped"
	case window.OutcomeRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

func kindName(k window.ResultKind) string {
	switch k {
	case window.ResultTriggered:
		return "triggered"
	case window.ResultCorrected:
		return "corrected"
	default:
		return "unknown"
	}
}
