package window_test

import (
	"fmt"

	"ontology/window"
)

// 端到端小例子：触发、容限内修正、超出容限丢弃。
func ExampleCounter() {
	// 窗口大小 10、无水位线延迟、迟到容限 5。
	c, err := window.New(window.Config{Size: 10, AllowedLateness: 5})
	if err != nil {
		panic(err)
	}

	// ts=0 缓冲；ts=10 把水位线推到 10，[0,10) 触发（gc-time=15，暂不清除）。
	out, _ := c.Process([]window.Event{{Key: "k", Timestamp: 0}, {Key: "k", Timestamp: 10}})
	for _, em := range out {
		fmt.Printf("%s [%d,%d) count=%d\n", em.Kind, em.Result.Start, em.Result.End, em.Result.Count)
	}

	// 水位线 10 < gc-time 15：迟到事件被接受，已输出值修正为 2。
	out, _ = c.Process([]window.Event{{Key: "k", Timestamp: 1}})
	for _, em := range out {
		fmt.Printf("%s [%d,%d) count=%d\n", em.Kind, em.Result.Start, em.Result.End, em.Result.Count)
	}

	// ts=15 使水位线恰好到达 gc-time 15，[0,10) 被清除；
	// 之后 ts=1 落在边界之外（wm >= gc-time），丢弃。
	_, _ = c.Process([]window.Event{{Key: "k", Timestamp: 15}})
	_, _ = c.Process([]window.Event{{Key: "k", Timestamp: 1}})
	fmt.Printf("dropped=%d\n", c.Snapshot().Dropped)

	// Output:
	// fired [0,10) count=1
	// corrected [0,10) count=2
	// dropped=1
}
