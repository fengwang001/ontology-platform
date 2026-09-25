// 物化视图原子切换读一致：演示程序。不读参数、不联网，全部通过时退出码 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/view"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK  ", name)
}

func main() {
	v := api.New()

	// 第三节八步操作序列
	seq1, c1 := v.Read()
	ok("步1 Read -> (0,{})", seq1 == 0 && len(c1) == 0)
	_ = v.StartRefresh()
	_ = v.Stage("a", "1")
	_ = v.Stage("b", "x")
	seq5, c5 := v.Read()
	ok("步5 Read=(0,{}) Stage提交前不可见", seq5 == 0 && len(c5) == 0)
	_ = v.Commit()
	seq7, c7 := v.Read()
	ok("步7 Read=(1,{a:1,b:x}) Commit原子可见", seq7 == 1 && c7["a"] == "1" && c7["b"] == "x" && len(c7) == 2)
	_ = v.StartRefresh()
	_ = v.Stage("b", "y")
	_ = v.Abort()
	seq8, c8 := v.Read()
	ok("步8 Abort回退 Read=(1,{a:1,b:x})", seq8 == 1 && c8["b"] == "x" && len(c8) == 2)

	// 切换中写：Commit 后到达的 Stage 落入新一代暂存
	_ = v.StartRefresh()
	_ = v.Stage("c", "2")
	_, cmid := v.Read()
	_ = v.Commit()
	seqN, cN := v.Read()
	ok("切换中Stage落新一代暂存", cmid["c"] == "" && seqN == 2 && cN["c"] == "2")

	// 三类可判定错误互不相同；被拒后状态不变
	e1, e2 := v.Stage("x", "y"), v.Commit()
	_ = v.StartRefresh()
	e3 := v.StartRefresh()
	e4 := v.Stage("", "v")
	ok("三类哨兵错误可判定且互异", errors.Is(e1, view.ErrNoRefresh) && errors.Is(e2, view.ErrNoRefresh) &&
		errors.Is(e3, view.ErrRefreshActive) && errors.Is(e4, view.ErrEmptyStageKey) &&
		view.ErrNoRefresh != view.ErrRefreshActive && view.ErrRefreshActive != view.ErrEmptyStageKey)
	seqR, cR := v.Read()
	ok("被拒后状态不变且可继续用", seqR == 2 && len(cR) == 3 && v.Abort() == nil)

	// 大 m 下 Commit 复制数为 0（由 SelfCheck 内部断言，计数器非导出）
	ok("大m下Commit复制数为0", view.SelfCheck() == nil)

	// 并发：N 读者 + 1 写者 M 轮提交，每个 (seq,cells) 自洽，最终 Seq==M
	const N, M = 8, 200
	cv := api.New()
	var wg sync.WaitGroup
	var good atomic.Bool
	good.Store(true)
	start := make(chan struct{})
	for r := 0; r < N; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < M; i++ {
				s, cells := cv.Read()
				_, hasCur := cells[fmt.Sprintf("k%d", s)]
				_, hasNext := cells[fmt.Sprintf("k%d", s+1)]
				if int64(len(cells)) != s || (s > 0 && !hasCur) || hasNext {
					good.Store(false)
					return
				}
			}
		}()
	}
	close(start)
	for i := 1; i <= M; i++ {
		_ = cv.StartRefresh()
		_ = cv.Stage(fmt.Sprintf("k%d", i), "v")
		_ = cv.Commit()
	}
	wg.Wait()
	seqF, _ := cv.Read()
	ok("并发读自洽且最终Seq==M", good.Load() && seqF == M)

	ok("SelfCheck", v.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
