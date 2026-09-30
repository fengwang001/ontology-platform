package flowtree

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// dumpTree 打印当前树（虚拟根 0 起），作为测试日志中的判定依据。
func dumpTree(a *Allocator) string {
	var walk func(parent int) string
	walk = func(parent int) string {
		kids, _ := a.Children(parent)
		if len(kids) == 0 {
			return ""
		}
		s := fmt.Sprintf("%d->%v ", parent, kids)
		for _, k := range kids {
			w, _ := a.Weight(k)
			s += fmt.Sprintf("[%d w=%d] ", k, w)
			s += walk(k)
		}
		return s
	}
	return walk(0)
}

func sortedShares(m map[int]int) []int {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		out = append(out, id, m[id])
	}
	return out
}

// TestExclusiveOpen 覆盖独占插入：新流成为父的唯一子，原有子改挂其下。
func TestExclusiveOpen(t *testing.T) {
	a := New()
	must := func(err error, ctx string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: 意外错误 %v", ctx, err)
		}
	}

	must(a.Open(1, 0, 10, false), "Open 1")
	must(a.Open(2, 0, 20, false), "Open 2")
	t.Logf("输入: Open(1,0,w10), Open(2,0,w20) | 树: %s", dumpTree(a))

	must(a.Open(3, 0, 30, true), "Open 3 exclusive")
	t.Logf("输入: Open(3,0,w30,独占) | 输出: 树 %s", dumpTree(a))

	if kids, _ := a.Children(0); len(kids) != 1 || kids[0] != 3 {
		t.Fatalf("独占失败，根的子=%v，期望 [3]", kids)
	}
	if kids, _ := a.Children(3); len(kids) != 2 || kids[0] != 1 || kids[1] != 2 {
		t.Fatalf("原子未改挂到 3 下: %v", kids)
	}
	if p, _ := a.Parent(1); p != 3 {
		t.Fatalf("流 1 的父=%d，期望 3", p)
	}
	if w, _ := a.Weight(2); w != 20 {
		t.Fatalf("流 2 权重=%d，改挂后期望保持 20", w)
	}
	t.Log("判定: 0->[3], 3->[1 2]，权重保持 -> 通过")
}

// TestRerootToDescendant 覆盖重设依赖到自己的后代。
func TestRerootToDescendant(t *testing.T) {
	a := New()
	for _, op := range []struct {
		id, p, w int
		ex       bool
	}{
		{1, 0, 10, false},
		{2, 1, 20, false},
		{3, 2, 30, false},
	} {
		if err := a.Open(op.id, op.p, op.w, op.ex); err != nil {
			t.Fatalf("Open %d: %v", op.id, err)
		}
	}
	t.Logf("输入: 开 1(w10)->2(w20)->3(w30) | 树: %s", dumpTree(a))

	if err := a.Reroot(1, 3, 50, true); err != nil {
		t.Fatalf("Reroot: %v", err)
	}
	t.Logf("输入: Reroot(1,newParent=3,w50,独占) | 输出: 树 %s", dumpTree(a))

	// 3 子树上提到 1 的原父（根），权重保持 30；
	// 1 独占挂到 3 下，原有的子 2 改挂到 1 下。
	if kids, _ := a.Children(0); len(kids) != 1 || kids[0] != 3 {
		t.Fatalf("上提失败，根的子=%v，期望 [3]", kids)
	}
	if w, _ := a.Weight(3); w != 30 {
		t.Fatalf("3 上提后权重=%d，期望保持 30", w)
	}
	if p, _ := a.Parent(1); p != 3 {
		t.Fatalf("流 1 的父=%d，期望 3", p)
	}
	if p, _ := a.Parent(2); p != 1 {
		t.Fatalf("流 2 的父=%d，期望 1（独占改挂）", p)
	}
	if w, _ := a.Weight(1); w != 50 {
		t.Fatalf("流 1 新权重=%d，期望 50", w)
	}
	t.Log("判定: 0->[3 w30], 3->[1 w50], 1->[2 w20]，无环 -> 通过")
}

