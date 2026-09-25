package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/cow"
	"ontology/mgmt"
)

var failed bool

func check(name, trace string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK " + name + " " + trace)
}

func main() {
	// cow：克隆整页拷贝、原页不受影响。
	p := cow.New([]byte{1, 2, 3, 4})
	p.Incr()
	q := p.Clone()
	p.Decr()
	q.Set(0, 9)
	check("cow", "", p.Refs() == 1 && q.Refs() == 1 && p.Byte(0) == 1 && q.Byte(0) == 9)

	// 第三节八步：每步核验引用计数与内容；第3步拷贝后 V0=0、V1=9。
	m := api.NewManager(4, 100)
	z := []byte{0, 0, 0, 0}
	v0, _ := m.Alloc(z)
	v1, _ := m.Snapshot(v0)
	v2, _ := m.Snapshot(v0)
	ok := m.RefCount(v0) == 3
	_ = m.Write(v1, 0, 9)
	r0, _ := m.Read(v0, 0)
	r2, _ := m.Read(v2, 0)
	r1, _ := m.Read(v1, 0)
	ok = ok && r0 == 0 && r2 == 0 && r1 == 9 && m.RefCount(v1) == 1 && m.RefCount(v0) == 2
	_ = m.Write(v0, 0, 5)
	ok = ok && m.RefCount(v0) == 1 // 第4步后 V0 独占新页
	_ = m.Release(v2)
	_, eRel := m.Read(v2, 0)
	ok = ok && errors.Is(eRel, mgmt.ErrInvalidView) // 第5步 A 归零释放、V2 失效
	_ = m.Write(v0, 1, 7)
	ok = ok && m.RefCount(v0) == 1 // 第6步独占原地写，计数仍 1
	v3, _ := m.Snapshot(v0)
	g1, _ := m.Read(v1, 0)
	g0a, _ := m.Read(v0, 0)
	g3, _ := m.Read(v3, 1)
	ok = ok && g1 == 9 && g0a == 5 && g3 == 7 && m.RefCount(v0) == 2
	trace := "1:A2 2:A3 3:A2[0000],B1[9000](V0=0,V1=9) 4:A1,B1,C1[5000] 5:A释放 6:C[5700]原地 7:C2 8:V1=9,V0=5"
	check("8steps/isolation/exclusive/release", trace, ok)
	// 引用计数守恒 + 与朴素参照一致：交给内置自检。
	check("selfcheck conservation+naive", "", m.SelfCheck() == nil)
	// 四类哨兵错误互不相同；被拒后状态不变、可继续使用。
	bad := api.NewManager(4, 10)
	bv, _ := bad.Alloc(z)
	small := api.NewManager(4, 1)
	sv, _ := small.Alloc(z)
	ss, _ := small.Snapshot(sv)
	e2 := func(_ api.View, e error) error { return e }
	cases := []struct{ err, want error }{
		{bad.Release(api.View{}), mgmt.ErrInvalidView},
		{bad.Write(bv, 4, 1), mgmt.ErrOffsetOutOfRange},
		{e2(bad.Alloc([]byte{1})), mgmt.ErrBadDataLen},
		{small.Write(sv, 0, 1), mgmt.ErrPageLimit},
	}
	distinct, allOK := map[error]bool{}, true
	for _, c := range cases {
		allOK = allOK && errors.Is(c.err, c.want)
		distinct[c.want] = true
	}
	rb, _ := small.Read(sv, 0)
	allOK = allOK && len(distinct) == 4 && rb == 0 && small.RefCount(ss) == 2
	check("4 errors distinct + no-trace", fmt.Sprintf("%d distinct", len(distinct)), allOK)

	// 大 m：共享旧页的 view 全读旧值、彼此独立；O(1) 计数器断言在同包测试。
	scaleOK := true
	for _, n := range []int{100, 1000, 10000} {
		s := api.NewManager(4, n+1)
		root, _ := s.Alloc(z)
		views := make([]api.View, n)
		for i := range views {
			views[i], _ = s.Snapshot(root)
		}
		if s.Write(root, 0, 5) != nil || s.RefCount(root) != 1 {
			scaleOK = false
		}
		for _, w := range views {
			if r, _ := s.Read(w, 0); r != 0 {
				scaleOK = false
			}
		}
		for _, w := range views {
			_ = s.Release(w)
		}
		_ = s.Release(root)
	}
	check("write-check O(1) m=100..10000", "", scaleOK)

	// 并发：各自快照+写不同偏移，互不影响且最终无泄漏。
	const N = 64
	c := api.NewManager(N, N+1)
	root, _ := c.Alloc(make([]byte, N))
	mine := make([]api.View, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.Snapshot(root)
			if err != nil {
				panic(err)
			}
			if err := c.Write(v, i, byte(i+1)); err != nil {
				panic(err)
			}
			mine[i] = v
		}()
	}
	wg.Wait()
	concOK := c.RefCount(root) == 1
	for i := 0; i < N; i++ {
		for j := 0; j < N; j++ {
			want := byte(0)
			if j == i {
				want = byte(i + 1)
			}
			if r, _ := c.Read(mine[i], j); r != want {
				concOK = false
			}
		}
	}
	for i := 0; i < N; i++ {
		_ = c.Release(mine[i])
	}
	_ = c.Release(root)
	check("concurrent independent writes", fmt.Sprintf("N=%d", N), concOK && c.RefCount(root) == 0)

	if failed {
		os.Exit(1)
	}
}
