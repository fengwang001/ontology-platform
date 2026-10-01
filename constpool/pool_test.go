package constpool

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"testing"
)

func mustPool(t *testing.T, capacity int) *Pool {
	t.Helper()
	p, err := NewPool(capacity)
	if err != nil {
		t.Fatalf("NewPool(%d) 失败: %v", capacity, err)
	}
	return p
}

func mustIntern(t *testing.T, p *Pool, c Constant) int {
	t.Helper()
	idx, err := p.Intern(c)
	if err != nil {
		t.Fatalf("Intern(%+v) 失败: %v", c, err)
	}
	return idx
}

// 整数 1 与浮点 1.0 种类不同，是两个常量。
func TestIntern_IntAndFloatAreDistinct(t *testing.T) {
	p := mustPool(t, 4)
	i := mustIntern(t, p, IntConst(1))
	f := mustIntern(t, p, FloatConst(1.0))
	t.Logf("输入: Int(1), Float(1.0); 输出: %d, %d; 判定依据: 种类不同永不相同，下标应不同", i, f)
	if i == f {
		t.Fatalf("整数 1 与浮点 1.0 应为两个常量，却得到同一下标 %d", i)
	}
	if got := p.Len(); got != 2 {
		t.Fatalf("池大小应为 2，实际 %d", got)
	}
}

// +0.0 与 -0.0 位模式不同，是两个常量。
func TestIntern_PositiveAndNegativeZeroAreDistinct(t *testing.T) {
	p := mustPool(t, 4)
	pos := mustIntern(t, p, FloatConst(0.0))
	neg := mustIntern(t, p, FloatConst(math.Copysign(0, -1)))
	t.Logf("输入: +0.0, -0.0; 输出: %d, %d; 判定依据: 浮点按位比较，符号位不同则不同", pos, neg)
	if pos == neg {
		t.Fatalf("+0.0 与 -0.0 应为两个常量，却得到同一下标 %d", pos)
	}
}

// 不同位模式的 NaN 合并为一个常量，且取值返回规范化位模式。
func TestIntern_NaNsAreUnified(t *testing.T) {
	p := mustPool(t, 4)
	nanA := math.Float64frombits(0x7FF8000000000001)
	nanB := math.Float64frombits(0xFFF0000000000001) // 负 NaN，位模式不同
	nanC := math.NaN()
	a := mustIntern(t, p, FloatConst(nanA))
	b := mustIntern(t, p, FloatConst(nanB))
	c := mustIntern(t, p, FloatConst(nanC))
	t.Logf("输入: NaN(bits=%#x), NaN(bits=%#x), NaN(bits=%#x); 输出: %d, %d, %d; 判定依据: 所有 NaN 视为同一常量",
		math.Float64bits(nanA), math.Float64bits(nanB), math.Float64bits(nanC), a, b, c)
	if a != b || b != c {
		t.Fatalf("不同位模式的 NaN 应合并为一个常量，得到下标 %d, %d, %d", a, b, c)
	}
	if got := p.Len(); got != 1 {
		t.Fatalf("池大小应为 1，实际 %d", got)
	}
	got, err := p.Get(a)
	if err != nil {
		t.Fatalf("Get(%d) 失败: %v", a, err)
	}
	if bits := math.Float64bits(got.Float); bits != canonicalNaNBits {
		t.Fatalf("取值应返回规范化 NaN 位模式 %#x，实际 %#x", canonicalNaNBits, bits)
	}
}

// 字符串按字节比较。
func TestIntern_StringByBytes(t *testing.T) {
	p := mustPool(t, 4)
	a := mustIntern(t, p, StringConst("abc"))
	b := mustIntern(t, p, StringConst(string([]byte{0x61, 0x62, 0x63})))
	c := mustIntern(t, p, StringConst("abd"))
	t.Logf("输入: \"abc\", 字节序列 61 62 63, \"abd\"; 输出: %d, %d, %d; 判定依据: 字符串按字节比较", a, b, c)
	if a != b {
		t.Fatalf("字节相同的字符串应命中同一下标，得到 %d 与 %d", a, b)
	}
	if a == c {
		t.Fatalf("字节不同的字符串应为不同常量，却得到同一下标 %d", a)
	}
}

