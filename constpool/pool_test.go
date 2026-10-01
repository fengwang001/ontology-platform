package constpool

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
)

// naivePool 是按题面规则直接写成的朴素单线程模拟，用于与真实实现对照。
type naivePool struct {
	cap    int
	elems  []Const
	lookup map[key]int
}

func newNaive(capacity int) *naivePool {
	if capacity < 1 {
		panic(ErrInvalidCapacity)
	}
	return &naivePool{cap: capacity, lookup: map[key]int{}}
}

func (n *naivePool) intern(c Const) (int, error) {
	k, canon, err := canonicalize(c)
	if err != nil {
		return 0, err
	}
	if idx, ok := n.lookup[k]; ok {
		return idx, nil
	}
	if len(n.elems) >= n.cap {
		return 0, ErrPoolFull
	}
	idx := len(n.elems)
	n.elems = append(n.elems, canon)
	n.lookup[k] = idx
	return idx, nil
}

func (n *naivePool) merge(snapshot []Const) ([]int, error) {
	keys := make([]key, len(snapshot))
	canons := make([]Const, len(snapshot))
	missing := map[key]struct{}{}
	for i, c := range snapshot {
		k, canon, err := canonicalize(c)
		if err != nil {
			return nil, err
		}
		keys[i], canons[i] = k, canon
		missing[k] = struct{}{}
	}
	for k := range missing {
		if _, ok := n.lookup[k]; ok {
			delete(missing, k)
		}
	}
	if len(n.elems)+len(missing) > n.cap {
		return nil, ErrPoolFull
	}
	reloc := make([]int, len(snapshot))
	for i := range snapshot {
		if idx, ok := n.lookup[keys[i]]; ok {
			reloc[i] = idx
			continue
		}
		idx := len(n.elems)
		n.elems = append(n.elems, canons[i])
		n.lookup[keys[i]] = idx
		reloc[i] = idx
	}
	return reloc, nil
}

func intC(v int64) Const     { return Const{Kind: KindInt, Int: v} }
func floatC(v float64) Const { return Const{Kind: KindFloat, Float: v} }
func strC(v string) Const    { return Const{Kind: KindString, Str: v} }

func TestNewInvalidCapacity(t *testing.T) {
	for _, c := range []int{0, -1, -100} {
		if _, err := New(c); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("New(%d) err = %v, want ErrInvalidCapacity", c, err)
		}
	}
	t.Logf("判定依据: 容量 < 1 直接拒绝，池未被构造")
}

