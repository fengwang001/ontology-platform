package clock_test

import (
	"fmt"
	"log"
	"os"

	"ontology/clock"
)

// ExampleSystem 演示三类事件的时间戳推进、全序排列与因果判定，
// 以及日志中打印的输入、时间戳与判定依据。
func ExampleSystem() {
	// 不带时间前缀的日志直接输出到标准输出，便于观察判定依据。
	sys := clock.New(2, 100)
	sys.SetLogger(log.New(os.Stdout, "", 0))

	sys.Local(0)                   // 事件0：节点0时钟 1
	send, _ := sys.Send(0)         // 事件1：节点0时钟 2，登记消息0
	sys.Local(1)                   // 事件2：节点1时钟 1
	sys.Receive(1, send.MessageID) // 事件3：max(1, 消息2)+1 = 3

	for _, e := range sys.TotalOrder() {
		fmt.Printf("order: clock=%d node=%d id=%d type=%s\n", e.Clock, e.Node, e.ID, e.Type)
	}

	o, _ := sys.Compare(0, 3) // 事件0 -> 发送 -> 接收：传递闭包
	fmt.Printf("event0 vs event3: %s\n", o)
	o, _ = sys.Compare(0, 2) // 两个无消息往来的独立本地事件：并发
	fmt.Printf("event0 vs event2: %s\n", o)

	// Output:
	// input=local node=0 | accept id=0 seq=0 timestamp=1 | rule: local clock 0 + 1 = 1
	// input=send node=0 | accept id=1 seq=1 timestamp=2 message=0 | rule: send clock 1 + 1 = 2; message registered
	// input=local node=1 | accept id=2 seq=0 timestamp=1 | rule: local clock 0 + 1 = 1
	// input=receive node=1 message=0 | accept id=3 seq=1 timestamp=3 | rule: max(local=1, message=2)=2 (message) + 1 = 3; causal edge send(event=1,node=0) -> receive(event=3)
	// order: clock=1 node=0 id=0 type=local
	// order: clock=1 node=1 id=2 type=local
	// order: clock=2 node=0 id=1 type=send
	// order: clock=3 node=1 id=3 type=receive
	// compare a=0 b=3 | before | path: event0 -> event1 -> event3
	// event0 vs event3: before
	// compare a=0 b=2 | concurrent | neither reachable via same-node order or send->receive edges
	// event0 vs event2: concurrent
}
