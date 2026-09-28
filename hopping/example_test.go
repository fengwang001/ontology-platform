package hopping_test

import (
	"errors"
	"fmt"

	"ontology/hopping"
)

// 演示负时间戳归属、部分迟到、整条丢弃、有序输出与时钟回退拒绝。
func ExampleCounter() {
	// 窗口长度 10、步长 5：窗口为 [5k, 5k+10)，相邻窗口重叠一半。
	c, err := hopping.New(hopping.Config{
		WindowSize:     10,
		SlideStep:      5,
		MaxOpenWindows: 100,
	})
	if err != nil {
		panic(err)
	}

	_ = c.Add(-7, "a", 1)
	_ = c.Add(-5, "b", 2)

	out, _ := c.Advance(0) // 关闭终点 <= 0 的 [-15,-5) 与 [-10,0)
	for _, r := range out {
		fmt.Printf("%d..%d %s %d\n", r.WindowStart, r.WindowEnd, r.Key, r.Count)
	}

	// t=-1 归属 [-10,0)（已关闭）与 [-5,5)（打开）：部分迟到，只计后者。
	_ = c.Add(-1, "c", 3)
	// t=-10 的两个归属窗口均已关闭：整条丢弃。
	_ = c.Add(-10, "d", 1)

	out, _ = c.Advance(5)
	for _, r := range out {
		fmt.Printf("%d..%d %s %d\n", r.WindowStart, r.WindowEnd, r.Key, r.Count)
	}

	// 时钟只进不退。
	if _, err := c.Advance(4); errors.Is(err, hopping.ErrClockRewind) {
		fmt.Println("rewind rejected")
	}

	st := c.Snapshot()
	fmt.Printf("dropped=%d windows=%d records=%d open=%d\n",
		st.DroppedEvents, st.EmittedWindows, st.EmittedRecords, st.OpenWindows)

	// Output:
	// -15..-5 a 1
	// -10..0 a 1
	// -10..0 b 2
	// -5..5 b 2
	// -5..5 c 3
	// rewind rejected
	// dropped=1 windows=3 records=5 open=0
}
