package patch

import (
	"errors"
	"io"
	"log"
	"reflect"
	"strings"
	"testing"
)

// testWriter 把每步判定日志送入 testing 输出，验证“打印每步输入、
// 补丁条目与判定依据”的要求。
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

var _ io.Writer = testWriter{}

// ---- 路径编码 / 解码：特殊字符转义 ----

func TestPathEscapeRoundTrip(t *testing.T) {
	keys := []string{"a~b", "c/d", "e", "~/both", "a~0b", "c~1d", "中文键"}
	encoded, err := EncodePath(keys)
	if err != nil {
		t.Fatalf("EncodePath: %v", err)
	}
	want := "/a~1b/c~0d/e/~1~0both/a~10b/c~11d/中文键"
	if encoded != want {
		t.Fatalf("encoded = %q, want %q", encoded, want)
	}
	decoded, err := DecodePath(encoded)
	if err != nil {
		t.Fatalf("DecodePath: %v", err)
	}
	if !reflect.DeepEqual(decoded, keys) {
		t.Fatalf("decoded = %#v, want %#v", decoded, keys)
	}

	// 解码必须先识别转义再拆分：转义出来的 "/" 不能被当作分隔符。
	one, err := DecodePath("/a~0b~1c")
	if err != nil || !reflect.DeepEqual(one, []string{"a/b~c"}) {
		t.Fatalf("escaped slash/tilde decode = %#v, %v", one, err)
	}
}

func TestInvalidPaths(t *testing.T) {
	badDecodes := []string{"a/b", "/a//b", "/a~2b", "/a~"}
	for _, path := range badDecodes {
		if _, err := DecodePath(path); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("DecodePath(%q) err = %v, want ErrInvalidPath", path, err)
		}
	}

	// 数组下标格式错误在应用下钻时归类为非法路径。
	badIndex := []string{"/a/01", "/a/-1", "/a/1.5", "/a/1x"}
	for _, path := range badIndex {
		doc := map[string]any{"a": []any{1, 2, 3}}
		_, err := Apply(doc, Patch{Ops: []Op{OpReplace(path, 9)}})
		if !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("Apply(%q) error = %v, want ErrInvalidPath", path, err)
		}
	}

	// 空段无法编码。
	if _, err := EncodePath([]string{""}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("EncodePath empty segment err = %v", err)
	}
	// 生成时空键整体拒绝。
	_, err := Generate(map[string]any{"": 1}, map[string]any{"": 2})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("generate with empty key err = %v", err)
	}
}

// ---- 生成规则：并集、字节序、单侧增删、不等替换 ----

func TestGenerateRules(t *testing.T) {
	source := map[string]any{
		"b":       2,
		"a":       map[string]any{"x": 1, "shared": 1},
		"removed": "go",
		"arr":     []any{1, 2},
		"scalar":  1,
		"same":    true,
	}
	target := map[string]any{
		"b":      2,
		"a":      map[string]any{"x": 1, "shared": 2, "added": "v"},
		"added":  "new",
		"arr":    []any{9},
		"scalar": "one",
		"same":   true,
	}

	var decisions []Decision
	logger := log.New(testWriter{t}, "patch-step ", 0)
	p, err := Generate(source, target, WithDecisionLogger(func(d Decision) {
		decisions = append(decisions, d)
		logger.Printf("path=%q kind=%s srcType=%s dstType=%s op=%+v",
			d.Path, d.Kind, d.SourceType, d.TargetType, d.Op)
	}))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// 条目按路径字节序、操作语义最小化：相等值不产生操作。
	wantOps := []Op{
		OpAdd("/a/added", "v"),
		OpReplace("/a/shared", 2),
		OpAdd("/added", "new"),
		OpReplace("/arr", []any{9}),
		OpRemove("/removed"),
		OpReplace("/scalar", "one"),
	}
	if !reflect.DeepEqual(p.Ops, wantOps) {
		t.Fatalf("ops = %#v\nwant %#v", p.Ops, wantOps)
	}

	// 判定依据必须记录每一步：相等的 same 也应有 equal 判定。
	kinds := map[string]bool{}
	for _, d := range decisions {
		kinds[d.Kind] = true
	}
	for _, kind := range []string{"equal", "object", "add", "remove", "replace"} {
		if !kinds[kind] {
			t.Fatalf("decision kind %q not logged; got %#v", kind, kinds)
		}
	}

	// 数组整体替换：不允许出现深入数组下标的操作。
	for _, op := range p.Ops {
		if strings.HasPrefix(op.Path, "/arr/") {
			t.Fatalf("array must be replaced wholesale, got %#v", op)
		}
	}
}

// ---- null 值键与缺失键的区分 ----

func TestNullVsMissing(t *testing.T) {
	// 缺失 -> null 是 add；值 -> null 与 null -> 值都是 replace。
	p, err := Generate(
		map[string]any{"b": nil, "c": 1},
		map[string]any{"a": nil, "b": 2, "c": nil},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []Op{
		OpAdd("/a", nil),
		OpReplace("/b", 2),
		OpReplace("/c", nil),
	}
	if !reflect.DeepEqual(p.Ops, want) {
		t.Fatalf("ops = %#v want %#v", p.Ops, want)
	}

	// replace 不存在的键失败（路径不存在），add 不要求键存在，
	// 因此应用端也能区分“null 值键”与“缺失键”。
	doc := map[string]any{}
	if _, err := Apply(doc, Patch{Ops: []Op{OpReplace("/missing", nil)}}); !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("replace missing key err = %v, want ErrPathNotFound", err)
	}
	got, err := Apply(doc, Patch{Ops: []Op{OpAdd("/missing", nil)}})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := got.(map[string]any)["missing"]
	if !ok || value != nil {
		t.Fatalf("add null key = %#v, want explicit null", got)
	}
	if _, ok := doc["missing"]; ok {
		t.Fatal("input doc must remain untouched on success")
	}
}

