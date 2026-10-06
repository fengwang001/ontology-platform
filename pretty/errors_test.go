package pretty

import (
	"strings"
	"testing"
)

// expectCode 断言错误类别。
func expectCode(t *testing.T, err error, want ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，实际无错误", want)
	}
	code, ok := CodeOf(err)
	if !ok || code != want {
		t.Fatalf("错误类别 = %v，期望 %v（err=%v）", code, want, err)
	}
}

// deepChain 构造 depth 层 Group 嵌套。
func deepChain(depth int, leaf Doc) Doc {
	d := leaf
	for i := 0; i < depth; i++ {
		d = Group(d)
	}
	return d
}

// TestRenderInvalidWidth 行宽小于 1 或大于 10000 为参数非法。
func TestRenderInvalidWidth(t *testing.T) {
	for _, w := range []int{-1, 0, 10001, 1 << 30} {
		_, err := Render(Text("a"), w)
		expectCode(t, err, ErrInvalidParam)
	}
	for _, w := range []int{1, 10000} {
		if _, err := Render(Text("a"), w); err != nil {
			t.Fatalf("width=%d 不应报错: %v", w, err)
		}
	}
}

// TestInvalidParams 各类非法参数。
func TestInvalidParams(t *testing.T) {
	expectCode(t, RenderErr(Text("a\nb"), 10), ErrInvalidParam)
	expectCode(t, RenderErr(Text("a\rb"), 10), ErrInvalidParam)
	expectCode(t, RenderErr(CondText("x\n", "y"), 10), ErrInvalidParam)
	expectCode(t, RenderErr(Indent(-1, Text("a")), 10), ErrInvalidParam)
	expectCode(t, RenderErr(nil, 10), ErrInvalidParam)
}

// RenderErr 是 Render 的只取错误的便捷形式。
func RenderErr(d Doc, width int) error {
	_, err := Render(d, width)
	return err
}