// 满池：已存在的常量仍可命中，新常量被拒绝且池不变。
func TestIntern_FullPool(t *testing.T) {
	p := mustPool(t, 2)
	mustIntern(t, p, IntConst(1))
	mustIntern(t, p, IntConst(2))

	hit, err := p.Intern(IntConst(1))
	t.Logf("满池驻留已存在常量 Int(1): 输出 idx=%d err=%v; 判定依据: 命中已有常量应成功", hit, err)
	if err != nil || hit != 0 {
		t.Fatalf("满池命中已有常量应返回原下标 0，得到 idx=%d err=%v", hit, err)
	}

	before := p.Snapshot()
	idx, err := p.Intern(IntConst(3))
	t.Logf("满池驻留新常量 Int(3): 输出 idx=%d err=%v; 判定依据: 池满且常量为新应拒绝", idx, err)
	if !errors.Is(err, ErrPoolFull) {
		t.Fatalf("应返回 ErrPoolFull，实际 %v", err)
	}
	after := p.Snapshot()
	if len(after) != len(before) {
		t.Fatalf("被拒绝的驻留不得改变池，大小由 %d 变为 %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("被拒绝的驻留不得改变池，下标 %d 由 %+v 变为 %+v", i, before[i], after[i])
		}
	}
}

// 错误口径：构造容量、未知种类、取值越界。
func TestErrors(t *testing.T) {
	if _, err := NewPool(0); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("NewPool(0) 应返回 ErrInvalidCapacity，实际 %v", err)
	}
	if _, err := NewPool(-3); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("NewPool(-3) 应返回 ErrInvalidCapacity，实际 %v", err)
	}

	p := mustPool(t, 2)
	unknown := Constant{Kind: Kind(99), Int: 1}
	if _, err := p.Intern(unknown); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("驻留未知种类应返回 ErrUnknownKind，实际 %v", err)
	}
	if got := p.Len(); got != 0 {
		t.Fatalf("被拒绝的驻留不得改变池，大小应为 0，实际 %d", got)
	}

	mustIntern(t, p, IntConst(7))
	for _, idx := range []int{-1, -100, 1, 2} {
		_, err := p.Get(idx)
		t.Logf("Get(%d)（池大小 1）: err=%v; 判定依据: 下标为负或不小于池大小应拒绝", idx, err)
		if !errors.Is(err, ErrIndexOutOfRange) {
			t.Fatalf("Get(%d) 应返回 ErrIndexOutOfRange，实际 %v", idx, err)
		}
	}
	got, err := p.Get(0)
	if err != nil || got != IntConst(7) {
		t.Fatalf("Get(0) 应返回 Int(7)，得到 %+v err=%v", got, err)
	}
}

// 合并：部分命中部分新增，恰好放下。
func TestMerge_PartialHitExactFit(t *testing.T) {
	p := mustPool(t, 4)
	mustIntern(t, p, IntConst(1)) // 下标 0
	mustIntern(t, p, IntConst(2)) // 下标 1

	snapshot := []Constant{IntConst(2), IntConst(3), IntConst(1), IntConst(4)}
	reloc, err := p.Merge(snapshot)
	t.Logf("输入: 池=[Int(1) Int(2)] C=4, 快照=%v; 输出: reloc=%v err=%v; 判定依据: 2 个新常量恰好放下", snapshot, reloc, err)
	if err != nil {
		t.Fatalf("Merge 失败: %v", err)
	}
	want := []int{1, 2, 0, 3}
	for i := range want {
		if reloc[i] != want[i] {
			t.Fatalf("重定位表应为 %v，实际 %v", want, reloc)
		}
	}
	if got := p.Len(); got != 4 {
		t.Fatalf("合并后池大小应为 4，实际 %d", got)
	}
}

