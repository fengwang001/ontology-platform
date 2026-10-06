package docsync

import (
	"errors"
	"testing"
)

func diagOff(s *Service, line, sc, ec int) Diagnostic {
	return Diagnostic{
		Range:    Range{Start: Position{line, sc}, End: Position{line, ec}},
		Severity: SeverityError,
		Message:  "d",
	}
}

func activeOffsets(snap Snapshot) [][2]int {
	out := make([][2]int, 0, len(snap.Diagnostics))
	for _, d := range snap.Diagnostics {
		out = append(out, [2]int{d.Range.Start.Character, d.Range.End.Character})
	}
	return out
}

func reg(t *testing.T, s *Service, ver, sc, ec int) int {
	t.Helper()
	seq, err := s.Register(ver, diagOff(s, 0, sc, ec))
	if err != nil {
		t.Fatalf("register [%d,%d): %v", sc, ec, err)
	}
	return seq
}

// TestInsertFourRelations 覆盖插入点与诊断端点的四种位置关系：
// s<a（诊断在插入之后）、s==a、a<s<b（内部）、s==b（非空诊断终点）、s>b。
func TestInsertFourRelations(t *testing.T) {
	// 文档 "abcdef"（单行，列 0..6）
	// 诊断： [0,2) "ab"、[2,4) "cd"、[1,4) "bcd"、[4,6) "ef"、空诊断 (3,3)、(4,4)
	s := NewService("abcdef")
	d1 := reg(t, s, 0, 0, 2)
	d2 := reg(t, s, 0, 2, 4)
	d3 := reg(t, s, 0, 1, 4)
	d4 := reg(t, s, 0, 4, 6)
	empty3 := reg(t, s, 0, 3, 3)
	empty4 := reg(t, s, 0, 4, 4)

	// 在列 3 插入 "XY"（长度 2）
	r := s.Change(0, []Edit{{Range: pointRange(0, 3), Text: "XY"}})
	if !r.Accepted {
		t.Fatal(r.Reason)
	}
	snap := s.Snapshot()
	want := map[int][2]int{
		d1:     {0, 2}, // s>b：不变
		d2:     {2, 6}, // a<s<b：终点后移，起点不变
		d3:     {1, 6}, // 内部：终点后移
		d4:     {6, 8}, // s==a 不可能（起点 4>3）；s<a：整体后移
		empty3: {5, 5}, // 空诊断 a=b=3==s：整体后移
		empty4: {6, 6}, // s<b(4)：整体平移
	}
	got := map[int][2]int{}
	for _, d := range snap.Diagnostics {
		got[d.Sequence] = [2]int{d.Range.Start.Character, d.Range.End.Character}
	}
	for seq, w := range want {
		if got[seq] != w {
			t.Errorf("diag seq %d want %v got %v", seq, w, got[seq])
		}
	}
	if s.Text() != "abcXYdef" {
		t.Fatalf("text=%q", s.Text())
	}
}

// TestInsertAtStartEnd 覆盖 s==a 整体后移与 s==b 非空诊断不变。
func TestInsertAtStartEnd(t *testing.T) {
	s := NewService("abcdef")
	dA := reg(t, s, 0, 2, 5) // "cde"
	// s==a：整体后移，不包含新文本
	if r := s.Change(0, []Edit{{Range: pointRange(0, 2), Text: "X"}}); !r.Accepted {
		t.Fatal(r.Reason)
	}
	snap := s.Snapshot()
	if o := activeOffsets(snap); o[0] != [2]int{3, 6} {
		t.Fatalf("s==a: got %v want [3,6]", o)
	}
	if s.Text() != "abXcdef" {
		t.Fatalf("text=%q", s.Text())
	}

	// s==b（在诊断终点插入）：诊断不变
	s = NewService("abcdef")
	dA = reg(t, s, 0, 2, 5)
	if r := s.Change(0, []Edit{{Range: pointRange(0, 5), Text: "X"}}); !r.Accepted {
		t.Fatal(r.Reason)
	}
	snap = s.Snapshot()
	if o := activeOffsets(snap); o[0] != [2]int{2, 5} {
		t.Fatalf("s==b: got %v want [2,5]", o)
	}
	_ = dA
}

