package ontology_test

import (
	"fmt"

	"ontology/ontology"
)

// ExampleSystem_demo 演示三类事件的时钟推进、全序与因果/并发判定，
// 并打印每次输入、得到的时间戳以及判定依据。
func ExampleSystem_demo() {
	s, _ := ontology.NewSystem(3, 100)

	// do 以多返回值为唯一实参，成功时取事件、失败即 panic（本示例不期望失败）。
	e1 := do(s.Local(0))
	printEv("本地(节点0)", e1)
	e2 := do(s.Send(0, "m"))
	printEv("发送m(节点0)", e2)
	e3 := do(s.Local(2))
	printEv("本地(节点2)", e3)
	e4 := do(s.Receive(1, "m"))
	printEv("接收m(节点1)", e4)

	// 判定依据：发送事件向量各分量 <= 接收事件向量且不全相等（消息边）。
	r, _ := s.Compare(ref(e2), ref(e4))
	fmt.Printf("发送m 与 接收m 的关系: %s（依据: 发送→接收消息边）\n", r)

	// 判定依据：两个无通信节点的事件向量不可比（各有分量更大），故并发。
	r, _ = s.Compare(ref(e1), ref(e3))
	fmt.Printf("节点0首个事件 与 节点2首个事件 的关系: %s（依据: 向量不可比）\n", r)

	fmt.Print("全序（按 时间戳,节点编号）: ")
	for i, e := range s.TotalOrder() {
		if i > 0 {
			fmt.Print(" < ")
		}
		fmt.Printf("t%d/n%d", e.Clock, e.Node)
	}
	fmt.Println()

	// 非法输入：接收不存在的消息，给出可区分原因，且不改变任何状态。
	if _, err := s.Receive(0, "ghost"); err != nil {
		fmt.Printf("非法接收原因: %s（拒绝后事件总数仍为 %d）\n",
			err.(*ontology.ClockError).Kind, s.EventCount())
	}

	// Output:
	// 本地(节点0) -> 节点0 序号1 时间戳=1 向量=[1 0 0]
	// 发送m(节点0) -> 节点0 序号2 时间戳=2 向量=[2 0 0]
	// 本地(节点2) -> 节点2 序号1 时间戳=1 向量=[0 0 1]
	// 接收m(节点1) -> 节点1 序号1 时间戳=3 向量=[2 1 0]
	// 发送m 与 接收m 的关系: before（依据: 发送→接收消息边）
	// 节点0首个事件 与 节点2首个事件 的关系: concurrent（依据: 向量不可比）
	// 全序（按 时间戳,节点编号）: t1/n0 < t1/n2 < t2/n0 < t3/n1
	// 非法接收原因: message_not_found（拒绝后事件总数仍为 4）
}

func do(ev ontology.Event, err error) ontology.Event {
	if err != nil {
		panic(err)
	}
	return ev
}

func printEv(name string, ev ontology.Event) {
	fmt.Printf("%s -> 节点%d 序号%d 时间戳=%d 向量=%v\n",
		name, ev.Node, ev.Seq, ev.Clock, ev.Vector)
}

func ref(e ontology.Event) ontology.EventRef {
	return ontology.EventRef{Node: e.Node, Seq: e.Seq}
}
