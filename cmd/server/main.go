// 演示会话窗口切分器：乱序摄入事件并打印切分结果与自检结论。
package main

import (
	"fmt"
	"log"

	"ontology/session"
)

func main() {
	p, err := session.NewPartitioner(10, 100)
	if err != nil {
		log.Fatalf("创建切分器失败: %v", err)
	}

	// 乱序到达的事件流：中间事件会把两侧会话桥接合并。
	events := []session.Event{
		{Key: "user-a", Time: 30},
		{Key: "user-a", Time: 0},
		{Key: "user-b", Time: 100},
		{Key: "user-a", Time: 10},
		{Key: "user-a", Time: 20},
		{Key: "user-b", Time: 100},
		{Key: "user-b", Time: 111},
	}
	if err := p.Add(events...); err != nil {
		log.Fatalf("摄入失败: %v", err)
	}

	for _, key := range p.Keys() {
		for _, s := range p.Sessions(key) {
			fmt.Printf("key=%s 会话=[%d,%d] 事件数=%d\n", s.Key, s.Start, s.End, s.Count)
		}
	}
	if err := p.Check(); err != nil {
		log.Fatalf("自检失败: %v", err)
	}
	fmt.Println("自检通过")
}
