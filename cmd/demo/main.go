package main

import (
	"fmt"

	"ontology/rside"
)

// 判定辅助：ok 打印 OK，失败打印 FAIL。
func report(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func rsideOK() bool {
	r := rside.New()
	r.Subscribe("B", "k2", 7) // 右表无 B：立即入一条 nil 响应
	if r.Pending() != 1 || r.QueueLen("B") != 1 {
		return false
	}
	resp, ok := r.Pop("B")
	if !ok || resp.K != "k2" || resp.Hash != 7 || resp.RVal != nil {
		return false
	}
	r.Unsubscribe("B", "k2")
	r.ChangeRight("B", strptr("b2"))  // 无订阅者：零条响应
	r.Subscribe("B", "k1", 3)         // 立即入一条 b2
	r.Subscribe("B", "k3", 9)         // 立即入一条 b2
	if r.Pending() != 2 {
		return false
	}
	r.Pop("B")
	r.Pop("B")
	n := r.ChangeRight("B", strptr("b3")) // k1,k3 按字典序各一条
	if n != 2 || r.Pending() != 2 {
		return false
	}
	first, _ := r.Pop("B")
	second, _ := r.Pop("B")
	return first.K == "k1" && first.RVal != nil && *first.RVal == "b3" &&
		second.K == "k3" && r.QueueLen("B") == 0
}

func strptr(s string) *string { return &s }

func main() {
	report("rside FIFO/订阅/字典序", rsideOK())
}
