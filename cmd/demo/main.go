// 演示：粘滞会话单调读的各项判定，全部 OK 且退出码 0 为通过。
package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var bad bool

func ok(cond bool, line string) {
	if !cond {
		bad = true
		fmt.Println("FAIL", line)
		return
	}
	fmt.Println("OK", line)
}

// scenario 建立第三节状态（R0/R1/R2 applied=5/3/2，k=E/C/B）与会话（粘 R0）。
func scenario() (*api.API, *api.Session) {
	a := api.New()
	a.Write("k", "A")
	a.Write("k", "B")
	a.Offline(2)
	a.Write("k", "C")
	a.Offline(1)
	a.Write("k", "D")
	a.Write("k", "E")
	a.Online(1)
	a.Online(2)
	s, _ := a.Open(0)
	return a, s
}

func main() {
	a, s := scenario() // 1. 第三节五行操作
	v1, l1, _ := a.Read(s, "k")
	e1 := a.SwitchReplica(s, 1)
	v2, l2, _ := a.Read(s, "k")
	e2 := a.SwitchReplica(s, 2)
	v3, l3, _ := a.Read(s, "k")
	ok(e1 == nil && e2 == nil && v1 == "E" && l1 == 5 && v2 == "E" && l2 == 5 && v3 == "E" && l3 == 5,
		fmt.Sprintf("五步: seen 0→%d→%d→%d, R1补{4,5}, R2补{3,4,5}, Read(%s,%d)×3", l1, l2, l3, v3, l3))

	b := api.New() // 2. View 与批量朴素参照一致
	want := map[string]string{}
	for _, kv := range []string{"x:1", "y:2", "x:3", "z:4", "y:5"} {
		b.Write(kv[:1], kv[2:])
		want[kv[:1]] = kv[2:]
	}
	ok(maps.Equal(b.View(), want), "View 与朴素参照一致")

	c, cs := scenario() // 3. 三类可判定错误互不相同
	_, we := c.Write("", "v")
	ie := c.Offline(9)
	c.Offline(1)
	se := c.SwitchReplica(cs, 1)
	c.Online(1)
	ok(errors.Is(we, api.ErrEmptyKey) && errors.Is(ie, api.ErrBadIndex) && errors.Is(se, api.ErrOffline) &&
		!errors.Is(api.ErrEmptyKey, api.ErrBadIndex) && !errors.Is(api.ErrBadIndex, api.ErrOffline),
		"三类错误可判定且互异")

	before := c.View() // 4. 被拒后状态、日志、会话不变
	c.Write("", "x")
	c.Offline(9)
	c.Offline(1)
	c.SwitchReplica(cs, 1)
	c.Online(1)
	v4, l4, _ := c.Read(cs, "k")
	ok(maps.Equal(c.View(), before) && v4 == "E" && l4 == 5, "拒绝后状态/日志/会话不变")

	scale := true // 5. 大 m 下切换补全正确；检查个数不随 m 增长的断言在 rep 包内测试
	for _, m := range []int{100, 1000, 10000} {
		d := api.New()
		for i := 1; i < m; i++ {
			d.Write("k", "v")
		}
		d.Offline(1)
		d.Write("k", "last")
		d.Online(1)
		ds, _ := d.Open(0)
		d.Read(ds, "k")
		if d.SwitchReplica(ds, 1) != nil {
			scale = false
			continue
		}
		v, l, _ := d.Read(ds, "k")
		scale = scale && v == "last" && l == m
	}
	ok(scale, "大 m(100/1000/10000) 切换补全正确，检查个数恒定(见 rep 测试)")

	e, es := scenario() // 6. 并发只读同一副本上的同一会话，结果逐字段相同
	e.Read(es, "k")
	var same atomic.Bool
	same.Store(true)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				if v, l, _ := e.Read(es, "k"); v != "E" || l != 5 {
					same.Store(false)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	ok(same.Load(), "8 goroutine 并发只读结果一致")

	ok(api.New().SelfCheck() == nil, "SelfCheck 通过") // 7. 内置自检
	if bad {
		os.Exit(1)
	}
}