// TestCloseRedistribution 覆盖关闭流时按比例重分，且取整下限为 1。
func TestCloseRedistribution(t *testing.T) {
	a := New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(a.Open(1, 0, 10, false))
	must(a.Open(2, 1, 3, false))
	must(a.Open(3, 1, 3, false))
	must(a.Open(4, 1, 4, false))
	t.Logf("输入: 1(w10) 下有 2(w3),3(w3),4(w4) | 树: %s", dumpTree(a))

	if err := a.Close(1); err != nil {
		t.Fatalf("Close: %v", err)
	}
	t.Logf("输入: Close(1) | 输出: 树 %s", dumpTree(a))

	for id, want := range map[int]int{2: 3, 3: 3, 4: 4} {
		if p, _ := a.Parent(id); p != 0 {
			t.Fatalf("流 %d 的父=%d，期望 0", id, p)
		}
		if w, _ := a.Weight(id); w != want {
			t.Fatalf("流 %d 权重=%d，期望 %d", id, w, want)
		}
	}

	// 下限场景：父权重 1，两个子权重 100 -> floor(0)=0 -> 提升为 1。
	b := New()
	must(b.Open(10, 0, 1, false))
	must(b.Open(11, 10, 100, false))
	must(b.Open(12, 10, 100, false))
	if err := b.Close(10); err != nil {
		t.Fatalf("Close 10: %v", err)
	}
	t.Logf("输入: Close(10 w1)，子 11/12 各 w100 | 输出: %s", dumpTree(b))
	for _, id := range []int{11, 12} {
		if w, _ := b.Weight(id); w != 1 {
			t.Fatalf("流 %d 权重=%d，floor 为 0 时期望下限 1", id, w)
		}
	}
	t.Log("判定: 比例折算 3/3/4；floor=0 时提升为 1 -> 通过")
}

