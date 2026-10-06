package docsync

import (
	"errors"
	"strings"
	"testing"
)

const emoji = "😀" // U+1F600，UTF-16 占 2 码元

func mustOffset(t *testing.T, s *Service, line, col int) int {
	t.Helper()
	off, err := s.PositionToOffset(Position{Line: line, Character: col})
	if err != nil {
		t.Fatalf("offset (%d,%d): %v", line, col, err)
	}
	return off
}

func TestEmptyDocumentOneLine(t *testing.T) {
	s := NewService("")
	snap := s.Snapshot()
	if snap.Version != 0 || snap.Text != "" || len(snap.Diagnostics) != 0 {
		t.Fatalf("bad empty snapshot: %+v", snap)
	}
	if _, err := s.PositionToOffset(Position{Line: 0, Character: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PositionToOffset(Position{Line: 1, Character: 0}); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("line 1 should be OOB, got %v", err)
	}
	if _, err := s.OffsetToPosition(0); err != nil {
		t.Fatal(err)
	}
}

func TestSurrogateColumns(t *testing.T) {
	// 行内容：a 😀 b（码元: a=1, emoji=2, b=1）
	s := NewService("a" + emoji + "b")
	for _, tc := range []struct {
		col int
		err error
		off int
	}{
		{0, nil, 0},                    // a 前
		{1, nil, 1},                    // emoji 前（码元边界）
		{2, ErrInsideSurrogatePair, 0}, // 代理对中间
		{3, nil, 3},                    // emoji 后、b 前
		{4, nil, 4},                    // 行尾
		{5, ErrOutOfBounds, 0},
	} {
		off, err := s.PositionToOffset(Position{Line: 0, Character: tc.col})
		if tc.err != nil {
			if !errors.Is(err, tc.err) {
				t.Fatalf("col %d want %v got %v", tc.col, tc.err, err)
			}
			continue
		}
		if err != nil || off != tc.off {
			t.Fatalf("col %d want off %d, got %d %v", tc.col, tc.off, off, err)
		}
	}
	// 反向换算：偏移 2 落在代理对中间。
	if _, err := s.OffsetToPosition(2); !errors.Is(err, ErrInsideSurrogatePair) {
		t.Fatalf("off 2 want surrogate, got %v", err)
	}
	p, err := s.OffsetToPosition(3)
	if err != nil || p != (Position{0, 3}) {
		t.Fatalf("off 3: %+v %v", p, err)
	}
	if _, err := s.OffsetToPosition(5); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("off 5 want OOB, got %v", err)
	}
}

