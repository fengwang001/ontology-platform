// 命令 vcdemo 演示因果交付缓冲在乱序、级联、重复、非法输入、缓冲满等
// 场景下的输入、判定依据与交付结果。
//
// 运行：go run ./cmd/vcdemo
package main

import (
	"fmt"
	"log"
	"os"

	"ontology/causalbuf"
)

func main() {
	logger := log.New(os.Stdout, "", 0)

	fmt.Println("==== 场景 1：乱序到达 + 级联交付 ====")
	// B1 先到立即交付；A2 等待 A1；A1 到达后 A2 被级联交付；
	// 之后 B2 携带对 A1 的因果依赖到达，条件满足立即交付。
	scenario(logger, 3, 10,
		causalbuf.Message{Sender: 1, Vector: []int{0, 1, 0}, Payload: "B1"},
		causalbuf.Message{Sender: 0, Vector: []int{2, 1, 0}, Payload: "A2"},
		causalbuf.Message{Sender: 0, Vector: []int{1, 0, 0}, Payload: "A1"},
		causalbuf.Message{Sender: 1, Vector: []int{1, 2, 0}, Payload: "B2"},
	)

	fmt.Println("==== 场景 2：重复消息丢弃并计数（不是错误）====")
	scenario(logger, 2, 10,
		causalbuf.Message{Sender: 0, Vector: []int{1, 0}, Payload: "A1"},
		causalbuf.Message{Sender: 0, Vector: []int{1, 0}, Payload: "A1-again"},
		causalbuf.Message{Sender: 0, Vector: []int{3, 0}, Payload: "A3"},
		causalbuf.Message{Sender: 0, Vector: []int{3, 0}, Payload: "A3-buffered-again"},
	)

	fmt.Println("==== 场景 3：各类非法输入（错误原因可区分，状态不变）====")
	scenario(logger, 2, 10,
		causalbuf.Message{Sender: 7, Vector: []int{1, 0}, Payload: "bad-sender"},
		causalbuf.Message{Sender: 0, Vector: []int{1}, Payload: "bad-vector-len"},
		causalbuf.Message{Sender: 0, Vector: []int{1, -1}, Payload: "bad-vector-neg"},
		causalbuf.Message{Sender: 0, Vector: []int{1, 0}, Payload: "A1-valid"},
	)

	fmt.Println("==== 场景 4：缓冲满拒绝，缺口补齐后照常级联 ====")
	scenario(logger, 2, 1,
		causalbuf.Message{Sender: 0, Vector: []int{2, 0}, Payload: "A2-takes-only-slot"},
		causalbuf.Message{Sender: 1, Vector: []int{0, 2}, Payload: "B2-rejected-full"},
		causalbuf.Message{Sender: 0, Vector: []int{1, 0}, Payload: "A1-unblocks"},
	)
}

func scenario(logger *log.Logger, peers, capacity int, msgs ...causalbuf.Message) {
	b, err := causalbuf.New(peers, capacity, causalbuf.WithLogger(logger))
	if err != nil {
		logger.Printf("New(%d,%d) rejected: %v", peers, capacity, err)
		return
	}
	for _, m := range msgs {
		r, err := b.Receive(m)
		switch {
		case err != nil:
			fmt.Printf("    -> 拒绝：%v\n", err)
		case r.Outcome == causalbuf.OutcomeDuplicate:
			fmt.Printf("    -> 重复丢弃，dupCount=%d\n", r.DupCount)
		case r.Outcome == causalbuf.OutcomeBuffered:
			fmt.Printf("    -> 已缓冲，buffered=%d\n", b.BufferedCount())
		default:
			fmt.Printf("    -> 本次交付 %v，本地向量=%v\n", brief(r.Delivered), b.Vector())
		}
	}
	fmt.Printf("    == 最终交付序列 %v，本地向量=%v，重复计数=%d\n\n",
		brief(b.Delivered()), b.Vector(), b.DupCount())
}

func brief(ms []causalbuf.Message) [][2]int {
	out := make([][2]int, 0, len(ms))
	for _, m := range ms {
		out = append(out, [2]int{m.Sender, m.Vector[m.Sender]})
	}
	return out
}
