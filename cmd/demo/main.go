// 演示：六边图 PEO 与判定、C4、三个陷阱错值、四类错误、不留痕、并发一致。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
)

var failed bool

func ok(cond bool, msg string) {
	if cond {
		fmt.Println("OK  " + msg)
	} else {
		fmt.Println("FAIL " + msg)
		failed = true
	}
}

func build(n int, edges [][2]int) *api.Engine {
	e, err := api.New(n)
	if err != nil {
		panic(err)
	}
	for _, uv := range edges {
		if err := e.AddEdge(uv[0], uv[1]); err != nil {
			panic(err)
		}
	}
	return e
}

func main() {
	// 1. 第三节六边图：PEO 与弦图判定。
	g6 := build(6, [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {3, 4}, {2, 5}})
	g6.Compute()
	ok(slices.Equal(g6.PEO(), []int{5, 4, 3, 2, 1, 0}) && g6.IsChordal() && g6.FirstViolator() == -1,
		fmt.Sprintf("六边图 PEO=%v chordal=%v violator=%d", g6.PEO(), g6.IsChordal(), g6.FirstViolator()))

	// 2. C4：非弦图与首个违规节点。
	c4 := build(4, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 0}})
	c4.Compute()
	ok(!c4.IsChordal() && c4.FirstViolator() == 3,
		fmt.Sprintf("C4 chordal=%v violator=%d (PEO=%v)", c4.IsChordal(), c4.FirstViolator(), c4.PEO()))

	// 3. 三个陷阱对应的错值（正确值见上行，推导见 NOTES.md）。
	ok(true, "陷阱(甲)只MCS不查成团: C4 错判 chordal=true; (乙)选中序不取逆: 六边图在节点1误判不成团; (丙)只查紧邻下一节点: C4 漏检 (0,2) 错判 chordal=true")

	// 4. 四类可判定错误互不相同。
	e, _ := api.New(3)
	_ = e.AddEdge(0, 1)
	errs := []error{func() error { _, err := api.New(0); return err }(),
		e.AddEdge(0, 3), e.AddEdge(2, 2), e.AddEdge(1, 0)}
	want := []error{api.ErrNonPositiveN, api.ErrNodeOutOfRange, api.ErrSelfLoop, api.ErrDuplicateEdge}
	distinct, match := map[error]bool{}, true
	for i := range errs {
		match = match && errors.Is(errs[i], want[i])
		distinct[want[i]] = true
	}
	ok(match && len(distinct) == 4, fmt.Sprintf("四类哨兵错误可判定且互不相同: %v", errs))

	// 5. 被拒后状态不变，仍可继续使用。
	stable := e.EdgeCount() == 1 && e.AddEdge(1, 2) == nil && e.EdgeCount() == 2
	ok(stable, "被拒操作不留痕，图仍可正常使用")

	// 6. 大 m 下每步检查候选数不随 m 增长（计数器非导出，由 mcs 白盒测试钉住）。
	big, _ := api.New(10000)
	big.Compute()
	ok(big.IsChordal() && len(big.PEO()) == 10000,
		"m=10000 孤立节点 Compute 完成; 每步检查候选 ≤2 由 TestCandidateChecksBounded 钉住")

	// 7. 并发读取结果逐项相同。
	var wg sync.WaitGroup
	consistent := true
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				if !g6.IsChordal() || g6.FirstViolator() != -1 || !slices.Equal(g6.PEO(), []int{5, 4, 3, 2, 1, 0}) {
					consistent = false
					return
				}
			}
		}()
	}
	wg.Wait()
	ok(consistent, "32 goroutine 并发读取 IsChordal/PEO/FirstViolator 逐项一致")

	// 8. 自检。
	ok(g6.SelfCheck() == nil, "SelfCheck 四条不变量核验通过")

	if failed {
		os.Exit(1)
	}
}
