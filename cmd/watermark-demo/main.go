// watermark-demo 用一个固定的乱序输入序列演示有界事件时间重排缓冲：
// 每个输入的判定依据、释放事件、主输出与旁路输出都会打印到标准错误。
//
// 运行：go run ./cmd/watermark-demo
package main

import (
	"os"

	"ontology/watermark"
)

func main() {
	const capacity = 4
	b, err := watermark.NewBuffer(capacity)
	if err != nil {
		panic(err)
	}
	log := watermark.NewLogger(b, os.Stderr)

	inputs := []watermark.Event{
		{ID: "e1", Time: 3}, // 接受，wm=3，驻留
		{ID: "e2", Time: 7}, // 接受，wm=7，释放 e1
		{ID: "e3", Time: 1}, // 1 < 7：迟到 → 旁路
		{ID: "e4", Time: 7}, // 等于 wm：准时驻留
		{ID: "e5", Time: 2}, // 迟到 → 旁路
		{ID: "e6", Time: 9}, // 接受，wm=9，释放 e2、e4（同时间按序号）
		{ID: "e7", Time: 6}, // 迟到 → 旁路
		{ID: "", Time: 9},   // 拒绝：空标识，状态不变
		{ID: "e4", Time: 0}, // 拒绝：重复标识，状态不变
		{ID: "e8", Time: 9}, // 等于 wm：准时驻留
	}
	for _, e := range inputs {
		log.Offer(e)
	}
	log.Close()
}