func TestDedupCriteria(t *testing.T) {
	p, err := New(16)
	if err != nil {
		t.Fatal(err)
	}

	// 整数 1 与浮点 1.0 是两个常量。
	i1, _ := p.Intern(intC(1))
	f1, _ := p.Intern(floatC(1.0))
	if i1 == f1 {
		t.Fatalf("int 1 与 float 1.0 必须是不同下标, got both %d", i1)
	}
	t.Logf("输入 Intern(int 1), Intern(float 1.0) => 下标 %d, %d；判定依据: 种类不同永不相同", i1, f1)

	// +0.0 与 -0.0 按位比较，是两个常量。
	posZero, _ := p.Intern(floatC(math.Copysign(0, +1)))
	negZero, _ := p.Intern(floatC(math.Copysign(0, -1)))
	if posZero == negZero {
		t.Fatalf("+0.0 与 -0.0 必须是不同下标")
	}
	t.Logf("+0.0 => %d, -0.0 => %d；判定依据: 浮点按位比较, 符号位不同", posZero, negZero)

	// 不同位模式的 NaN 全部驻留为同一个常量。
	nans := []float64{
		math.NaN(),
		math.Float64frombits(0x7FF0000000000001),
		math.Float64frombits(0x7FF8000000000001),
		math.Float64frombits(0xFFF8000000000001),
		math.Float64frombits(0x7FF7FFFFFFFFFFFF),
	}
	first, _ := p.Intern(floatC(nans[0]))
	for _, n := range nans[1:] {
		idx, _ := p.Intern(floatC(n))
		if idx != first {
			t.Fatalf("NaN %x 命中下标 %d, 期望 %d", math.Float64bits(n), idx, first)
		}
	}
	got, _ := p.Get(first)
	if math.Float64bits(got.Float) != canonicalNaNBits {
		t.Fatalf("NaN 规范化位模式 = %x, want %x", math.Float64bits(got.Float), canonicalNaNBits)
	}
	t.Logf("5 个不同 NaN => 同一下标 %d, Get 规范化位模式 %x", first, canonicalNaNBits)

	// 字符串按字节比较："A" 与不同字节序列区分。
	a, _ := p.Intern(strC("A"))
	if idx, _ := p.Intern(strC("A")); idx != a {
		t.Fatalf("重复字符串应命中 %d, got %d", a, idx)
	}
	if idx, _ := p.Intern(strC("a")); idx == a {
		t.Fatalf("\"A\" 与 \"a\" 字节不同, 不应相等")
	}

	// 整数 1 再次驻留仍命中原下标，下标连续无空洞。
	if idx, _ := p.Intern(intC(1)); idx != i1 {
		t.Fatalf("重复 int 1 应命中 %d, got %d", i1, idx)
	}
	t.Logf("输出: 当前池大小 = %d, 下标 0..%d 连续", p.Len(), p.Len()-1)
}

func TestFullPoolHitAndReject(t *testing.T) {
	p, _ := New(3)
	i0, err := p.Intern(intC(10))
	if err != nil || i0 != 0 {
		t.Fatalf("首次驻留 = (%d, %v), want (0, nil)", i0, err)
	}
	if _, err := p.Intern(intC(20)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Intern(strC("full")); err != nil {
		t.Fatal(err)
	}

	// 池满：新常量被拒绝，池不变。
	idx, err := p.Intern(floatC(3.14))
	if !errors.Is(err, ErrPoolFull) {
		t.Fatalf("满池新常量 err = %v, want ErrPoolFull", err)
	}
	if p.Len() != 3 {
		t.Fatalf("拒绝后池大小 = %d, want 3", p.Len())
	}
	t.Logf("输入 Intern(float 3.14) 在池满时 => err ErrPoolFull；判定依据: 池大小 %d == 容量 3 且常量为新", p.Len())

	// 池满但命中已有常量：仍返回原下标。
	idx, err = p.Intern(intC(10))
	if err != nil || idx != i0 {
		t.Fatalf("满池命中已有常量 = (%d, %v), want (%d, nil)", idx, err, i0)
	}
	if p.Len() != 3 {
		t.Fatalf("命中后池大小 = %d, want 3", p.Len())
	}
	t.Logf("输入 Intern(int 10) 在池满时 => 下标 %d；判定依据: 先查命中, 不触发容量检查", i0)

	// 未知种类优先于满池判定。
	if _, err := p.Intern(Const{Kind: Kind(99)}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("未知种类 err = %v, want ErrUnknownKind", err)
	}
}

func TestMergePartialAndOverflow(t *testing.T) {
	// 目标池已有 [int(1), str("a")]，容量 4。
	p, _ := New(4)
	p.Intern(intC(1))
	p.Intern(strC("a"))

	// 源快照：部分命中、部分新增；含源池内重复常量。
	snapshot := []Const{
		intC(1),   // 0: 命中 -> 0
		intC(2),   // 1: 新增 -> 2
		strC("b"), // 2: 新增 -> 3
		intC(2),   // 3: 源内重复 -> 同 2
		strC("a"), // 4: 命中 -> 1
	}
	reloc, err := p.Merge(snapshot)
	if err != nil {
		t.Fatalf("恰好放下应成功, got %v", err)
	}
	want := []int{0, 2, 3, 2, 1}
	if fmt.Sprint(reloc) != fmt.Sprint(want) {
		t.Fatalf("重定位表 = %v, want %v", reloc, want)
	}
	if p.Len() != 4 {
		t.Fatalf("合并后大小 = %d, want 4", p.Len())
	}
	t.Logf("输入快照 %v\n输出重定位表 %v；判定依据: 缺 2 个不同常量, 2+2=4 <= 容量 4", snapshot, reloc)

	// 再合并：只缺 1 个但容量已满，超一个即整体拒绝且无任何新增。
	before := p.Snapshot()
	_, err = p.Merge([]Const{intC(1), floatC(9.0)})
	if !errors.Is(err, ErrPoolFull) {
		t.Fatalf("超容量合并 err = %v, want ErrPoolFull", err)
	}
	after := p.Snapshot()
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("拒绝后池发生变化: before=%v after=%v", before, after)
	}
	if _, err := p.Get(4); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("拒绝后下标 4 err = %v, want ErrIndexOutOfRange", err)
	}
	t.Logf("输入 Merge([int(1)命中, float(9)新增]) => ErrPoolFull；判定依据: 缺 1, 4+1=5 > 4, 整体回滚")

	// 未知种类优先于容量判定，且池不变。
	_, err = p.Merge([]Const{intC(1), {Kind: Kind(7)}})
	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("快照未知种类 err = %v, want ErrUnknownKind", err)
	}
	if fmt.Sprint(p.Snapshot()) != fmt.Sprint(before) {
		t.Fatal("未知种类拒绝后池发生变化")
	}
}

