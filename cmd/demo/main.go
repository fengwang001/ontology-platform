package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"

	"ontology/api"
)

var failed bool

func ok(name string, cond bool) {
	status := "OK"
	if !cond {
		status, failed = "FAIL", true
	}
	fmt.Println(name + ": " + status)
}

// unexp 读取未导出字段：演示不经由任何导出接口拿计数器，只用反射。
func unexp(v any, names ...string) reflect.Value {
	rv := reflect.ValueOf(v)
	for _, n := range names {
		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		rv = rv.FieldByName(n)
	}
	return rv
}
func commitTo(g *api.Engine, n int64) {
	for i := g.Head() + 1; i <= n; i++ {
		g.Commit(i)
	}
}

func main() {
	// 1. 第三节八步表：每步后的 H/A 与第 6、8 步的 Pos/Downgraded
	g := api.New()
	nop := func() {}
	ops := []func(){func() { g.Commit(0) }, func() { g.Commit(1) }, func() { g.Commit(2) },
		func() { g.Apply(1) }, func() { g.Commit(3) }, nop, func() { g.Commit(4) }, nop}
	want := [][2]int64{{0, -1}, {1, -1}, {2, -1}, {2, 1}, {3, 1}, {3, 1}, {4, 1}, {4, 1}}
	s1 := true
	var r6, r8 api.Result
	for i, op := range ops {
		op()
		switch i {
		case 5:
			r6, _ = g.Read(2, api.Downgrade)
		case 7:
			r8, _ = g.Read(2, api.Downgrade)
		}
		s1 = s1 && g.Head() == want[i][0] && g.Applied() == want[i][1]
	}
	s1 = s1 && r6.Pos == 1 && !r6.Downgraded && r8.Pos == 1 && r8.Downgraded
	ok("steps", s1 && api.New().SelfCheck() == nil)
	// 2. 甲：错式「A<=T 判降级」第6步错成 true，第8步仍 true 不受影响
	wrong := func(a, t int64) bool { return a <= t }
	ok("甲", wrong(1, 1) && wrong(1, 2) == r8.Downgraded)
	// 3. 乙：第6步 Block 不阻塞返回 Pos=1；错返回 T 时第8步 Pos 错成 2（未应用）
	g2 := api.New()
	commitTo(g2, 2)
	g2.Apply(1)
	g2.Commit(3)
	rb, _ := g2.Read(2, api.Block)
	g2.Commit(4)
	ok("乙", rb.Pos == 1 && !rb.Downgraded && g2.Head()-2 == 2 && g2.Head()-2 != g2.Applied())
	// 4. 丙：Downgraded 轨迹 false→true→false，非单调
	g.Commit(5)
	r9, _ := g.Read(2, api.Downgrade)
	g.Apply(3)
	r10, _ := g.Read(2, api.Downgrade)
	ok("丙", r9.Pos == 1 && r9.Downgraded && !r10.Downgraded)
	// 5. 阻塞模式返回后 Applied() >= 冻结的 T
	g3 := api.New()
	commitTo(g3, 4)
	t5 := g3.Head() - 2
	done := make(chan api.Result, 1)
	go func() { res, _ := g3.Read(2, api.Block); done <- res }()
	for n := int64(0); n <= t5; n++ {
		g3.Apply(n)
	}
	res5 := <-done
	ok("block", res5.Pos == g3.Applied() && g3.Applied() >= t5)
	// 6+7. 三类哨兵错误互不相同；被拒后状态不变且可继续用
	g4 := api.New()
	g4.Commit(0)
	e1, e2 := g4.Commit(2), g4.Apply(5)
	_, e3 := g4.Read(-1, api.Downgrade)
	ok("errors", e1 == api.ErrCommitGap && e2 == api.ErrApplyRange && e3 == api.ErrNegativeLag &&
		e1 != e2 && e2 != e3 && e1 != e3)
	ok("notrace", g4.Head() == 0 && g4.Applied() == -1 && g4.Commit(1) == nil && g4.Apply(0) == nil)
	// 8. 大 m 下唤醒检查数不随 m 增长（反射读非导出计数器）
	s8 := true
	for _, m := range []int{100, 1000, 10000} {
		g5 := api.New()
		commitTo(g5, 9)
		go g5.Read(9, api.Block) // 1 个 T=0
		for i := 0; i < m-1; i++ {
			go g5.Read(0, api.Block) // m-1 个 T=9
		}
		for unexp(g5, "r", "w").Len() < m {
			runtime.Gosched()
		}
		g5.Apply(0) // 恰好只唤醒 T=0 那 1 个
		s8 = s8 && unexp(g5, "r", "checked").Int() <= 3
	}
	ok("wakeheap", s8)
	// 9. 并发只读结果逐字段一致
	g6 := api.New()
	commitTo(g6, 49)
	g6.Apply(30)
	same := make(chan bool, 64)
	for i := 0; i < 64; i++ {
		go func() { same <- g6.Head() == 49 && g6.Applied() == 30 }()
	}
	s9 := true
	for i := 0; i < 64; i++ {
		s9 = s9 && <-same
	}
	ok("concread", s9)
	// 10. m 个阻塞者按各自冻结 T 被 Apply 序列正确放行
	m := int64(64)
	g7 := api.New()
	commitTo(g7, m)
	res10 := make(chan bool, m)
	for t := int64(0); t < m; t++ {
		go func() {
			res, _ := g7.Read(m-t, api.Block) // 冻结 T=t
			res10 <- res.Pos >= t && !res.Downgraded
		}()
	}
	for unexp(g7, "r", "w").Len() < int(m) {
		runtime.Gosched()
	}
	for n := int64(0); n <= m; n++ {
		g7.Apply(n)
	}
	allok := true
	for i := int64(0); i < m; i++ {
		allok = allok && <-res10
	}
	ok("concwake", allok)
	if failed {
		os.Exit(1)
	}
}