// TestTooDeep 嵌套深度含引用展开后超过 1000 报文档过深。
func TestTooDeep(t *testing.T) {
	if err := RenderErr(deepChain(999, Text("x")), 10); err != nil {
		t.Fatalf("深度 1000 应合法: %v", err)
	}
	expectCode(t, RenderErr(deepChain(1000, Text("x")), 10), ErrTooDeep)

	// 引用展开后超限：片段自身深 600，文档把它嵌在 600 层里。
	s := NewSession()
	if err := s.Register("f", deepChain(599, Text("x"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Render(deepChain(400, Ref("f")), 10); err != nil {
		t.Fatalf("深度 1000 应合法: %v", err)
	}
	_, err := s.Render(deepChain(401, Ref("f")), 10)
	expectCode(t, err, ErrTooDeep)
}

// TestUnregisteredRef 引用未登记的片段。
func TestUnregisteredRef(t *testing.T) {
	expectCode(t, RenderErr(Ref("nope"), 10), ErrUnregisteredRef)
	expectCode(t, RenderErr(Seq(Text("a"), Ref("nope")), 10), ErrUnregisteredRef)
}

// TestOutputTooLarge 渲染结果总宽度超过 10^7。
func TestOutputTooLarge(t *testing.T) {
	// 缩进 3_000_000 的四行：总宽度约 1.2*10^7，文档本身很小。
	var lines []Doc
	for i := 0; i < 4; i++ {
		lines = append(lines, HardLine(), Text("x"))
	}
	doc := Indent(3_000_000, Seq(lines...))
	expectCode(t, RenderErr(doc, 100), ErrOutputTooLarge)

	// 不超限时正常。
	doc2 := Indent(100, Seq(HardLine(), Text("x")))
	if err := RenderErr(doc2, 100); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
}

// TestOutputLimitAfterTrim 输出上限按渲染结果（去掉行尾空格后）的总
// 宽度判定：巨额行尾空格被裁掉后不算超限。
func TestOutputLimitAfterTrim(t *testing.T) {
	// 10^7+5 个行尾空格被裁掉，结果只有换行符，不应报输出过大。
	doc := Seq(Text(strings.Repeat(" ", MaxOutputWidth+5)), HardLine())
	res, err := Render(doc, 100)
	if err != nil {
		t.Fatalf("行尾空格裁掉后不应超限: %v", err)
	}
	if res.Text != "\n" {
		t.Fatalf("got %q", res.Text)
	}
	// 10^7+1 个非空格字符无法裁掉，必须超限。
	expectCode(t, RenderErr(Text(strings.Repeat("a", MaxOutputWidth+1)), 100), ErrOutputTooLarge)
}

// TestErrorPriority 错误优先级：参数非法 > 文档过深 > 未登记引用 >
// 输出过大。
func TestErrorPriority(t *testing.T) {
	// 未登记引用 + 输出会过大：报未登记引用。
	big := Indent(3_000_000, Seq(HardLine(), Text("x")))
	expectCode(t, RenderErr(Seq(big, Ref("nope")), 100), ErrUnregisteredRef)

	// 过深 + 未登记引用：报文档过深。
	expectCode(t, RenderErr(deepChain(1001, Ref("nope")), 10), ErrTooDeep)

	// 参数非法 + 过深 + 未登记引用：报参数非法。
	bad := Seq(Text("a\nb"), deepChain(1001, Ref("nope")))
	expectCode(t, RenderErr(bad, 10), ErrInvalidParam)

	// 行宽非法优先于一切文档错误。
	expectCode(t, RenderErr(bad, 0), ErrInvalidParam)
}

// TestRegisterErrors 登记错误与优先级：参数非法、重复片段名。
func TestRegisterErrors(t *testing.T) {
	s := NewSession()
	expectCode(t, s.Register("", Text("a")), ErrInvalidParam)
	expectCode(t, s.Register("f", nil), ErrInvalidParam)
	expectCode(t, s.Register("f", Text("a\nb")), ErrInvalidParam)
	expectCode(t, s.Register("f", Indent(-2, Text("a"))), ErrInvalidParam)

	// 被拒绝的登记不改变会话状态：同名可再次登记。
	if err := s.Register("f", Text("ok")); err != nil {
		t.Fatalf("登记应成功: %v", err)
	}
	expectCode(t, s.Register("f", Text("dup")), ErrDuplicateName)

	// 片段只能引用已登记的片段。
	expectCode(t, s.Register("g", Ref("missing")), ErrUnregisteredRef)
	if err := s.Register("g", Ref("f")); err != nil {
		t.Fatalf("引用已登记片段应成功: %v", err)
	}

	// 重复名与非法参数同时存在时，参数非法优先。
	expectCode(t, s.Register("f", Text("x\ny")), ErrInvalidParam)
}

// TestRenderDoesNotMutateSession 渲染不改变会话状态。
func TestRenderDoesNotMutateSession(t *testing.T) {
	s := NewSession()
	if err := s.Register("f", Text("frag")); err != nil {
		t.Fatal(err)
	}
	doc := Seq(Text("["), Ref("f"), Text("]"))
	before, err := s.Render(doc, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 渲染失败也不改变状态。
	expectCode(t, RenderErr(Ref("nope"), 10), ErrUnregisteredRef)
	_ = s
	after, err := s.Render(doc, 10)
	if err != nil {
		t.Fatal(err)
	}
	if before.Text != after.Text || before.Text != "[frag]" {
		t.Fatalf("渲染结果不一致: %q vs %q", before.Text, after.Text)
	}
	// 渲染后仍可正常登记。
	if err := s.Register("g", Text("x")); err != nil {
		t.Fatal(err)
	}
}

// TestErrorMessage 错误信息可读且类别可区分。
func TestErrorMessage(t *testing.T) {
	err := RenderErr(Ref("nope"), 10)
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("错误信息应包含片段名: %v", err)
	}
}
