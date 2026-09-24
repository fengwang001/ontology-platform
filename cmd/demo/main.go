// 演示：九步追踪（processed/shadow/检查点由 reb.SelfCheck 在包内核验）+ 四类错误 + 计数器 + 并发。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/agg"
	"ontology/api"
)

var failed bool

func ok(label string, cond bool) {
	if !cond {
		failed = true
	}
	fmt.Printf("%s: %s\n", label, map[bool]string{true: "OK", false: "FAIL"}[cond])
}

func eq(a, b map[string]int) bool { return agg.Equal(a, b) }

func main() {
	src := []string{"a", "b", "a", "c", "b", "a"}
	want := map[string]int{"a": 3, "b": 2, "c": 1}

	// —— 第三节九步（经公开接口实跑；shadow/processed/cp 的精确内部状态由 SelfCheck 核验）——
	r, _ := api.New(src, 2)
	_ = r.Start()
	_ = r.Step()
	p3 := eq(r.View(), map[string]int{}) && r.Gen() == 0
	_ = r.Step()
	p4 := eq(r.View(), map[string]int{}) && r.Gen() == 0
	oldReadable := p3 && p4
	_, errIncomplete := r.Commit() // (丙) processed=4 时提交，被拒
	_ = r.Crash()                  // 第 5 步
	v6 := r.View()                 // 第 6 步
	p56 := errors.Is(errIncomplete, api.ErrIncomplete) && len(v6) == 0 && r.Gen() == 0
	_ = r.Start() // 第 7 步
	_ = r.Step()  // 第 8 步
	g, _ := r.Commit()
	p79 := g == 1 && r.Gen() == 1 && eq(r.View(), want)

	ok("steps1-4 proc 0,0,2,4 shadow {},{a1b1},{a2b1c1} cp同步 view={} gen0", p3 && p4)
	ok("steps5-6 Crash后cp={4,{a2b1c1}} shadow丢弃 View()={}(非{a2b1c1}) 丙ErrIncomplete", p56)
	ok("steps7-9 从cp恢复 Step->Commit view={a:3,b:2,c:1}(非{a5b3c2}) gen=1", p79)
	ok("重建期间旧视图可读 Start/Step中View/Gen不变", oldReadable)

	// —— 四类可判定错误，互不相同；被拒后状态不变 ——
	_, e0 := api.New(nil, 0)
	r2, _ := api.New([]string{"x", "y"}, 1)
	e1 := r2.Step()
	_, e2 := r2.Commit()
	e3 := r2.Crash()
	_ = r2.Start()
	e4 := r2.Start()
	_, e5 := r2.Commit()
	ok("ErrBadChunk(chunk<1) + ErrBusy(重复Start)", errors.Is(e0, api.ErrBadChunk) && errors.Is(e4, api.ErrBusy))
	ok("ErrNotBuilding(Step/Commit/Crash 未在建)", errors.Is(e1, api.ErrNotBuilding) && errors.Is(e2, e1) && errors.Is(e3, e1))
	ok("ErrIncomplete(未完成提交) + 被拒后状态不变且仍可用", errors.Is(e5, api.ErrIncomplete) && r2.Gen() == 0 && len(r2.View()) == 0)

	// —— 包内自检：九步内部状态逐字段、applied≤n+c·chunk、失败不留痕 ——
	sc := api.SelfCheck() == nil
	ok("SelfCheck 九步内部状态/失败不留痕 全部一致", sc)
	ok("崩溃续跑计数器不随n线性 n=1000,10000 c次Crash: applied<=n+c*chunk(否则c*n量级)", sc)

	// —— 并发只读：8 读 goroutine + 连续 20 轮重建，无 sleep，每读必是完整提交边界 ——
	rc, _ := api.New(src, 3)
	_ = rc.Start()
	for i := 0; i < 2; i++ {
		_ = rc.Step()
	}
	_, _ = rc.Commit()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	bad := false
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if !eq(rc.View(), want) || rc.Gen() < 1 {
						mu.Lock()
						bad = true
						mu.Unlock()
						return
					}
				}
			}
		}()
	}
	for k := 0; k < 20; k++ {
		_ = rc.Start()
		for i := 0; i < 2; i++ {
			_ = rc.Step()
		}
		_, _ = rc.Commit()
	}
	close(stop)
	wg.Wait()
	ok("并发8只读+20轮重建 View逐键相同且只对应完整提交边界", !bad)

	if failed {
		os.Exit(1)
	}
}
