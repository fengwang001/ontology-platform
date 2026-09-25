// 双时间戳版本历史演示：逐条打印 OK/FAIL，全部通过退出码 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"

	"ontology/api"
	"ontology/ver"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	s := "OK "
	if !ok {
		s = "FAIL"
	}
	fmt.Printf("%s %s\n", s, name)
}

func main() {
	y, _ := api.New(10)
	type rec struct {
		v      string
		ev, in int64
	}
	seq := []rec{{"A", 15, 1}, {"B", 25, 2}, {"C", 5, 3}, {"D", 20, 4}}
	wantLE := []string{"A", "B", "B", "B"}
	steps, okSteps := "", true
	for i, r := range seq {
		okSteps = y.Apply("K", r.v, r.ev, r.in) == nil && okSteps
		le, _ := y.LatestEvent("K")
		li, _ := y.LatestIngest("K")
		steps += fmt.Sprintf(" %d:%s/%s", i+1, le.Value, li.Value)
		okSteps = okSteps && le.Value == wantLE[i] && li.Value == r.v
	}
	check("分步 LE/LI:"+steps, okSteps)
	le, _ := y.LatestEvent("K")
	li, _ := y.LatestIngest("K")
	check("最终 LatestEvent=B@25 LatestIngest=D@In4", le.Value == "B" && le.Ev == 25 && li.Value == "D" && li.In == 4)
	// (甲) 误把摄取最新当事件最新 → D；(乙) AtEvent 用 Ev<T → A；(丙) 同 Ev 取先到 → X
	wJia := li.Value
	wYi, wYiEv := "", int64(-1)<<62
	for _, r := range seq {
		if r.ev < 20 && r.ev > wYiEv {
			wYi, wYiEv = r.v, r.ev
		}
	}
	y2, _ := api.New(10)
	y2.Apply("K2", "X", 10, 1)
	y2.Apply("K2", "Y", 10, 2)
	le2, _ := y2.LatestEvent("K2")
	check("(甲)错值=D (乙)错值=A (丙)错值=X; 正确 B/D/Y", wJia == "D" && wYi == "A" && le2.Value == "Y")
	check("与朴素扫描一致 + Ev最新单调 + In严格递增", api.SelfCheck() == nil)
	// 四类可判定错误，互不相同
	_, e1 := api.New(0)
	e2 := y.Apply("", "x", 1, 99)
	e3 := y.Apply("K", "x", 1, 2)
	for i := 5; i <= 10; i++ {
		y.Apply("K", "f", int64(i), int64(i))
	}
	e4 := y.Apply("K", "x", 1, 11)
	distinct := !errors.Is(e1, e2) && !errors.Is(e1, e3) && !errors.Is(e1, e4) &&
		!errors.Is(e2, e3) && !errors.Is(e2, e4) && !errors.Is(e3, e4)
	check("四类错误可判定且互不相同", errors.Is(e1, api.ErrBadConfig) && errors.Is(e2, api.ErrEmptyKey) &&
		errors.Is(e3, api.ErrNonMonotonicIngest) && errors.Is(e4, api.ErrCapacity) && distinct)
	b4, _ := y.LatestEvent("K")
	y.Apply("", "x", 1, 99)
	y.Apply("K", "x", 1, 2)
	y.Apply("K", "x", 1, 11)
	af, _ := y.LatestEvent("K")
	check("被拒后状态不变", b4 == af)
	check("大m下 LatestEvent 检查数不随 m 增长", ver.SelfCheck() == nil)
	// 并发：N 个不同 key 各写 1 版，M 个 goroutine 对同一 key 写严格递增 In（冲突重试），并发只读
	yc, _ := api.New(64)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); yc.Apply(fmt.Sprintf("k%d", g), "v", 1, 1) }(g)
	}
	for j := 1; j <= 16; j++ {
		wg.Add(1)
		go func(j int) { // CAS 式重试：取当前最大 In+1 写入，冲突让出 CPU 后重试，不用 sleep
			defer wg.Done()
			for {
				li, _ := yc.LatestIngest("S")
				if yc.Apply("S", fmt.Sprintf("v%d", j), int64(j), li.In+1) == nil {
					return
				}
				runtime.Gosched()
			}
		}(j)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				yc.LatestEvent("S")
				yc.AtIngest("S", 8)
				api.SelfCheck()
			}
		}()
	}
	wg.Wait()
	liS, eS := yc.LatestIngest("S")
	okC := eS == nil && liS.In == 16
	for g := 0; g < 8; g++ {
		v, e := yc.LatestIngest(fmt.Sprintf("k%d", g))
		okC = okC && e == nil && v.In == 1
	}
	check("并发写入与并发只读正确", okC)
	if failed {
		fmt.Println("FAIL overall")
		os.Exit(1)
	}
	fmt.Println("OK overall")
}