// 合并：超出容量一个则整体拒绝，已有常量也不得新增。
func TestMerge_ExceedsByOneRejected(t *testing.T) {
	p := mustPool(t, 3)
	mustIntern(t, p, IntConst(1))
	mustIntern(t, p, IntConst(2))
	before := p.Snapshot()

	// 快照含 1 个已存在常量与 2 个新常量，新增后大小为 4 > C=3。
	snapshot := []Constant{IntConst(1), IntConst(3), IntConst(4)}
	reloc, err := p.Merge(snapshot)
	t.Logf("输入: 池=[Int(1) Int(2)] C=3, 快照=%v; 输出: reloc=%v err=%v; 判定依据: 2 个新常量超容量 1 个，整体拒绝", snapshot, reloc, err)
	if !errors.Is(err, ErrMergeExceedsCapacity) {
		t.Fatalf("应返回 ErrMergeExceedsCapacity，实际 %v", err)
	}
	if reloc != nil {
		t.Fatalf("被拒绝的合并不应返回重定位表，实际 %v", reloc)
	}
	after := p.Snapshot()
	if len(after) != len(before) {
		t.Fatalf("被拒绝的合并不得改变池，大小由 %d 变为 %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("被拒绝的合并不得改变池，下标 %d 由 %+v 变为 %+v", i, before[i], after[i])
		}
	}
}

// 重定位表：源快照中重复的常量映射到同一个本池下标。
func TestMerge_DuplicateSourceConstants(t *testing.T) {
	p := mustPool(t, 8)
	mustIntern(t, p, StringConst("x")) // 下标 0

	snapshot := []Constant{StringConst("a"), StringConst("x"), StringConst("a"), FloatConst(2.5), StringConst("a")}
	reloc, err := p.Merge(snapshot)
	t.Logf("输入: 池=[Str(x)] C=8, 快照=%v; 输出: reloc=%v err=%v; 判定依据: 重复源常量映射到同一下标", snapshot, reloc, err)
	if err != nil {
		t.Fatalf("Merge 失败: %v", err)
	}
	want := []int{1, 0, 1, 2, 1}
	for i := range want {
		if reloc[i] != want[i] {
			t.Fatalf("重定位表应为 %v，实际 %v", want, reloc)
		}
	}
	if got := p.Len(); got != 3 {
		t.Fatalf("合并后池大小应为 3，实际 %d", got)
	}
}

// 合并错误优先级：快照内含未知种类先于超容量报错。
func TestMerge_UnknownKindTakesPrecedence(t *testing.T) {
	p := mustPool(t, 1) // 容量必然不够
	snapshot := []Constant{IntConst(1), {Kind: Kind(42)}, IntConst(2)}
	_, err := p.Merge(snapshot)
	t.Logf("输入: C=1, 快照含未知种类且超容量; 输出: err=%v; 判定依据: 按序只报第一个（未知种类）", err)
	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("应返回 ErrUnknownKind，实际 %v", err)
	}
	if got := p.Len(); got != 0 {
		t.Fatalf("被拒绝的合并不得改变池，大小应为 0，实际 %d", got)
	}
}

// 快照内的 NaN 也按规范化口径去重。
func TestMerge_NaNNormalized(t *testing.T) {
	p := mustPool(t, 2)
	snapshot := []Constant{
		FloatConst(math.Float64frombits(0x7FF8000000000001)),
		FloatConst(math.Float64frombits(0xFFF8000000000000)),
	}
	reloc, err := p.Merge(snapshot)
	t.Logf("输入: 两个不同位模式的 NaN 快照; 输出: reloc=%v err=%v; 判定依据: NaN 合并为一个常量", reloc, err)
	if err != nil {
		t.Fatalf("Merge 失败: %v", err)
	}
	if reloc[0] != reloc[1] || reloc[0] != 0 {
		t.Fatalf("两个 NaN 应映射到同一下标 0，实际 %v", reloc)
	}
	if got := p.Len(); got != 1 {
		t.Fatalf("合并后池大小应为 1，实际 %d", got)
	}
}

