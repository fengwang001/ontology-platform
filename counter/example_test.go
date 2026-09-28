package counter_test

import (
	"fmt"
	"io"

	"ontology/counter"
)

// ExampleCounter 演示累积输出与迟到丢弃：
//   - a@1、a@6 落入大窗口 [0,10)；
//   - a@6 把水位线推到 6，子窗口终点 5 到期，输出累计 1；
//   - a@10（边界事件，归 [10,20)）把水位线推到 10，终点 10 到期，累计 2；
//   - a@4 的最小子窗口终点 5 不超过水位线 10，被丢弃。
func ExampleCounter() {
	c, err := counter.New(counter.Config{WindowSize: 10, Step: 5, MaxWindows: 4},
		counter.WithLogger(io.Discard))
	if err != nil {
		panic(err)
	}

	for _, ts := range []int64{1, 6, 10, 4} {
		out, err := c.Add(counter.Event{Key: "a", Timestamp: ts})
		if err != nil {
			panic(err)
		}
		for _, r := range out {
			fmt.Printf("emit window=[%d,%d) end=%d cumulative=%d\n",
				r.WindowStart, r.WindowStart+10, r.End, r.Count)
		}
	}
	fmt.Println("dropped =", c.Dropped())
	// Output:
	// emit window=[0,10) end=5 cumulative=1
	// emit window=[0,10) end=10 cumulative=2
	// dropped = 1
}