// TestSamePointMultipleInserts 同点多插入：起点全部生效、空诊断整体后移到整组之后。
func TestSamePointMultipleInserts(t *testing.T) {
	s := NewService("abcdef")
	// 诊断 [3,3) 空、[3,5) 非空（起点在插入点）
	empty := reg(t, s, 0, 3, 3)
	nonEmpty := reg(t, s, 0, 3, 5)
	before := reg(t, s, 0, 1, 2) // 完全在插入点之前
	r := s.Change(0, []Edit{
		{Range: pointRange(0, 3), Text: "X"},
		{Range: pointRange(0, 3), Text: "Y"},
	})
	if !r.Accepted {
		t.Fatal(r.Reason)
	}
	snap := s.Snapshot()
	want := map[int][2]int{empty: {5, 5}, nonEmpty: {5, 7}, before: {1, 2}}
	for _, d := range snap.Diagnostics {
		if w := want[d.Sequence]; w != [2]int{d.Range.Start.Character, d.Range.End.Character} {
			t.Errorf("seq %d want %v", d.Sequence, w)
		}
	}
	if s.Text() != "abcXYdef" {
		t.Fatalf("text=%q", s.Text())
	}
}

// TestInvalidationBoundaries 失效边界：相接不失效，重叠一个码元即失效，空诊断严格内部。
func TestInvalidationBoundaries(t *testing.T) {
	s := NewService("abcdefgh")
	touchAtEnd := reg(t, s, 0, 0, 3)   // [0,3)，删除 [3,6)：b==s 相接，存活
	touchAtStart := reg(t, s, 0, 6, 8) // [6,8)，删除 [3,6)：a==e 相接，存活
	overlapOne := reg(t, s, 0, 2, 4)   // 与 [3,6) 重叠一码元，失效
	inside := reg(t, s, 0, 4, 5)       // 被包含，失效
	emptyStrict := reg(t, s, 0, 5, 5)  // 空诊断严格在 (3,6) 内，失效
	emptyEdgeS := reg(t, s, 0, 3, 3)   // 空诊断在 s，存活
	emptyEdgeE := reg(t, s, 0, 6, 6)   // 空诊断在 e，存活
	r := s.Change(0, []Edit{{
		Range: Range{Start: Position{0, 3}, End: Position{0, 6}}, Text: "",
	}})
	if !r.Accepted {
		t.Fatal(r.Reason)
	}
	snap := s.Snapshot()
	alive := map[int]bool{}
	for _, d := range snap.Diagnostics {
		alive[d.Sequence] = true
	}
	for _, seq := range []int{overlapOne, inside, emptyStrict} {
		if alive[seq] {
			t.Errorf("seq %d should be dead", seq)
		}
	}
	for _, seq := range []int{touchAtEnd, touchAtStart, emptyEdgeS, emptyEdgeE} {
		if !alive[seq] {
			t.Errorf("seq %d should be alive (adjacent/edge)", seq)
		}
	}
	if len(snap.Dead) != 3 {
		t.Fatalf("dead count=%d want 3: %+v", len(snap.Dead), snap.Dead)
	}
	for _, d := range snap.Dead {
		if d.FailedVersion != 1 {
			t.Errorf("dead failed version=%d", d.FailedVersion)
		}
	}
	// 相接存活诊断平移：[6,8) 左移 3 到 [3,5)；[0,3) 不变；空 (3,3)→(3,3)；(6,6)→(3,3)
	want := map[int][2]int{
		touchAtEnd:   {0, 3},
		touchAtStart: {3, 5},
		emptyEdgeS:   {3, 3},
		emptyEdgeE:   {3, 3},
	}
	for _, d := range snap.Diagnostics {
		if w, ok := want[d.Sequence]; ok && w != [2]int{d.Range.Start.Character, d.Range.End.Character} {
			t.Errorf("seq %d got (%d,%d) want %v", d.Sequence, d.Range.Start.Character, d.Range.End.Character, w)
		}
	}
	if s.Text() != "abcgh" {
		t.Fatalf("text=%q", s.Text())
	}
}