func TestGetOutOfRange(t *testing.T) {
	p, _ := New(2)
	p.Intern(intC(42))
	for _, idx := range []int{-1, -100, 1, 2} {
		if _, err := p.Get(idx); !errors.Is(err, ErrIndexOutOfRange) {
			t.Fatalf("Get(%d) err = %v, want ErrIndexOutOfRange", idx, err)
		}
	}
	c, err := p.Get(0)
	if err != nil || c.Kind != KindInt || c.Int != 42 {
		t.Fatalf("Get(0) = %+v, %v", c, err)
	}
	t.Logf("输入 Get(-1/1/2 等) => ErrIndexOutOfRange；判定依据: 下标 < 0 或 >= 池大小 %d", p.Len())
}

func TestConcurrentIntern(t *testing.T) {
	p, _ := New(128)
	constants := []Const{intC(1), floatC(1.0), strC("one"), floatC(math.Copysign(0, -1))}
	var wg sync.WaitGroup
	results := make([][]int, len(constants))
	for g := range constants {
		results[g] = make([]int, 64)
	}
	for g, c := range constants {
		for rep := 0; rep < 64; rep++ {
			wg.Add(1)
			go func(g, rep int, c Const) {
				defer wg.Done()
				idx, err := p.Intern(c)
				if err != nil {
					t.Errorf("并发 Intern %+v: %v", c, err)
					return
				}
				results[g][rep] = idx
			}(g, rep, c)
		}
	}
	wg.Wait()
	for g, idxs := range results {
		for _, idx := range idxs {
			if idx != idxs[0] {
				t.Fatalf("常量 %v 并发驻留得到多个下标: %v", constants[g], idxs)
			}
		}
	}
	if p.Len() != len(constants) {
		t.Fatalf("并发驻留后大小 = %d, want %d（同一常量只有一个下标）", p.Len(), len(constants))
	}
	// 下标连续无空洞：每个下标都能取值。
	for i := 0; i < p.Len(); i++ {
		if _, err := p.Get(i); err != nil {
			t.Fatalf("下标 %d 取值失败: %v", i, err)
		}
	}
	t.Logf("4 个常量 x 64 个 goroutine 并发驻留 => 池大小 %d, 同一常量结果一致", p.Len())
}