// 候选常量集：覆盖整数/浮点/字符串、±0、多种 NaN 位模式、未知种类。
func candidateConstants() []Constant {
	return []Constant{
		IntConst(1),
		FloatConst(1.0),
		IntConst(-7),
		FloatConst(0.0),
		FloatConst(math.Copysign(0, -1)),
		FloatConst(math.Float64frombits(0x7FF8000000000001)),
		FloatConst(math.Float64frombits(0xFFF8000000000042)),
		FloatConst(math.NaN()),
		FloatConst(2.5),
		StringConst(""),
		StringConst("abc"),
		StringConst("abd"),
		StringConst("常量"),
		{Kind: Kind(99), Int: 1}, // 未知种类
	}
}

type opKind int

const (
	opIntern opKind = iota
	opMerge
	opGet
)

type op struct {
	kind     opKind
	constant Constant
	snapshot []Constant
	index    int
}

func (o op) String() string {
	switch o.kind {
	case opIntern:
		return fmt.Sprintf("Intern(%v)", describeConst(o.constant))
	case opMerge:
		parts := make([]string, len(o.snapshot))
		for i, c := range o.snapshot {
			parts[i] = describeConst(c)
		}
		return fmt.Sprintf("Merge([%s])", joinStrings(parts, " "))
	case opGet:
		return fmt.Sprintf("Get(%d)", o.index)
	}
	return "?"
}

func describeConst(c Constant) string {
	switch c.Kind {
	case KindInt:
		return fmt.Sprintf("Int(%d)", c.Int)
	case KindFloat:
		return fmt.Sprintf("Float(bits=%#x)", math.Float64bits(c.Float))
	case KindString:
		return fmt.Sprintf("Str(%q)", c.Str)
	}
	return fmt.Sprintf("Unknown(kind=%d)", c.Kind)
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, s := range parts {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

func randomOps(r *rand.Rand, n int, candidates []Constant) []op {
	ops := make([]op, 0, n)
	for i := 0; i < n; i++ {
		switch r.Intn(3) {
		case 0:
			ops = append(ops, op{kind: opIntern, constant: candidates[r.Intn(len(candidates))]})
		case 1:
			size := r.Intn(5)
			snapshot := make([]Constant, size)
			for j := range snapshot {
				snapshot[j] = candidates[r.Intn(len(candidates))]
			}
			ops = append(ops, op{kind: opMerge, snapshot: snapshot})
		case 2:
			ops = append(ops, op{kind: opGet, index: r.Intn(12) - 2}) // 覆盖负下标与越界
		}
	}
	return ops
}

// 与朴素模拟对照：同一随机操作序列在 Pool 与 naiveModel 上的每步输出
// （下标、错误、重定位表、取值）必须完全一致。
func TestAgainstNaiveModel(t *testing.T) {
	const capacity = 8
	candidates := candidateConstants()
	for seed := int64(0); seed < 20; seed++ {
		r := rand.New(rand.NewSource(seed))
		ops := randomOps(r, 60, candidates)
		p := mustPool(t, capacity)
		m := newNaiveModel(capacity)
		for step, o := range ops {
			switch o.kind {
			case opIntern:
				gotIdx, gotErr := p.Intern(o.constant)
				wantIdx, wantErr := m.intern(o.constant)
				t.Logf("seed=%d step=%d 输入: %s; 输出: idx=%d err=%v; 模拟: idx=%d err=%v; 判定依据: 两者必须一致",
					seed, step, o, gotIdx, gotErr, wantIdx, wantErr)
				if gotIdx != wantIdx || !errors.Is(gotErr, wantErr) {
					t.Fatalf("seed=%d step=%d %s: Pool=(%d,%v) 模拟=(%d,%v)",
						seed, step, o, gotIdx, gotErr, wantIdx, wantErr)
				}
			case opMerge:
				gotReloc, gotErr := p.Merge(o.snapshot)
				wantReloc, wantErr := m.merge(o.snapshot)
				t.Logf("seed=%d step=%d 输入: %s; 输出: reloc=%v err=%v; 模拟: reloc=%v err=%v; 判定依据: 两者必须一致",
					seed, step, o, gotReloc, gotErr, wantReloc, wantErr)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("seed=%d step=%d %s: Pool err=%v 模拟 err=%v",
						seed, step, o, gotErr, wantErr)
				}
				if gotErr == nil {
					if len(gotReloc) != len(wantReloc) {
						t.Fatalf("seed=%d step=%d %s: 重定位表长度 %d != %d",
							seed, step, o, len(gotReloc), len(wantReloc))
					}
					for i := range gotReloc {
						if gotReloc[i] != wantReloc[i] {
							t.Fatalf("seed=%d step=%d %s: Pool reloc=%v 模拟 reloc=%v",
								seed, step, o, gotReloc, wantReloc)
						}
					}
				}
			case opGet:
				gotC, gotErr := p.Get(o.index)
				wantC, wantErr := m.get(o.index)
				t.Logf("seed=%d step=%d 输入: %s; 输出: const=%v err=%v; 模拟: const=%v err=%v; 判定依据: 两者必须一致",
					seed, step, o, describeConst(gotC), gotErr, describeConst(wantC), wantErr)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("seed=%d step=%d %s: Pool err=%v 模拟 err=%v",
						seed, step, o, gotErr, wantErr)
				}
				if gotErr == nil && !constEqual(gotC, wantC) {
					t.Fatalf("seed=%d step=%d %s: Pool=%v 模拟=%v",
						seed, step, o, describeConst(gotC), describeConst(wantC))
				}
			}
		}
		if p.Len() != len(m.consts) {
			t.Fatalf("seed=%d: 最终池大小 %d != 模拟 %d", seed, p.Len(), len(m.consts))
		}
		t.Logf("seed=%d: %d 步操作全部一致，最终池大小 %d", seed, len(ops), p.Len())
	}
}