// TestReplacementWithNewlineShift 替换文本含换行、删除跨行后的平移与位置还原。
func TestReplacementWithNewlineShift(t *testing.T) {
	// 三行： "aa\nBBBB\ncc"
	doc := "aa\nBBBB\ncc"
	s := NewService(doc)
	// 删除第二行 "BBBB"（偏移 3..7）替换为 "X\nY"（含换行，码元长度 3）
	// 诊断 [8,9) 指向末尾 "cc" 中的 c（偏移 8），登记在 v0
	seq, err := s.Register(0, Diagnostic{
		Range:    Range{Start: Position{2, 0}, End: Position{2, 1}},
		Severity: SeverityWarning, Message: "tail",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := s.Change(0, []Edit{{
		Range: Range{Start: Position{1, 0}, End: Position{1, 4}},
		Text:  "X\nY",
	}})
	if !r.Accepted {
		t.Fatal(r.Reason)
	}
	snap := s.Snapshot()
	if snap.Text != "aa\nX\nY\ncc" {
		t.Fatalf("text=%q", snap.Text)
	}
	// 删除 4 码元、插入 3 码元，净 -1；原偏移 8 → 7；新文本中偏移 7 是第三行 "Y" 后换行再 'c'。
	// "aa\nX\nY\ncc" 行: 0:"aa"(0-2) 1:"X"(3) 2:"Y"(5) 3:"cc"(7-8)
	var found *DiagnosticRecord
	for i := range snap.Diagnostics {
		if snap.Diagnostics[i].Sequence == seq {
			found = &snap.Diagnostics[i]
		}
	}
	if found == nil {
		t.Fatal("tail diag missing")
	}
	if found.Range.Start != (Position{3, 0}) || found.Range.End != (Position{3, 1}) {
		t.Fatalf("tail diag moved to %+v want (3,0)-(3,1)", found.Range)
	}
}

// TestDeadStayDead 已失效诊断不再迁移、不可恢复。
func TestDeadStayDead(t *testing.T) {
	s := NewService("abcdef")
	seq := reg(t, s, 0, 1, 3)
	s.Change(0, []Edit{{Range: Range{Start: Position{0, 1}, End: Position{0, 3}}, Text: ""}})
	if s.Snapshot().Dead[0].Sequence != seq {
		t.Fatal("not dead")
	}
	s.Change(1, []Edit{{Range: pointRange(0, 1), Text: "zz"}})
	snap := s.Snapshot()
	if len(snap.Dead) != 1 || snap.Dead[0].Sequence != seq || snap.Dead[0].FailedVersion != 1 {
		t.Fatalf("dead list changed: %+v", snap.Dead)
	}
}

// TestRegisterRejects 登记拒绝优先级：过期 > start>end > 越界 > 代理对中间。
func TestRegisterRejects(t *testing.T) {
	stale := NewService("a" + emoji)
	stale.Change(0, []Edit{{Range: pointRange(0, 0), Text: "z"}}) // v1
	if _, err := stale.Register(0, diagOff(stale, 0, 0, 1)); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("stale: %v", err)
	}
	s := NewService("a" + emoji) // v0，文本 a😀（列 0=a，1,2=代理对）
	// start>end
	if _, err := s.Register(0, Diagnostic{
		Range: Range{Start: Position{0, 3}, End: Position{0, 1}},
	}); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("reverse: %v", err)
	}
	// 越界
	if _, err := s.Register(0, diagOff(s, 0, 0, 9)); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("oob: %v", err)
	}
	// 代理对中间
	if _, err := s.Register(0, diagOff(s, 0, 2, 2)); !errors.Is(err, ErrInsideSurrogatePair) {
		t.Fatalf("surrogate: %v", err)
	}
}