func TestConcurrentMixed(t *testing.T) {
	p, _ := New(64)
	consts := []Const{intC(1), intC(2), floatC(1.0), floatC(2.0), strC("x"), strC("y")}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(3)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = p.Intern(consts[(g+i)%len(consts)])
			}
		}(g)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = p.Merge([]Const{consts[(g+i)%len(consts)], consts[(g+i+1)%len(consts)]})
			}
		}(g)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				n := p.Len()
				if n > 0 {
					if _, err := p.Get((i % n)); err != nil {
						t.Errorf("并发 Get: %v", err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	snap := p.Snapshot()
	if len(snap) != p.Len() {
		t.Fatalf("快照大小 %d != Len %d", len(snap), p.Len())
	}
	seen := map[string]int{}
	for i, c := range snap {
		k, _, err := canonicalize(c)
		if err != nil {
			t.Fatalf("快照含非法常量: %v", err)
		}
		label := fmt.Sprintf("%d:%d:%d:%s", k.kind, k.intV, k.bits, k.strV)
		if prev, ok := seen[label]; ok {
			t.Fatalf("常量 %s 重复出现在下标 %d 与 %d", label, prev, i)
		}
		seen[label] = i
	}
	t.Logf("8x(Intern+Merge+Get) 混合并发后: 池大小 %d, 下标连续且无重复常量", p.Len())
}

// TestReplayAgainstNaive 用随机操作序列重放，逐操作对照真实实现与朴素模拟。
func TestReplayAgainstNaive(t *testing.T) {
	seed := int64(20261001)
	rng := func() int64 {
		// xorshift，保证测试可精确复现。
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return seed
	}
	randN := func(n int) int { return int(uint64(rng()) % uint64(n)) }
	pool := []Const{
		intC(0), intC(1), intC(2),
		floatC(1.0), floatC(math.Copysign(0, -1)), floatC(math.Float64frombits(0x7FF000000000001)),
		strC(""), strC("a"), strC("ab"),
	}

	real, _ := New(6)
	naive := newNaive(6)

	checkErr := func(op string, got, want error) {
		t.Helper()
		if (got == nil) != (want == nil) || (got != nil && got.Error() != want.Error()) {
			t.Fatalf("%s 错误不一致: real=%v naive=%v", op, got, want)
		}
	}

	for step := 0; step < 3000; step++ {
		op := rng() % 3
		switch op {
		case 0:
			c := pool[randN(len(pool))]
			ri, re := real.Intern(c)
			ni, ne := naive.intern(c)
			checkErr(fmt.Sprintf("step %d Intern %v", step, c), re, ne)
			if re == nil && ri != ni {
				t.Fatalf("step %d Intern 下标不一致: real=%d naive=%d", step, ri, ni)
			}
		case 1:
			n := randN(6)
			snap := make([]Const, n)
			for j := range snap {
				snap[j] = pool[randN(len(pool))]
			}
			rr, re := real.Merge(snap)
			nr, ne := naive.merge(snap)
			checkErr(fmt.Sprintf("step %d Merge", step), re, ne)
			if re == nil && fmt.Sprint(rr) != fmt.Sprint(nr) {
				t.Fatalf("step %d Merge 重定位表不一致: real=%v naive=%v", step, rr, nr)
			}
		case 2:
			idx := randN(10) - 2
			_, re := real.Get(idx)
			var ne error
			if idx < 0 || idx >= len(naive.elems) {
				ne = ErrIndexOutOfRange
			}
			checkErr(fmt.Sprintf("step %d Get(%d)", step, idx), re, ne)
		}
		// 每步后两个池的完整内容必须一致，保证“拒绝不改变池”。
		if fmt.Sprint(real.Snapshot()) != fmt.Sprint(naive.elems) {
			t.Fatalf("step %d 后池内容分叉:\nreal =%v\nnaive=%v", step, real.Snapshot(), naive.elems)
		}
	}
	t.Logf("3000 步随机 Intern/Merge/Get 重放与朴素模拟完全一致（下标、重定位表、错误、池内容）")
}
