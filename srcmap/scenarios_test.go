package srcmap

import (
	"fmt"
	"testing"
)

func mapped(start, src, line, col int) Segment {
	return Segment{Start: start, SourceIndex: src, OrigLine: line, OrigCol: col}
}

func unmapped(start int) Segment { return Segment{Start: start, Unmapped: true} }

func mustNew(t testing.TB, sourceCount int, lines []Line) *Mapping {
	t.Helper()
	m, err := New(sourceCount, lines)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	return m
}

func stepwiseLookup(m2, m1 *Mapping, line, col int) (LookupResult, error) {
	r2, err := m2.Lookup(line, col)
	if err != nil || !r2.Mapped {
		return LookupResult{Mapped: false}, err
	}
	r1, err := m1.Lookup(r2.Position.Line, r2.Position.Column)
	if err != nil {
		return LookupResult{}, err
	}
	if !r1.Mapped {
		return LookupResult{Mapped: false}, nil
	}
	return r1, nil
}

// TestSplitAtM1Boundary：段内偏移跨过 M1 段边界时必须切开。
func TestSplitAtM1Boundary(t *testing.T) {
	m2 := mustNew(t, 1, []Line{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 7, 100),
	}}})
	m1 := mustNew(t, 1, []Line{{GeneratedLine: 7, Segments: []Segment{
		mapped(100, 0, 3, 0),
		mapped(105, 0, 4, 0),
	}}})
	fmt.Printf("[输入] M2=%v M1=%v\n", m2.lines, m1.lines)
	got, err := Compose(m2, m1)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := []Line{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 3, 0),
		mapped(5, 0, 4, 0),
	}}}
	fmt.Printf("[输出] 合成=%v\n[判定] 期望=%v 相等=%v\n", got.lines, want, fmt.Sprint(got.lines) == fmt.Sprint(want))
	if fmt.Sprint(got.lines) != fmt.Sprint(want) {
		t.Fatalf("边界切开错误")
	}
}

// TestLastSegmentMultipleSplits：末段向行尾延伸时的多次切开。
func TestLastSegmentMultipleSplits(t *testing.T) {
	m2 := mustNew(t, 1, []Line{{GeneratedLine: 2, Segments: []Segment{
		mapped(0, 0, 0, 0),
	}}})
	m1 := mustNew(t, 1, []Line{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 9, 0),
		mapped(3, 0, 9, 30),
		unmapped(6),
		mapped(8, 0, 9, 80),
		unmapped(13),
	}}})
	got, err := Compose(m2, m1)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := []Line{{GeneratedLine: 2, Segments: []Segment{
		mapped(0, 0, 9, 0),
		mapped(3, 0, 9, 30),
		unmapped(6),
		mapped(8, 0, 9, 80),
		unmapped(13),
	}}}
	fmt.Printf("[输出] 末段多次切开=%v\n[判定] 期望=%v 相等=%v\n", got.lines, want, fmt.Sprint(got.lines) == fmt.Sprint(want))
	if fmt.Sprint(got.lines) != fmt.Sprint(want) {
		t.Fatalf("末段多次切开错误")
	}
	for c := 0; c <= 12; c++ {
		step, _ := stepwiseLookup(m2, m1, 2, c)
		direct, err := got.Lookup(2, c)
		if err != nil {
			t.Fatalf("Lookup(%d): %v", c, err)
		}
		if step != direct {
			t.Fatalf("列 %d: 逐步=%v 直接=%v", c, step, direct)
		}
	}
	fmt.Printf("[判定] 列 0..12 逐步查询与合成映射逐点一致\n")
}

// TestMiddleSegmentLimit：M2 中间段切开不得越过下一段起点。
func TestMiddleSegmentLimit(t *testing.T) {
	m2 := mustNew(t, 1, []Line{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 0, 0),
		mapped(4, 0, 1, 0),
	}}})
	m1 := mustNew(t, 1, []Line{
		{GeneratedLine: 0, Segments: []Segment{
			mapped(0, 0, 5, 0),
			mapped(3, 0, 6, 0),
			mapped(10, 0, 7, 0),
		}},
		{GeneratedLine: 1, Segments: []Segment{mapped(0, 0, 8, 0)}},
	})
	got, err := Compose(m2, m1)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := []Line{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 5, 0),
		mapped(3, 0, 6, 0),
		mapped(4, 0, 8, 0),
	}}}
	fmt.Printf("[输出] 中段限界=%v\n[判定] 期望=%v 相等=%v\n", got.lines, want, fmt.Sprint(got.lines) == fmt.Sprint(want))
	if fmt.Sprint(got.lines) != fmt.Sprint(want) {
		t.Fatalf("中间段越界")
	}
}

