package srcmap

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

const gridN = 24

func stateOf(r LookupResult) string {
	if !r.Mapped {
		return "u"
	}
	return fmt.Sprintf("%d:%d:%d", r.Position.SourceIndex, r.Position.Line, r.Position.Column)
}

// naiveCompose 在密网格 0..gridN 上逐列逐步查询，重建规范结果。
func naiveCompose(t *testing.T, m2, m1 *Mapping, finalLines []int) []Line {
	t.Helper()
	out := []Line{}
	for _, fl := range finalLines {
		var raw []Segment
		prev := ""
		for c := 0; c <= gridN; c++ {
			r, err := stepwiseLookup(m2, m1, fl, c)
			if err != nil {
				t.Fatalf("逐步查询 (%d,%d): %v", fl, c, err)
			}
			st := stateOf(r)
			if c == 0 || st != prev {
				if r.Mapped {
					raw = append(raw, mapped(c, r.Position.SourceIndex, r.Position.Line, r.Position.Column))
				} else {
					raw = append(raw, unmapped(c))
				}
			}
			prev = st
		}
		if canon := canonicalizeLine(raw); len(canon) > 0 {
			out = append(out, Line{GeneratedLine: fl, Segments: canon})
		}
	}
	return out
}

// randomFullLines 生成每行从列 0 起全覆盖的随机段（斜率 1）。
func randomFullLines(t *testing.T, rng *rand.Rand, srcCount int, origMax int) []Line {
	t.Helper()
	var lines []Line
	for ln := 0; ln < 4; ln++ {
		starts := map[int]bool{0: true}
		for i := 0; i < rng.Intn(6); i++ {
			starts[rng.Intn(gridN+1)] = true
		}
		var cols []int
		for c := range starts {
			cols = append(cols, c)
		}
		sort.Ints(cols)
		segs := make([]Segment, 0, len(cols))
		for _, c := range cols {
			segs = append(segs, mapped(c, rng.Intn(srcCount), rng.Intn(3), rng.Intn(origMax+1)))
		}
		// 在网格外用显式未映射段封口，使语义只在 0..gridN 上比较，
		// 末段也不会向 1e9 无限延伸而产生与网格无关的位置溢出。
		segs = append(segs, unmapped(gridN+1))
		lines = append(lines, Line{GeneratedLine: ln, Segments: segs})
	}
	return lines
}

// TestRandomDifferential：300 组随机映射，逐列暴力枚举对照。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(1605))
	for iter := 0; iter < 300; iter++ {
		srcCount := 1 + rng.Intn(2)
		m1 := mustNew(t, srcCount, randomFullLines(t, rng, srcCount, gridN))
		m2Lines := randomFullLines(t, rng, 1, 12)
		// M2 的中间行限制在 M1 覆盖的 0..3。
		for li := range m2Lines {
			for si := range m2Lines[li].Segments {
				m2Lines[li].Segments[si].OrigLine = rng.Intn(4)
				m2Lines[li].Segments[si].SourceIndex = 0
			}
		}
		m2 := mustNew(t, 1, m2Lines)
		got, err := Compose(m2, m1)
		if err != nil {
			t.Fatalf("iter %d Compose: %v\nM1=%v\nM2=%v", iter, err, m1.lines, m2.lines)
		}
		var finalLines []int
		for ln := 0; ln < 4; ln++ {
			finalLines = append(finalLines, ln)
		}
		want := naiveCompose(t, m2, m1, finalLines)
		gotGrid := projectToGrid(got, finalLines)
		if iter < 3 {
			fmt.Printf("[输入] iter=%d M1=%v M2=%v\n[输出] 合成=%v\n[判定] 与朴素模型相等=%v\n",
				iter, m1.lines, m2.lines, got.lines, fmt.Sprint(gotGrid) == fmt.Sprint(want))
		}
		if fmt.Sprint(gotGrid) != fmt.Sprint(want) {
			t.Fatalf("iter %d 与朴素模型不一致\n got=%v\nwant=%v\nM1=%v\nM2=%v", iter, gotGrid, want, m1.lines, m2.lines)
		}
	}
	fmt.Printf("[判定] 300 组随机映射全部与逐列朴素模型一致\n")
}

// randomSparseLines 生成含未映射缺口、且某些列/行可缺失的稀疏映射。
func randomSparseLines(t *testing.T, rng *rand.Rand, srcCount, lines int) []Line {
	return randomSparseLinesForceSrc(t, rng, srcCount, lines, -1)
}

func randomSparseLinesForceSrc(t *testing.T, rng *rand.Rand, srcCount, lines, forceSrc int) []Line {
	t.Helper()
	var out []Line
	for ln := 0; ln < lines; ln++ {
		if rng.Intn(5) == 0 {
			continue // 整行缺失
		}
		starts := map[int]bool{}
		for i := 0; i < rng.Intn(7); i++ {
			starts[rng.Intn(gridN+1)] = true
		}
		var cols []int
		for c := range starts {
			cols = append(cols, c)
		}
		sort.Ints(cols)
		segs := make([]Segment, 0, len(cols)+1)
		for _, c := range cols {
			if rng.Intn(3) == 0 {
				segs = append(segs, unmapped(c))
			} else {
				src := rng.Intn(srcCount)
				if forceSrc >= 0 {
					src = forceSrc
				}
				segs = append(segs, mapped(c, src, rng.Intn(3), rng.Intn(gridN+1)))
			}
		}
		segs = append(segs, unmapped(gridN+1))
		out = append(out, Line{GeneratedLine: ln, Segments: segs})
	}
	return out
}