// ---- 数组下标合法性与数组整体替换 ----

func TestArrayOps(t *testing.T) {
	doc := map[string]any{"arr": []any{1, 2, 3}}

	got, err := Apply(doc, Patch{Ops: []Op{OpReplace("/arr", []any{4, 5, 6, 7})}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.(map[string]any)["arr"], []any{4, 5, 6, 7}) {
		t.Fatalf("array replace = %#v", got)
	}

	cases := []struct {
		op  Op
		err error
	}{
		{OpAdd("/arr/0", 0), nil},
		{OpRemove("/arr/2"), nil},
		{OpReplace("/arr/0", 9), nil},
		{OpReplace("/arr/9", 9), ErrPathNotFound},
		{OpAdd("/arr/99", 9), ErrPathNotFound},
		{OpReplace("/arr/00", 9), ErrInvalidPath},
		{OpReplace("/arr/-1", 9), ErrInvalidPath},
	}
	for _, tc := range cases {
		_, err := Apply(doc, Patch{Ops: []Op{tc.op}})
		if tc.err == nil && err != nil {
			t.Fatalf("%#v unexpected err %v", tc.op, err)
		}
		if tc.err != nil && !errors.Is(err, tc.err) {
			t.Fatalf("%#v err = %v, want %v", tc.op, err, tc.err)
		}
	}
}

// ---- 错误类别互不相同、可区分 ----

func TestErrorCategoriesDistinct(t *testing.T) {
	sentinel := []error{
		ErrInvalidPath, ErrPathNotFound, ErrInvalidOperation,
		ErrPatchTooLarge, ErrUnsupportedValue,
	}
	for i := range sentinel {
		for j := i + 1; j < len(sentinel); j++ {
			if errors.Is(sentinel[i], sentinel[j]) {
				t.Fatalf("error categories must be distinct: %v %v",
					sentinel[i], sentinel[j])
			}
		}
	}

	doc := map[string]any{"a": map[string]any{"b": 1}}
	cases := []struct {
		name string
		op   Op
		want error
	}{
		{"unknown op", Op{Type: "frob", Path: "/a/b"}, ErrInvalidOperation},
		{"missing intermediate", OpReplace("/x/y", 1), ErrPathNotFound},
		{"traverse scalar", OpReplace("/a/b/c", 1), ErrPathNotFound},
		{"bad escape", OpReplace("/a/~x", 1), ErrInvalidPath},
		{"remove absent", OpRemove("/a/nope"), ErrPathNotFound},
		{"root add", OpAdd("", 1), ErrInvalidOperation},
	}
	for _, tc := range cases {
		_, err := Apply(doc, Patch{Ops: []Op{tc.op}})
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	_, err := Apply(doc, Patch{Ops: []Op{OpAdd("/x", make(chan int))}})
	if !errors.Is(err, ErrUnsupportedValue) {
		t.Fatalf("unsupported value err = %v", err)
	}
}

// ---- 补丁超限：生成与应用两侧都拒绝 ----

func TestPatchTooLarge(t *testing.T) {
	source := map[string]any{"a": 1, "b": 2}
	target := map[string]any{"a": 2, "b": 3}
	_, err := Generate(source, target, WithMaxOps(1))
	if !errors.Is(err, ErrPatchTooLarge) {
		t.Fatalf("generate limit err = %v", err)
	}

	ops := make([]Op, DefaultMaxOps+1)
	for i := range ops {
		ops[i] = OpReplace("/a", i)
	}
	_, err = Apply(map[string]any{"a": 0}, Patch{Ops: ops})
	if !errors.Is(err, ErrPatchTooLarge) {
		t.Fatalf("apply limit err = %v", err)
	}
}

// ---- 失败不留痕：任一条失败，整份补丁失败且 doc / patch 不变 ----

func TestAtomicFailureLeavesNoTrace(t *testing.T) {
	doc := map[string]any{
		"keep": map[string]any{"nested": 1},
		"list": []any{1, 2, 3},
	}
	p := Patch{Ops: []Op{
		OpReplace("/keep/nested", 42),
		OpAdd("/list/0", 0),
		OpRemove("/ghost"),
	}}

	docSnap, _ := deepCopy(doc)
	patchSnap := deepCopyOps(p.Ops)
	_, err := Apply(doc, p)
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("err = %v, want ErrPathNotFound", err)
	}
	if !reflect.DeepEqual(doc, docSnap) {
		t.Fatalf("doc mutated after failure: got %#v want %#v", doc, docSnap)
	}
	if !reflect.DeepEqual(p.Ops, patchSnap) {
		t.Fatalf("patch mutated after failure: got %#v want %#v", p.Ops, patchSnap)
	}
}

func deepCopyOps(ops []Op) []Op {
	cp := make([]Op, len(ops))
	copy(cp, ops)
	return cp
}
