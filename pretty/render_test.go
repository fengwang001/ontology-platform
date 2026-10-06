package pretty

import (
	"reflect"
	"strings"
	"testing"
)

// renderOK 渲染并断言无错误。
func renderOK(t *testing.T, d Doc, width int) Result {
	t.Helper()
	res, err := Render(d, width)
	if err != nil {
		t.Fatalf("Render(width=%d) 出错: %v", width, err)
	}
	return res
}

// expectText 断言渲染文本。
func expectText(t *testing.T, d Doc, width int, want string) {
	t.Helper()
	res := renderOK(t, d, width)
	if res.Text != want {
		t.Fatalf("width=%d:\n got %q\nwant %q", width, res.Text, want)
	}
}

// TestExactFitBoundary 恰好取等放得下，多一列放不下。
func TestExactFitBoundary(t *testing.T) {
	// "ab cd" 宽 5。
	doc := Group(Seq(Text("ab"), Space(), Text("cd")))
	expectText(t, doc, 5, "ab cd")
	expectText(t, doc, 4, "ab\ncd")

	// 后续内容也算入：组内容宽 5，紧跟文本 "x"。
	doc2 := Seq(Group(Seq(Text("ab"), Space(), Text("cd"))), Text("x"))
	expectText(t, doc2, 6, "ab cdx")  // 5+1 = 6，取等放得下
	expectText(t, doc2, 5, "ab\ncdx") // 多一列放不下
}

// TestFollowingContentAffectsDecision 后续内容的宽度影响组的判定：
// 组自身放得下，但连同后续内容放不下时组断开。
func TestFollowingContentAffectsDecision(t *testing.T) {
	// 组内容 "ab cd" 宽 5；后续文本 "efgh" 宽 4。
	doc := Seq(Group(Seq(Text("ab"), Space(), Text("cd"))), Text("efgh"))
	expectText(t, doc, 9, "ab cdefgh")  // 5+4 = 9，放得下
	expectText(t, doc, 8, "ab\ncdefgh") // 组自身放得下，连同后续放不下
}

// TestBrokenOuterTruncatesScan 外层已断开时，后续内容里处于断开模式的
// 可断点截断放得下判定的考察。
func TestBrokenOuterTruncatesScan(t *testing.T) {
	// 外层组含 11 列文本，行宽 10，必断开；其直接可断空格随之断开。
	// 内层组 "bb cc" 宽 5，考察在遇到外层已断开的可断空格时停止，
	// 因此不计入后面 20 列的 "d..."。
	doc := Group(Seq(
		Text("aaaaaaaaaaa"), // 11 列
		Space(),
		Group(Seq(Text("bb"), Space(), Text("cc"))),
		Space(),
		Text("dddddddddddddddddddd"), // 20 列
	))
	expectText(t, doc, 10, "aaaaaaaaaaa\nbb cc\ndddddddddddddddddddd")
}

// TestHardLinePropagatesOutward 含强制换行的组不可能平铺，并向外层传播。
func TestHardLinePropagatesOutward(t *testing.T) {
	doc := Group(Seq(
		Text("a"), Space(),
		Group(Seq(Text("b"), HardLine(), Text("c"))),
		Space(), Text("d"),
	))
	// 行宽足够放下 "a b" 与 "d"，但强制换行使两个组都断开。
	expectText(t, doc, 80, "a\nb\nc\nd")
}

// TestAlignAndIndent 对齐与缩进叠加：对齐把缩进重置为当前列，其内部的
// 缩进继续在此基础上累加。
func TestAlignAndIndent(t *testing.T) {
	// "k: " 之后对齐到第 3 列。
	doc := Group(Seq(Text("k: "), Align(Seq(Text("v1"), Space(), Text("v2")))))
	expectText(t, doc, 80, "k: v1 v2")
	expectText(t, doc, 6, "k: v1\n   v2")

	// 对齐内部再缩进 2 列：新行缩进 3+2=5。
	doc2 := Group(Seq(Text("k: "), Align(Indent(2, Seq(Text("v1"), Space(), Text("v2"))))))
	expectText(t, doc2, 6, "k: v1\n     v2")

	// 对齐发生在换行后的行首时取当前缩进列（首行本身无缩进）。
	doc3 := Indent(2, Group(Seq(Text("a"), Space(), Align(Seq(Text("b"), Space(), Text("c"))))))
	expectText(t, doc3, 3, "a\n  b\n  c")
}

// TestCondText 条件文本随最近外层组的模式变化；不在任何组内按断开处理。
func TestCondText(t *testing.T) {
	doc := Group(Seq(Text("x"), CondText(",", ";"), Text("y")))
	expectText(t, doc, 3, "x,y")
	expectText(t, doc, 2, "x;y")

	// 不在任何组内：按断开处理。
	expectText(t, Seq(Text("x"), CondText(",", ";"), Text("y")), 80, "x;y")

	// 嵌套组：内层组自行判定，条件文本取内层组的模式。
	nested := Group(Seq(Text("aaaa"), Space(), Group(Seq(Text("b"), CondText(",", ";"), Text("c")))))
	expectText(t, nested, 5, "aaaa\nb,c")
}