// 相同的操作序列重放得到完全相同的下标与重定位表。
func TestReplayDeterminism(t *testing.T) {
	const capacity = 8
	candidates := candidateConstants()
	r := rand.New(rand.NewSource(42))
	ops := randomOps(r, 80, candidates)

	run := func() ([]int, [][]int) {
		p := mustPool(t, capacity)
		indices := make([]int, 0, len(ops))
		relocs := make([][]int, 0, len(ops))
		for _, o := range ops {
			switch o.kind {
			case opIntern:
				idx, _ := p.Intern(o.constant)
				indices = append(indices, idx)
				relocs = append(relocs, nil)
			case opMerge:
				reloc, _ := p.Merge(o.snapshot)
				indices = append(indices, -1)
				relocs = append(relocs, reloc)
			case opGet:
				indices = append(indices, -1)
				relocs = append(relocs, nil)
			}
		}
		return indices, relocs
	}

	idx1, reloc1 := run()
	idx2, reloc2 := run()
	for i := range idx1 {
		if idx1[i] != idx2[i] {
			t.Fatalf("重放下标不一致: 第 %d 步 %d != %d", i, idx1[i], idx2[i])
		}
		r1, r2 := reloc1[i], reloc2[i]
		if len(r1) != len(r2) {
			t.Fatalf("重放重定位表长度不一致: 第 %d 步 %v != %v", i, r1, r2)
		}
		for j := range r1 {
			if r1[j] != r2[j] {
				t.Fatalf("重放重定位表不一致: 第 %d 步 %v != %v", i, r1, r2)
			}
		}
	}
	t.Logf("输入: %d 步固定操作序列重放两次; 输出: 两次下标与重定位表完全相同; 判定依据: 确定性重放", len(ops))
}