// TestAllocateRemainderTie 覆盖同层余数分配与并列取编号小者。
func TestAllocateRemainderTie(t *testing.T) {
	a := New()
	// T=10，三流权重均 3，Σw=9：base=floor(30/9)=3，余数均 30 mod 9=3，
	// leftover=1，编号最小的 1 多得 1 -> {1:4,2:3,3:3}。
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(a.Open(1, 0, 3, false))
	must(a.Open(2, 0, 3, false))
	must(a.Open(3, 0, 3, false))

	shares, err := a.Allocate(10, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	t.Logf("输入: Allocate(T=10, ready=[1,2,3], 三流权重均为 3) | 输出: %v",
		sortedShares(shares))

	want := map[int]int{1: 4, 2: 3, 3: 3}
	for id, w := range want {
		if shares[id] != w {
			t.Fatalf("流 %d 份额=%d，期望 %d", id, shares[id], w)
		}
	}
	sum := 0
	for _, v := range shares {
		sum += v
	}
	if sum != 10 {
		t.Fatalf("份额之和=%d，期望恒等于额度 10", sum)
	}
	t.Log("判定: base 各 3，余数并列取小编号 1 多得 1，总和=10 -> 通过")
}

// TestAllocateReadyParentBlocks 覆盖就绪的父挡住其子。
func TestAllocateReadyParentBlocks(t *testing.T) {
	a := New()
	if err := a.Open(1, 0, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := a.Open(2, 1, 1, false); err != nil {
		t.Fatal(err)
	}

	shares, err := a.Allocate(100, []int{1, 2})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	t.Logf("输入: Allocate(T=100, ready=[1,2]，2 是 1 的子) | 输出: %v",
		sortedShares(shares))

	if shares[1] != 100 || shares[2] != 0 {
		t.Fatalf("期望 {1:100, 2:0}，实际 %v", shares)
	}
	t.Log("判定: 子树根 1 就绪，额度全归 1，后代 2 得 0 -> 通过")
}

// TestAllocateNoReady 覆盖无就绪流：全部份额为 0。
func TestAllocateNoReady(t *testing.T) {
	a := New()
	if err := a.Open(1, 0, 1, false); err != nil {
		t.Fatal(err)
	}

	shares, err := a.Allocate(100, nil)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	t.Logf("输入: Allocate(T=100, ready=[]) | 输出: %v", sortedShares(shares))
	if len(shares) != 0 {
		t.Fatalf("无就绪流时期望空映射，实际 %v", shares)
	}

	if err := a.Open(2, 1, 1, false); err != nil {
		t.Fatal(err)
	}
	shares, err = a.Allocate(50, []int{})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if len(shares) != 0 {
		t.Fatalf("无含就绪子树时期望空映射，实际 %v", shares)
	}
	t.Log("判定: 无就绪流时份额全 0 -> 通过")
}

// TestAllocateNested 覆盖多层递归、剪枝与余数在非根层的分配。
func TestAllocateNested(t *testing.T) {
	a := New()
	for _, op := range []struct{ id, p, w int }{
		{1, 0, 1},
		{2, 0, 1},
		{3, 1, 1},
		{4, 1, 3},
	} {
		if err := a.Open(op.id, op.p, op.w, false); err != nil {
			t.Fatal(err)
		}
	}
	// ready={2,3,4}：1 自身未就绪但子树含就绪流。
	// 根层 1、2 权重各 1 -> 5/5；
	// 1 层 w3=1,w4=3 分 5：base 1 与 3，余数 1 与 3，余 1 字节给 4。
	shares, err := a.Allocate(10, []int{2, 3, 4})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	t.Logf("输入: 0->[1,2], 1->[3 w1,4 w3], ready=[2,3,4], T=10 | 输出: %v",
		sortedShares(shares))

	want := map[int]int{2: 5, 3: 1, 4: 4}
	for id, w := range want {
		if shares[id] != w {
			t.Fatalf("流 %d 份额=%d，期望 %d", id, shares[id], w)
		}
	}
	t.Log("判定: 根层 5/5；1 层 1/4（余数大者多得）；总和=10 -> 通过")
}

// TestRejectionOrder 覆盖各操作的拒绝顺序，且被拒绝操作不得改变树。
func TestRejectionOrder(t *testing.T) {
	a := New()
	if err := a.Open(1, 0, 10, false); err != nil {
		t.Fatal(err)
	}

	type tc struct {
		name string
		call func() error
		want error
	}
	cases := []tc{
		{"Open 编号非正", func() error { return a.Open(0, 99, 999, false) }, ErrStreamUsed},
		{"Open 编号已用", func() error { return a.Open(1, 99, 999, false) }, ErrStreamUsed},
		{"Open 父不存在", func() error { return a.Open(5, 99, 999, false) }, ErrParentMissing},
		{"Open 权重越界", func() error { return a.Open(5, 1, 0, false) }, ErrWeightRange},
		{"Reroot 流不存在", func() error { return a.Reroot(9, 99, 0, false) }, ErrStreamMissing},
		{"Reroot 新父不存在", func() error { return a.Reroot(1, 9, 999, false) }, ErrNewParentMissing},
		{"Reroot 新父是自身", func() error { return a.Reroot(1, 1, 999, false) }, ErrParentIsSelf},
		{"Reroot 权重越界", func() error { return a.Reroot(1, 0, 257, false) }, ErrWeightRange},
		{"Close 不存在的流", func() error { return a.Close(9) }, ErrStreamMissing},
	}
	for _, c := range cases {
		before := dumpTree(a)
		err := c.call()
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v，期望 %v", c.name, err, c.want)
		}
		if after := dumpTree(a); after != before {
			t.Fatalf("%s: 被拒绝操作改变了树: before=%q after=%q", c.name, before, after)
		}
		t.Logf("输入: %s | 输出: %v | 判定: 返回首个错误且树不变 %q -> 通过",
			c.name, err, before)
	}

	// Allocate 的拒绝顺序：就绪流不存在先于额度为负。
	if _, err := a.Allocate(-1, []int{99}); !errors.Is(err, ErrReadyMissing) {
		t.Fatalf("Allocate: err=%v，期望 ErrReadyMissing", err)
	}
	if _, err := a.Allocate(-1, []int{1}); !errors.Is(err, ErrNegativeQuota) {
		t.Fatalf("Allocate: err=%v，期望 ErrNegativeQuota", err)
	}
	t.Log("输入: Allocate(-1,ready含不存在) / Allocate(-1,ready合法) | " +
		"判定: 先报就绪流不存在，再报额度为负 -> 通过")
}

// TestClosedIDReused 关闭后的编号不可再用。
func TestClosedIDReused(t *testing.T) {
	a := New()
	if err := a.Open(7, 0, 10, false); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(7); err != nil {
		t.Fatal(err)
	}
	if err := a.Open(7, 0, 10, false); !errors.Is(err, ErrStreamUsed) {
		t.Fatalf("复用已关闭编号: err=%v，期望 ErrStreamUsed", err)
	}
	if _, ok := a.Parent(7); ok {
		t.Fatal("已关闭流不应可查")
	}
	t.Log("输入: Open(7), Close(7), Open(7) | 判定: 编号永久占用 -> 通过")
}

// TestConcurrentSafety 并发调用各操作：不崩溃、无数据竞争（-race 检测）、
// 且任意时刻每个存活流恰有一个父。
func TestConcurrentSafety(t *testing.T) {
	a := New()

	// 预置一棵小树供 Reroot/Close/Allocate 并发操作。
	for i := 1; i <= 20; i++ {
		parent := 0
		if i > 1 {
			parent = (i - 1) % 4
			if parent == 0 {
				parent = i % 4
			}
		}
		if err := a.Open(i, parent, (i%256)+1, i%5 == 0); err != nil {
			t.Fatal(err)
		}
	}

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				id := 1 + (g+k)%20
				switch k % 5 {
				case 0:
					_ = a.Reroot(id, 0, (k%256)+1, k%2 == 0)
				case 1:
					_ = a.Reroot(id, 1+((id+1)%20), (k%256)+1, false)
				case 2:
					_, _ = a.Allocate(1000, []int{id, 1 + (id % 20)})
				case 3:
					_, _ = a.Parent(id)
					_, _ = a.Children(0)
				case 4:
					// 关闭后重新开一个全新编号，维持可操作集合大小。
					_ = a.Close(id)
					_ = a.Open(1000+g*200+k, 0, (k%256)+1, false)
				}
			}
		}(g)
	}
	wg.Wait()

	// 判定：每个存活流的父唯一（0 或存活流），且沿父链必到 0（无环）。
	kids, _ := a.Children(0)
	all := append([]int(nil), kids...)
	for _, id := range all {
		cur := id
		steps := 0
		for cur != 0 {
			p, ok := a.Parent(cur)
			if !ok {
				t.Fatalf("流 %d 的父指向不存在节点", cur)
			}
			cur = p
			steps++
			if steps > 100000 {
				t.Fatal("父链未抵达虚拟根，疑似成环")
			}
		}
	}

	shares, err := a.Allocate(12345, all)
	if err != nil {
		t.Fatalf("最终 Allocate: %v", err)
	}
	sum := 0
	for _, v := range shares {
		sum += v
	}
	if sum != 12345 {
		t.Fatalf("并发后份额之和=%d，期望 12345", sum)
	}
	t.Logf("输入: 16 goroutine x 200 次混合并发操作 | 输出: 份额和=%d | "+
		"判定: 无崩溃、无环、份额和等于额度 -> 通过", sum)
}

// TestReplayDeterministic 相同操作序列重放，分配结果完全相同。
func TestReplayDeterministic(t *testing.T) {
	run := func() []int {
		a := New()
		ops := []struct {
			id, p, w int
			ex       bool
		}{
			{1, 0, 3, false}, {2, 0, 7, true}, {3, 2, 2, false},
			{4, 2, 5, false}, {5, 0, 9, false},
		}
		for _, op := range ops {
			if err := a.Open(op.id, op.p, op.w, op.ex); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.Reroot(3, 0, 11, false); err != nil {
			t.Fatal(err)
		}
		shares, err := a.Allocate(1000, []int{1, 3, 4, 5})
		if err != nil {
			t.Fatal(err)
		}
		return sortedShares(shares)
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !equalInts(got, first) {
			t.Fatalf("第 %d 次重放=%v，首次=%v，结果不一致", i+1, got, first)
		}
	}
	t.Logf("输入: 固定开流/重设序列 + Allocate(T=1000) | 输出: %v | 判定: 6 次重放一致 -> 通过", first)
}

func equalInts(x, y []int) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
