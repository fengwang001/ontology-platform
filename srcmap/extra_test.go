package srcmap

import (
	"errors"
	"testing"
)

func TestMiscBranches(t *testing.T) {
	// SourceCount / Lines / Get / Error() / CategoryOf 普通错误分支。
	m := mustNew(t, 3, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 2, 1, 0)}}})
	if m.SourceCount() != 3 || len(m.Lines()) != 1 {
		t.Fatalf("访问器错误")
	}
	svc := NewService()
	if err := svc.Register("z", 3, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 2, 1, 0)}}}); err != nil {
		t.Fatal(err)
	}
	sc, lines, err := svc.Get("z")
	if err != nil || sc != 3 || len(lines) != 1 {
		t.Fatalf("Get: %v %d %v", err, sc, lines)
	}
	if _, _, err := svc.Get("missing"); CategoryOf(err) != CategoryNotFound {
		t.Fatalf("Get 缺名: %v", err)
	}
	var me *MapError
	if !errors.As(error(&MapError{Category: CategoryPositionOverflow, Message: "x"}), &me) || me.Error() == "" {
		t.Fatalf("Error()/As 失败")
	}
	if CategoryOf(errors.New("plain")) != "" {
		t.Fatalf("非本包错误类别应为空")
	}

	// 各种校验分支。
	bad := [][]Line{
		{{GeneratedLine: -1, Segments: []Segment{mapped(0, 0, 0, 0)}}},
		{{GeneratedLine: 0, Segments: []Segment{{Start: MaxCoord + 1}}}},
		{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, -1, 0)}}},
		{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, -1)}}},
	}
	for i, ls := range bad {
		if _, err := New(1, ls); CategoryOf(err) != CategoryInvalidArgument {
			t.Fatalf("bad[%d] 应参数非法: %v", i, err)
		}
	}
	if _, err := New(-1, nil); CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("负源数量: %v", err)
	}

	// 注册非法映射不改变状态；M2 段源索引非 0。
	if err := svc.Register("bad", 1, bad[0]); CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("非法登记: %v", err)
	}
	if _, _, err := svc.Get("bad"); CategoryOf(err) != CategoryNotFound {
		t.Fatalf("被拒绝登记不得写入状态")
	}
	if err := svc.Register("three", 3, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 1, 0, 0)}}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Compose("three", "z", "o1"); CategoryOf(err) != CategoryInvalidArgument {
		t.Fatalf("M2 段源索引非 0: %v", err)
	}

	// Lookup 溢出与空行。
	big := mustNew(t, 1, []Line{{GeneratedLine: 0, Segments: []Segment{mapped(0, 0, 0, MaxCoord)}}})
	if _, err := big.Lookup(0, 1); CategoryOf(err) != CategoryPositionOverflow {
		t.Fatalf("查询段内溢出: %v", err)
	}
	empty := mustNew(t, 1, nil)
	if r, _ := empty.Lookup(0, 0); r.Mapped {
		t.Fatalf("空映射查询应未映射")
	}
	if r, _ := m.Lookup(5, 0); r.Mapped {
		t.Fatalf("不存在的行应未映射")
	}
	if r, _ := m.Lookup(0, 0); !r.Mapped || r.Position.SourceIndex != 2 {
		t.Fatalf("正常查询错误: %v", r)
	}
}