// TestRandomSparseDifferential：含未映射缺口与缺失行的随机对照。
func TestRandomSparseDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(2026))
	for iter := 0; iter < 200; iter++ {
		m1Src := 1 + rng.Intn(2)
		m1 := mustNew(t, m1Src, randomSparseLines(t, rng, m1Src, 4))
		m2raw := randomSparseLinesForceSrc(t, rng, 1, 4, 0)
		for li := range m2raw {
			for si := range m2raw[li].Segments {
				s := &m2raw[li].Segments[si]
				if !s.Unmapped {
					s.SourceIndex = 0
					s.OrigLine = rng.Intn(4)
					s.OrigCol = rng.Intn(gridN + 1)
				}
			}
		}
		m2 := mustNew(t, 1, m2raw)
		got, err := Compose(m2, m1)
		if err != nil {
			t.Fatalf("iter %d Compose: %v\nM1=%v\nM2=%v", iter, err, m1.lines, m2.lines)
		}
		var fls []int
		for ln := 0; ln < 4; ln++ {
			fls = append(fls, ln)
		}
		want := naiveCompose(t, m2, m1, fls)
		gotGrid := projectToGrid(got, fls)
		if iter < 2 {
			fmt.Printf("[输入] 稀疏 iter=%d M1=%v M2=%v\n[判定] 与朴素模型相等=%v\n",
				iter, m1.lines, m2.lines, fmt.Sprint(gotGrid) == fmt.Sprint(want))
		}
		if fmt.Sprint(gotGrid) != fmt.Sprint(want) {
			t.Fatalf("稀疏 iter %d 不一致\n got=%v\nwant=%v\nM1=%v\nM2=%v", iter, gotGrid, want, m1.lines, m2.lines)
		}
	}
	fmt.Printf("[判定] 200 组含未映射缺口/缺失行的随机映射与朴素模型一致\n")
}

// projectToGrid 把合成结果在网格 0..gridN 上逐点查询后重建，保证只比较网格语义，
// 不受“末段向 1e9 无限延伸”这一表示差异影响。
func projectToGrid(m *Mapping, finalLines []int) []Line {
	out := []Line{}
	for _, fl := range finalLines {
		var raw []Segment
		prev := ""
		for c := 0; c <= gridN; c++ {
			r, _ := m.Lookup(fl, c)
			st := stateOf(r)
			if c == 0 || st != prev {
				if r.Mapped {
					raw = append(raw, mapped(c, r.Position.SourceIndex, r.Position.Line, r.Position.Column))
				} else {
					raw = append(raw, unmapped(c))
				}
			}
			prev = st
		}
		if canon := canonicalizeLine(raw); len(canon) > 0 {
			out = append(out, Line{GeneratedLine: fl, Segments: canon})
		}
	}
	return out
}

// TestAssociativity：(M3∘M2)∘M1 与 M3∘(M2∘M1) 在所有网格点上一致。
func TestAssociativity(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	for iter := 0; iter < 50; iter++ {
		m1 := mustNew(t, 2, randomFullLines(t, rng, 2, gridN))
		m2Lines := randomFullLines(t, rng, 1, gridN)
		m3Lines := randomFullLines(t, rng, 1, gridN)
		for _, ls := range [][]Line{m2Lines, m3Lines} {
			for li := range ls {
				for si := range ls[li].Segments {
					ls[li].Segments[si].OrigLine = rng.Intn(4)
					ls[li].Segments[si].SourceIndex = 0
				}
			}
		}
		m2 := mustNew(t, 1, m2Lines)
		m3 := mustNew(t, 1, m3Lines)
		left, err := Compose(ComposeOrDie(m3, m2), m1)
		if err != nil {
			t.Fatalf("iter %d 左结合: %v", iter, err)
		}
		mid, err := Compose(m2, m1)
		if err != nil {
			t.Fatalf("iter %d M2∘M1: %v", iter, err)
		}
		right, err := Compose(m3, mid)
		if err != nil {
			t.Fatalf("iter %d 右结合: %v", iter, err)
		}
		var fls []int
		for ln := 0; ln < 4; ln++ {
			fls = append(fls, ln)
		}
		a := projectToGrid(left, fls)
		b := projectToGrid(right, fls)
		if iter == 0 {
			fmt.Printf("[输入] M1=%v M2=%v M3=%v\n[输出] 左结合=%v 右结合=%v\n[判定] 网格语义相等=%v\n",
				m1.lines, m2.lines, m3.lines, a, b, fmt.Sprint(a) == fmt.Sprint(b))
		}
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Fatalf("iter %d 结合性失败\nleft=%v\nright=%v", iter, a, b)
		}
	}
	fmt.Printf("[判定] 50 组随机三映射连续合成满足结合性\n")
}