func TestCarriageReturnIsNormal(t *testing.T) {
	s := NewService("a\rb\r\nb")
	if got := strings.Count(s.Text(), "\n"); got != 1 {
		t.Fatalf("want 1 line break, got %d", got)
	}
	// 两行："a\rb\r" 与 "b"（回车是普通字符，可作为行内容，只有 \n 分行）。
	if _, err := s.PositionToOffset(Position{Line: 1, Character: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PositionToOffset(Position{Line: 2, Character: 0}); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("line 2 should not exist, got %v", err)
	}
	off := mustOffset(t, s, 0, 2) // 第 0 行 a \r b，列 2 = 'b'
	if p, _ := s.OffsetToPosition(off); p != (Position{0, 2}) {
		t.Fatalf("CR counts as char: %+v", p)
	}
}

func TestBasicEditsAndVersion(t *testing.T) {
	s := NewService("hello")
	r := s.Change(0, []Edit{{Range: Range{Start: Position{0, 0}, End: Position{0, 5}}, Text: "world!!"}})
	if !r.Accepted || s.Text() != "world!!" || s.Version() != 1 {
		t.Fatalf("replace: %+v text=%q", r, s.Text())
	}
	// 过期
	if r := s.Change(0, []Edit{{Range: pointRange(0, 0), Text: "x"}}); !errors.Is(r.Reason, ErrStaleVersion) {
		t.Fatalf("stale: %+v", r)
	}
	// 删除
	if r := s.Change(1, []Edit{{Range: Range{Start: Position{0, 0}, End: Position{0, 5}}, Text: ""}}); !r.Accepted || s.Text() != "!!" {
		t.Fatalf("delete: %+v text=%q", r, s.Text())
	}
	// 插入
	if r := s.Change(2, []Edit{{Range: pointRange(0, 0), Text: "ab"}}); !r.Accepted || s.Text() != "ab!!" {
		t.Fatalf("insert: %+v text=%q", r, s.Text())
	}
}

func pointRange(line, col int) Range {
	return Range{Start: Position{line, col}, End: Position{line, col}}
}

func TestSimultaneousAdjacentAndSamePointInserts(t *testing.T) {
	s := NewService("abc")
	// [1,1) 插入 X、[1,2) 删除 b→Y、[1,1) 插入 Z、[2,2) 插入 W（相接于删除终点）
	edits := []Edit{
		{Range: pointRange(0, 1), Text: "X"},
		{Range: Range{Start: Position{0, 1}, End: Position{0, 2}}, Text: "Y"},
		{Range: pointRange(0, 1), Text: "Z"},
		{Range: pointRange(0, 2), Text: "W"},
	}
	r := s.Change(0, edits)
	if !r.Accepted {
		t.Fatalf("rejected: %v", r.Reason)
	}
	// 期望：a + XZ（同点插入按序）+ Y（替换 b）+ W（接在替换后）+ c
	if got := s.Text(); got != "aXZYWc" {
		t.Fatalf("text=%q want aXZYWc", got)
	}

	// 重叠拒绝
	s = NewService("abcdef")
	r = s.Change(0, []Edit{
		{Range: Range{Start: Position{0, 0}, End: Position{0, 3}}, Text: "P"},
		{Range: Range{Start: Position{0, 2}, End: Position{0, 4}}, Text: "Q"},
	})
	if !errors.Is(r.Reason, ErrOverlappingEdits) {
		t.Fatalf("overlap: %+v", r)
	}
	// 相接不重叠
	r = s.Change(0, []Edit{
		{Range: Range{Start: Position{0, 0}, End: Position{0, 3}}, Text: "P"},
		{Range: Range{Start: Position{0, 3}, End: Position{0, 4}}, Text: "Q"},
	})
	if !r.Accepted {
		t.Fatalf("adjacent: %v", r.Reason)
	}
}

func TestRejectPriority(t *testing.T) {
	s := NewService("abc")
	s.Change(0, []Edit{{Range: pointRange(0, 0), Text: "z"}}) // v1
	// 同时过期 + 越界 + 代理对中间 + 重叠 → 过期优先
	r := s.Change(0, []Edit{
		{Range: Range{Start: Position{0, 0}, End: Position{9, 0}}},
	})
	if !errors.Is(r.Reason, ErrStaleVersion) {
		t.Fatalf("priority stale: %v", r.Reason)
	}
	// 越界优先于代理对中间
	s = NewService("a" + emoji)
	r = s.Change(0, []Edit{
		{Range: Range{Start: Position{0, 99}, End: Position{0, 2}}},
	})
	if !errors.Is(r.Reason, ErrOutOfBounds) {
		t.Fatalf("priority oob: %v", r.Reason)
	}
	// 代理对中间优先于重叠
	r = s.Change(0, []Edit{
		{Range: Range{Start: Position{0, 2}, End: Position{0, 2}}, Text: "x"},
		{Range: Range{Start: Position{0, 0}, End: Position{0, 3}}, Text: "y"},
	})
	if !errors.Is(r.Reason, ErrInsideSurrogatePair) {
		t.Fatalf("priority surrogate: %v", r.Reason)
	}
	// 拒绝后无变化
	if s.Version() != 0 || s.Text() != "a"+emoji {
		t.Fatal("state changed after rejection")
	}
}