// 并发：同一常量并发驻留只得到一个下标；下标连续无空洞；池内常量
// 互不相同；结果等价于某个串行顺序。
func TestConcurrentIntern(t *testing.T) {
	constants := []Constant{
		IntConst(1), FloatConst(1.0), FloatConst(0.0),
		FloatConst(math.Copysign(0, -1)),
		FloatConst(math.Float64frombits(0x7FF8000000000001)),
		FloatConst(math.Float64frombits(0xFFF8000000000002)),
		StringConst("a"), StringConst("b"),
	}
	p := mustPool(t, len(constants))

	const goroutines = 16
	const distinct = 7 // 两个不同位模式的 NaN 合并为一个常量
	results := make([][]int, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			results[g] = make([]int, len(constants))
			for i, c := range constants {
				idx, err := p.Intern(c)
				if err != nil {
					t.Errorf("并发 Intern(%v) 失败: %v", describeConst(c), err)
					return
				}
				results[g][i] = idx
			}
		}(g)
	}
	wg.Wait()

	// 同一常量的所有并发驻留必须得到同一下标。
	for i, c := range constants {
		for g := 1; g < goroutines; g++ {
			if results[g][i] != results[0][i] {
				t.Fatalf("常量 %v 并发驻留得到多个下标: %d 与 %d",
					describeConst(c), results[0][i], results[g][i])
			}
		}
	}
	t.Logf("输入: %d 个 goroutine 各驻留 %d 个常量; 输出: 每个常量唯一下标 %v; 判定依据: 同一常量并发驻留只得到一个下标",
		goroutines, len(constants), results[0])

	// 下标连续无空洞，且池内常量互不相同。
	if got := p.Len(); got != distinct {
		t.Fatalf("池大小应为 %d，实际 %d", distinct, got)
	}
	seen := make(map[int]bool)
	for _, idx := range results[0] {
		if idx < 0 || idx >= distinct {
			t.Fatalf("下标 %d 越界（结果 %v）", idx, results[0])
		}
		seen[idx] = true
	}
	// 下标连续无空洞：出现的下标恰好覆盖 0..distinct-1。
	if len(seen) != distinct {
		t.Fatalf("下标应连续覆盖 0..%d，实际覆盖 %v", distinct-1, seen)
	}
	snapshot := p.Snapshot()
	for i := 0; i < len(snapshot); i++ {
		for j := i + 1; j < len(snapshot); j++ {
			if constEqual(snapshot[i], snapshot[j]) {
				t.Fatalf("池内下标 %d 与 %d 的常量相同: %v", i, j, describeConst(snapshot[i]))
			}
		}
	}
	// 等价于某个串行顺序：每个常量驻留返回的下标处正是该常量（规范化后）。
	for i, c := range constants {
		got, err := p.Get(results[0][i])
		if err != nil {
			t.Fatalf("Get(%d) 失败: %v", results[0][i], err)
		}
		if !constEqual(got, c) {
			t.Fatalf("下标 %d 处为 %v，应为 %v", results[0][i], describeConst(got), describeConst(c))
		}
	}
}

// 并发混合驻留、合并与取值：验证无数据竞争且最终状态满足不变量。
func TestConcurrentMixedOps(t *testing.T) {
	candidates := candidateConstants()[:13] // 排除未知种类
	p := mustPool(t, 64)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				switch r.Intn(3) {
				case 0:
					_, _ = p.Intern(candidates[r.Intn(len(candidates))])
				case 1:
					size := r.Intn(4)
					snapshot := make([]Constant, size)
					for j := range snapshot {
						snapshot[j] = candidates[r.Intn(len(candidates))]
					}
					_, _ = p.Merge(snapshot)
				case 2:
					_, _ = p.Get(r.Intn(20) - 2)
				}
			}
		}(g)
	}
	wg.Wait()

	// 不变量：下标连续无空洞、池内常量互不相同、取值与驻留一致。
	snapshot := p.Snapshot()
	for i, c := range snapshot {
		got, err := p.Get(i)
		if err != nil || !constEqual(got, c) {
			t.Fatalf("Get(%d) = %v, %v；快照为 %v", i, describeConst(got), err, describeConst(c))
		}
		for j := i + 1; j < len(snapshot); j++ {
			if constEqual(snapshot[i], snapshot[j]) {
				t.Fatalf("池内下标 %d 与 %d 的常量相同", i, j)
			}
		}
	}
	t.Logf("输入: 8 个 goroutine 混合驻留/合并/取值各 200 步; 输出: 最终池大小 %d; 判定依据: 下标连续无空洞且常量互不相同", len(snapshot))
}