func ComposeOrDie(a, b *Mapping) *Mapping {
	m, err := Compose(a, b)
	if err != nil {
		panic(err)
	}
	return m
}

// TestErrorsAndService：错误类别、优先级、不可变性与并发。
func TestErrorsAndService(t *testing.T) {
	// 参数非法分类。
	if _, err := New(1, []Line{{GeneratedLine: 1, Segments: []Segment{{Start: -1}}}}); CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("应参数非法: %v", err)
	}
	if _, err := New(1, []Line{
		{GeneratedLine: 2, Segments: []Segment{mapped(0, 0, 0, 0)}},
		{GeneratedLine: 2, Segments: []Segment{mapped(0, 0, 0, 0)}},
	}); CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("行号未递增应参数非法: %v", err)
	}
	if _, err := New(1, []Line{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 0, 0), mapped(0, 0, 0, 1),
	}}}); CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("列未严格递增应参数非法: %v", err)
	}
	if _, err := New(1, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 5, 0, 0)}}}); CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("源索引越界应参数非法: %v", err)
	}

	svc := NewService()
	// 位置溢出优先于名字问题之外：查询越界是参数非法。
	if _, err := svc.Query("nope", 0, MaxCoord+1); CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("查询越界应参数非法: %v", err)
	}
	// 未找到。
	if _, err := svc.Query("nope", 0, 0); CategoryOf(err) != CategoryNotFound {
		t.Fatalf("应未找到: %v", err)
	}
	if err := svc.Compose("x", "y", "z"); CategoryOf(err) != CategoryNotFound {
		t.Fatalf("合成缺名应未找到: %v", err)
	}

	if err := svc.Register("a", 1, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, 0)}}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := svc.Register("a", 1, nil); CategoryOf(err) != CategoryDuplicate {
		t.Fatalf("重名应重复: %v", err)
	}
	// 合成结果名已占用优先于其他后续问题。
	if err := svc.Compose("a", "a", "a"); CategoryOf(err) != CategoryDuplicate {
		t.Fatalf("结果名占用应重复: %v", err)
	}

	// M2 源数量不为一 → 参数非法。
	if err := svc.Register("two", 2, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, 0)}}}); err != nil {
		t.Fatalf("register two: %v", err)
	}
	if err := svc.Compose("two", "a", "out1"); CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("M2 多源应参数非法: %v", err)
	}

	// 位置溢出：M2 最终列 0 → 中间列 0（斜率 1）。
	// M1 第一段覆盖中间列 0..1e9-1；在中间列 1e9 处开始第二段，
	// 把原始列重置为 1e9。最终列 1e9 命中第二段 → 原始列 1e9 合法，
	// 最终列 1e9+1 越界不可查。要使“可表示位置”本身溢出，
	// 令第二段起点原始列 = 1e9 且其在中间列 1e9 之后仍斜率 1：
	// 可表示的最终位置只到 1e9，故可复现的溢出来自段起点本身 >1e9——
	// 例如 M1 在中间列 5 的段把原始列起点设为 1e9，而 M2 让最终列 5
	// 对应中间列 5：最终列 5 原始列 1e9 合法，最终列 6 原始列 1e9+1
	// 仍可表示（中间列 6 合法）→ 必须报错。
	m2ov := mustNew(t, 1, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, 0)}}})
	m1ov := mustNew(t, 1, []Line{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 0, 0),
		mapped(5, 0, 0, MaxCoord), // 最终列6 → 原始列 1e9+1
	}}})
	if _, err := Compose(m2ov, m1ov); CategoryOf(err) != CategoryPositionOverflow {
		t.Fatalf("应位置溢出: %v", err)
	}
	// 参数非法优先于位置溢出：用一个会溢出的 M1，但 M2 源数量不为一。
	m2multi := mustNew(t, 2, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(5, 0, 0, 5)}}})
	if _, err := Compose(m2multi, m1ov); CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("M2 多源时应先报参数非法（优先于位置溢出）: %v", err)
	}
	fmt.Printf("[判定] 错误分类、优先级、M2 单源约束、位置溢出均正确\n")

	// 并发：多 goroutine 同时登记/合成/查询，结果必须等价于某一串行顺序。
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			name := fmt.Sprintf("p%d", g)
			err := svc.Register(name, 1, []Line{{GeneratedLine: 0, Segments: []Segment{
				mapped(0, 0, 0, g%5), unmapped(10),
			}}})
			if err != nil {
				t.Errorf("并发登记 %s: %v", name, err)
				return
			}
			if err := svc.Compose(name, "a", name+"_c"); err != nil {
				t.Errorf("并发合成: %v", err)
			}
			if _, err := svc.Query(name+"_c", 0, 3); err != nil {
				t.Errorf("并发查询: %v", err)
			}
		}(g)
	}
	wg.Wait()
	fmt.Printf("[判定] 16 goroutine 并发登记/合成/查询全部成功\n")
}