// TestM1UnmappedBecomesExplicitUnmapped。
func TestM1UnmappedBecomesExplicitUnmapped(t *testing.T) {
	m2 := mustNew(t, 1, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, 0)}}})
	m1 := mustNew(t, 1, []Line{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 1, 0), unmapped(3), mapped(5, 0, 1, 50), unmapped(10),
	}}})
	got, err := Compose(m2, m1)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := []Line{{GeneratedLine: 0, Segments: []Segment{
		mapped(0, 0, 1, 0), unmapped(3), mapped(5, 0, 1, 50), unmapped(10),
	}}}
	fmt.Printf("[输出] 显式未映射=%v\n[判定] 期望=%v 相等=%v\n", got.lines, want, fmt.Sprint(got.lines) == fmt.Sprint(want))
	if fmt.Sprint(got.lines) != fmt.Sprint(want) {
		t.Fatalf("显式未映射错误")
	}
}

// TestInfiniteLastSegmentOK：末段若在可表示域内永不溢出，则合成成功，
// 且其表示向行尾无限延伸（没有封口段）。
func TestInfiniteLastSegmentOK(t *testing.T) {
	// M2 最终列 0 → 中间列 0；M1 中间列 0 → 原始列 0。
	// 斜率 1 时最终列 1e9 恰好原始列 1e9，合法。
	m2 := mustNew(t, 1, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, 0)}}})
	m1 := mustNew(t, 1, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, 0)}}})
	got, err := Compose(m2, m1)
	if err != nil {
		t.Fatalf("合法无限末段不应报错: %v", err)
	}
	r, err := got.Lookup(0, MaxCoord)
	if err != nil || !r.Mapped || r.Position.Column != MaxCoord {
		t.Fatalf("行尾点应为原始列 1e9: %v %v", r, err)
	}
	fmt.Printf("[判定] 合法无限末段合成成功，行尾点 (0,1e9) → %v\n", r.Position)
}

// TestEqualityHitAndNoCrossLine：取等命中、无段未映射、不跨行、段内偏移。
func TestEqualityHitAndNoCrossLine(t *testing.T) {
	m := mustNew(t, 1, []Line{
		{GeneratedLine: 1, Segments: []Segment{mapped(5, 0, 0, 10)}},
	})
	r, err := m.Lookup(1, 5)
	if err != nil || !r.Mapped || r.Position != (Position{0, 0, 10}) {
		t.Fatalf("取等命中失败: %v %v", r, err)
	}
	if r, _ = m.Lookup(1, 4); r.Mapped {
		t.Fatalf("无匹配段应未映射")
	}
	if r, _ = m.Lookup(0, 9); r.Mapped {
		t.Fatalf("不得跨行回溯")
	}
	if r, _ = m.Lookup(1, 7); !r.Mapped || r.Position.Column != 12 {
		t.Fatalf("段内偏移错误: %v", r)
	}
	fmt.Printf("[判定] 取等命中、无段未映射、不跨行回溯、段内偏移正确\n")
}

// TestCanonicalRules：行首省略、连续未映射合并、延伸合并、空行省略。
func TestCanonicalRules(t *testing.T) {
	m, err := New(1, []Line{{GeneratedLine: 0, Segments: []Segment{
		unmapped(0),
		mapped(2, 0, 0, 0),
		mapped(3, 0, 0, 1),
		mapped(10, 0, 0, 8),
		unmapped(20),
		unmapped(25),
		mapped(30, 0, 0, 100),
	}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := []Line{{GeneratedLine: 0, Segments: []Segment{
		mapped(2, 0, 0, 0), unmapped(20), mapped(30, 0, 0, 100),
	}}}
	fmt.Printf("[输出] 规范化=%v\n[判定] 期望=%v 相等=%v\n", m.lines, want, fmt.Sprint(m.lines) == fmt.Sprint(want))
	if fmt.Sprint(m.lines) != fmt.Sprint(want) {
		t.Fatalf("规范化规则错误")
	}
	m2, err := New(1, []Line{
		{GeneratedLine: 0, Segments: []Segment{unmapped(0), unmapped(5)}},
		{GeneratedLine: 1, Segments: []Segment{mapped(0, 0, 0, 0)}},
	})
	if err != nil || len(m2.lines) != 1 {
		t.Fatalf("空行应省略: %v %v", m2, err)
	}
	fmt.Printf("[判定] 全未映射行已省略\n")
}

// TestCanonicalEquivalence：不同构造的等价映射规范形式逐字相同。
func TestCanonicalEquivalence(t *testing.T) {
	a := mustNew(t, 1, []Line{{GeneratedLine: 3, Segments: []Segment{
		mapped(0, 0, 2, 10), mapped(1, 0, 2, 11), mapped(4, 0, 2, 14),
	}}})
	b := mustNew(t, 1, []Line{{GeneratedLine: 3, Segments: []Segment{
		mapped(0, 0, 2, 10), mapped(2, 0, 2, 12),
	}}})
	eq := fmt.Sprint(a.lines) == fmt.Sprint(b.lines)
	fmt.Printf("[输入] A=%v B=%v\n[输出] 规范 A=%v 规范 B=%v\n[判定] 相等=%v\n", a.lines, b.lines, a.lines, b.lines, eq)
	if !eq {
		t.Fatalf("等价映射规范形式不一致")
	}
}