// TestDoubleWidth 双宽字符按宽度 2 计。
func TestDoubleWidth(t *testing.T) {
	if got := Width("界面ab"); got != 6 {
		t.Fatalf("Width(界面ab) = %d, want 6", got)
	}
	doc := Group(Seq(Text("界面"), Space(), Text("ab")))
	expectText(t, doc, 7, "界面 ab") // 4+1+2 = 7，取等放得下
	expectText(t, doc, 6, "界面\nab")
}

// TestTrailingSpaceTrim 每行行尾的全部空格都要去掉；只含缩进的行输出
// 为空行。
func TestTrailingSpaceTrim(t *testing.T) {
	expectText(t, Seq(Text("ab  "), HardLine(), Text("cd")), 80, "ab\ncd")

	// 文本自带的行尾空格在可断点断开时也要去掉。
	doc := Group(Seq(Text("ab "), Space(), Text("cd")))
	expectText(t, doc, 3, "ab\ncd")

	// 只含缩进的行输出为空行。
	doc2 := Seq(Text("a"), Indent(4, Seq(HardLine(), HardLine())), Text("b"))
	expectText(t, doc2, 80, "a\n\n    b")
}

// TestOverlongLines 超宽行清单：去掉行尾空格后宽度仍超过行宽的行，
// 按行号升序；超宽不是错误，内容不丢弃。
func TestOverlongLines(t *testing.T) {
	doc := Seq(Text("abcdef"), HardLine(), Text("界ab"), HardLine(), Text("xy"))
	res := renderOK(t, doc, 3)
	if res.Text != "abcdef\n界ab\nxy" {
		t.Fatalf("got %q", res.Text)
	}
	want := []OverlongLine{{Line: 1, Width: 6}, {Line: 2, Width: 4}}
	if !reflect.DeepEqual(res.Overlong, want) {
		t.Fatalf("Overlong = %+v, want %+v", res.Overlong, want)
	}
}

// TestRefEquivalence 片段引用与直接展开行为完全相同。
func TestRefEquivalence(t *testing.T) {
	s := NewSession()
	frag := Group(Seq(Text("x"), Space(), Indent(2, Seq(Text("y"), Space(), Text("z")))))
	if err := s.Register("f", frag); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// 片段可以再引用已登记的片段。
	if err := s.Register("g", Seq(Text("["), Ref("f"), Text("]"))); err != nil {
		t.Fatalf("Register g: %v", err)
	}
	viaRef := Seq(Text("> "), Ref("g"))
	inline := Seq(Text("> "), Text("["), frag, Text("]"))
	for w := 1; w <= 20; w++ {
		r1, err1 := s.Render(viaRef, w)
		r2, err2 := s.Render(inline, w)
		if err1 != nil || err2 != nil {
			t.Fatalf("width=%d: %v %v", w, err1, err2)
		}
		if !reflect.DeepEqual(r1, r2) {
			t.Fatalf("width=%d:\nref    %q %+v\ninline %q %+v", w, r1.Text, r1.Overlong, r2.Text, r2.Overlong)
		}
	}
}

// TestBrokenGroupBreaksDirectBreakables 组断开时其直接包含的可断点
// （可断空格与可断空串）全部换行。
func TestBrokenGroupBreaksDirectBreakables(t *testing.T) {
	doc := Group(Seq(Text("a"), Space(), Text("b"), Blank(), Text("c")))
	expectText(t, doc, 80, "a bc") // 可断空串平铺时不输出任何内容
	expectText(t, doc, 3, "a\nb\nc")
}

// TestTopLevelBreakablesBreak 不在任何组内的可断点按断开处理。
func TestTopLevelBreakablesBreak(t *testing.T) {
	expectText(t, Seq(Text("a"), Space(), Text("b"), Blank(), Text("c")), 80, "a\nb\nc")
}

// TestEmptyDocument 空文档渲染为空串。
func TestEmptyDocument(t *testing.T) {
	res := renderOK(t, Seq(), 10)
	if res.Text != "" || len(res.Overlong) != 0 {
		t.Fatalf("got %q %+v", res.Text, res.Overlong)
	}
}

// TestNoTrailingNewline 渲染不会自作主张追加行尾换行。
func TestNoTrailingNewline(t *testing.T) {
	expectText(t, Text("abc"), 10, "abc")
	expectText(t, Seq(Text("abc"), HardLine()), 10, "abc\n")
}

// TestStringWidth 宽度函数的边界。
func TestStringWidth(t *testing.T) {
	cases := map[string]int{
		"":          0,
		"abc":       3,
		"a b":       3,
		"⺀":         2, // U+2E80 恰好是双宽边界
		"⹿":         1, // U+2E7F 仍是单宽
		"你好, world": 11,
	}
	for s, want := range cases {
		if got := Width(s); got != want {
			t.Fatalf("Width(%q) = %d, want %d", s, got, want)
		}
	}
}

// TestIndentOnlyLineBetweenBreaks 连续断开产生空行而非缩进空格行。
func TestIndentOnlyLineBetweenBreaks(t *testing.T) {
	doc := Indent(6, Seq(Text("a"), HardLine(), HardLine(), Text("b")))
	res := renderOK(t, doc, 80)
	if res.Text != "a\n\n      b" {
		t.Fatalf("got %q", res.Text)
	}
	lines := strings.Split(res.Text, "\n")
	for i, ln := range lines {
		if strings.HasSuffix(ln, " ") {
			t.Fatalf("第 %d 行有行尾空格: %q", i+1, ln)
		}
	}
}
