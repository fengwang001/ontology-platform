package patch

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// logPatch 打印补丁条目，作为判定依据的日志。
func logPatch(t *testing.T, p Patch) {
	t.Helper()
	t.Logf("patch entries (%d):", len(p))
	for i, op := range p {
		t.Logf("  [%d] op=%s path=%q value=%v", i, op.Op, op.Path, op.Value)
	}
}

// roundTrip 校验生成-应用往返相等、深度一致且输入未被修改。
func roundTrip(t *testing.T, oldDoc, newDoc any) Patch {
	t.Helper()
	t.Logf("input old=%v", oldDoc)
	t.Logf("input new=%v", newDoc)
	oldDepth, newDepth := depth(oldDoc), depth(newDoc)

	p, err := Generate(oldDoc, newDoc)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	logPatch(t, p)

	got, err := Apply(oldDoc, p)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	t.Logf("applied result=%v", got)

	if !deepEqual(got, newDoc) {
		t.Fatalf("round-trip mismatch: got %v, want %v", got, newDoc)
	}
	t.Logf("verdict: round-trip equal")
	if depth(got) != newDepth {
		t.Fatalf("result depth %d != target depth %d", depth(got), newDepth)
	}
	if depth(oldDoc) != oldDepth || depth(newDoc) != newDepth {
		t.Fatalf("input depth changed: old %d->%d new %d->%d", oldDepth, depth(oldDoc), newDepth, depth(newDoc))
	}
	t.Logf("verdict: depths preserved (result=%d, inputs unchanged)", depth(got))
	return p
}

// TestEscapeSegment 覆盖特殊字符转义与解码的往返。
func TestEscapeSegment(t *testing.T) {
	cases := []string{"", "plain", "a/b", "a~b", "~/", "a~0b", "空格 键", "a/b~c/d"}
	for _, c := range cases {
		enc := escapeSegment(c)
		dec, err := unescapeSegment(enc)
		if err != nil {
			t.Fatalf("unescape(%q) failed: %v", enc, err)
		}
		t.Logf("input=%q encoded=%q decoded=%q verdict=round-trip", c, enc, dec)
		if dec != c {
			t.Fatalf("escape round-trip: got %q, want %q", dec, c)
		}
	}
}

// TestUnescapeInvalid 非法转义必须拒绝且归类为 ErrInvalidPath。
func TestUnescapeInvalid(t *testing.T) {
	for _, s := range []string{"~", "~2", "a~", "x~y"} {
		_, err := unescapeSegment(s)
		t.Logf("input=%q err=%v verdict=reject", s, err)
		if !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("unescape(%q): want ErrInvalidPath, got %v", s, err)
		}
	}
}

// TestGenerateSpecialChars 含特殊字符的键生成补丁后路径须正确转义。
func TestGenerateSpecialChars(t *testing.T) {
	oldDoc := map[string]any{"a/b": 1.0, "c~d": map[string]any{"x/y": "old"}}
	newDoc := map[string]any{"a/b": 2.0, "c~d": map[string]any{"x/y": "new"}}
	p := roundTrip(t, oldDoc, newDoc)
	want := Patch{
		{Op: OpReplace, Path: "/a~1b", Value: 2.0},
		{Op: OpReplace, Path: "/c~0d/x~1y", Value: "new"},
	}
	if !patchEqual(p, want) {
		t.Fatalf("patch = %+v, want %+v", p, want)
	}
	t.Logf("verdict: escaped paths match expected encoding")
}

// TestGenerateKeyOrder 对象键并集按字节序逐键处理。
func TestGenerateKeyOrder(t *testing.T) {
	oldDoc := map[string]any{"b": 1.0, "a": 1.0, "c": 1.0}
	newDoc := map[string]any{"b": 2.0, "a": 0.0, "d": 3.0}
	p := roundTrip(t, oldDoc, newDoc)
	want := Patch{
		{Op: OpReplace, Path: "/a", Value: 0.0},
		{Op: OpReplace, Path: "/b", Value: 2.0},
		{Op: OpRemove, Path: "/c"},
		{Op: OpAdd, Path: "/d", Value: 3.0},
	}
	if !patchEqual(p, want) {
		t.Fatalf("patch = %+v, want %+v", p, want)
	}
	t.Logf("verdict: ops ordered by byte-wise key union")
}

// TestGenerateEqual 两值相等不产生操作。
func TestGenerateEqual(t *testing.T) {
	doc := map[string]any{"a": []any{1.0, "x"}, "b": nil}
	p := roundTrip(t, doc, doc)
	if len(p) != 0 {
		t.Fatalf("equal docs produced %d ops, want 0", len(p))
	}
	t.Logf("verdict: empty patch for equal docs")
}

// TestGenerateArrayReplace 数组不等时整体替换，不逐元素 diff。
func TestGenerateArrayReplace(t *testing.T) {
	oldDoc := map[string]any{"list": []any{1.0, 2.0, 3.0}}
	newDoc := map[string]any{"list": []any{1.0, 9.0}}
	p := roundTrip(t, oldDoc, newDoc)
	if len(p) != 1 || p[0].Op != OpReplace || p[0].Path != "/list" {
		t.Fatalf("array diff = %+v, want single replace at /list", p)
	}
	t.Logf("verdict: array replaced as a whole")
}

// TestGenerateTypeChange 类型不同整体替换。
func TestGenerateTypeChange(t *testing.T) {
	oldDoc := map[string]any{"v": map[string]any{"x": 1.0}}
	newDoc := map[string]any{"v": "scalar"}
	p := roundTrip(t, oldDoc, newDoc)
	if len(p) != 1 || p[0].Op != OpReplace || p[0].Path != "/v" {
		t.Fatalf("type-change diff = %+v, want single replace at /v", p)
	}
	t.Logf("verdict: object->scalar replaced as a whole")
}

// TestNullVsMissing 空值键与缺失键必须区分。
func TestNullVsMissing(t *testing.T) {
	// 缺失 -> 空值：应生成 add；空值 -> 缺失：应生成 remove。
	p1 := roundTrip(t, map[string]any{}, map[string]any{"k": nil})
	if len(p1) != 1 || p1[0].Op != OpAdd {
		t.Fatalf("missing->null = %+v, want single add", p1)
	}
	t.Logf("verdict: missing key vs null key distinguished (add)")

	p2 := roundTrip(t, map[string]any{"k": nil}, map[string]any{})
	if len(p2) != 1 || p2[0].Op != OpRemove {
		t.Fatalf("null->missing = %+v, want single remove", p2)
	}
	t.Logf("verdict: null key vs missing key distinguished (remove)")

	// 两侧都是空值键：不产生操作。
	p3 := roundTrip(t, map[string]any{"k": nil}, map[string]any{"k": nil})
	if len(p3) != 0 {
		t.Fatalf("null->null produced %+v, want empty", p3)
	}
	t.Logf("verdict: null==null yields no ops")
}

// patchEqual 比较两份补丁是否相同。
func patchEqual(a, b Patch) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Op != b[i].Op || a[i].Path != b[i].Path || !deepEqual(a[i].Value, b[i].Value) {
			return false
		}
	}
	return true
}

// TestApplyBasic 覆盖逐条应用：对象增删改、数组插入与删除。
func TestApplyBasic(t *testing.T) {
	doc := map[string]any{"a": 1.0, "arr": []any{"x", "y"}}
	p := Patch{
		{Op: OpReplace, Path: "/a", Value: 2.0},
		{Op: OpAdd, Path: "/b", Value: "new"},
		{Op: OpAdd, Path: "/arr/1", Value: "mid"},
		{Op: OpRemove, Path: "/arr/0"},
	}
	t.Logf("input doc=%v", doc)
	logPatch(t, p)
	got, err := Apply(doc, p)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	want := map[string]any{"a": 2.0, "b": "new", "arr": []any{"mid", "y"}}
	t.Logf("result=%v want=%v", got, want)
	if !deepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	t.Logf("verdict: sequential ops applied in order")
}

// TestApplyFailures 非法输入整体拒绝，且失败后文档与补丁不变。
func TestApplyFailures(t *testing.T) {
	cases := []struct {
		name string
		doc  any
		op   Op
		want error
	}{
		{"bad escape", map[string]any{}, Op{Op: OpAdd, Path: "/a~2b", Value: 1.0}, ErrInvalidPath},
		{"no leading slash", map[string]any{}, Op{Op: OpAdd, Path: "a", Value: 1.0}, ErrInvalidPath},
		{"leading zero index", map[string]any{"a": []any{1.0}}, Op{Op: OpReplace, Path: "/a/01", Value: 2.0}, ErrInvalidPath},
		{"negative index", map[string]any{"a": []any{1.0}}, Op{Op: OpReplace, Path: "/a/-1", Value: 2.0}, ErrInvalidPath},
		{"missing intermediate", map[string]any{}, Op{Op: OpAdd, Path: "/x/y", Value: 1.0}, ErrPathNotFound},
		{"index out of bounds", map[string]any{"a": []any{1.0}}, Op{Op: OpRemove, Path: "/a/5"}, ErrPathNotFound},
		{"replace missing key", map[string]any{}, Op{Op: OpReplace, Path: "/k", Value: 1.0}, ErrPathNotFound},
		{"remove missing key", map[string]any{}, Op{Op: OpRemove, Path: "/k"}, ErrPathNotFound},
		{"add existing key", map[string]any{"k": 1.0}, Op{Op: OpAdd, Path: "/k", Value: 2.0}, ErrInvalidOp},
		{"unknown op", map[string]any{}, Op{Op: "move", Path: "/k"}, ErrInvalidOp},
		{"remove root", map[string]any{}, Op{Op: OpRemove, Path: ""}, ErrInvalidOp},
		{"descend into scalar", map[string]any{"s": "str"}, Op{Op: OpAdd, Path: "/s/x", Value: 1.0}, ErrTypeMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Patch{tc.op}
			before := deepCopy(tc.doc)
			t.Logf("input doc=%v op=%+v", tc.doc, tc.op)
			_, err := Apply(tc.doc, p)
			t.Logf("err=%v verdict=reject", err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if !deepEqual(tc.doc, before) {
				t.Fatalf("doc mutated after failure: %v", tc.doc)
			}
			t.Logf("verdict: doc unchanged after failure")
		})
	}
}

// TestApplyAtomic 多操作补丁中途失败时整份失败，文档不变。
func TestApplyAtomic(t *testing.T) {
	doc := map[string]any{"a": 1.0, "b": 2.0}
	p := Patch{
		{Op: OpReplace, Path: "/a", Value: 100.0},
		{Op: OpRemove, Path: "/b"},
		{Op: OpRemove, Path: "/ghost"}, // 第三条失败
	}
	before := deepCopy(doc)
	t.Logf("input doc=%v", doc)
	logPatch(t, p)
	_, err := Apply(doc, p)
	t.Logf("err=%v", err)
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("want ErrPathNotFound, got %v", err)
	}
	if !deepEqual(doc, before) {
		t.Fatalf("doc mutated after atomic failure: %v", doc)
	}
	t.Logf("verdict: whole patch rejected, doc unchanged")
}

// TestApplyTooLarge 补丁超限整体拒绝。
func TestApplyTooLarge(t *testing.T) {
	p := make(Patch, MaxOps+1)
	for i := range p {
		p[i] = Op{Op: OpAdd, Path: fmt.Sprintf("/k%d", i), Value: i}
	}
	t.Logf("input patch ops=%d limit=%d", len(p), MaxOps)
	_, err := Apply(map[string]any{}, p)
	t.Logf("err=%v verdict=reject", err)
	if !errors.Is(err, ErrPatchTooLarge) {
		t.Fatalf("want ErrPatchTooLarge, got %v", err)
	}
}

// TestGenerateTooLarge 生成结果超限时报错。
func TestGenerateTooLarge(t *testing.T) {
	newDoc := make(map[string]any, MaxOps+1)
	for i := 0; i <= MaxOps; i++ {
		newDoc[fmt.Sprintf("k%05d", i)] = i
	}
	t.Logf("input new keys=%d limit=%d", len(newDoc), MaxOps)
	_, err := Generate(map[string]any{}, newDoc)
	t.Logf("err=%v verdict=reject", err)
	if !errors.Is(err, ErrPatchTooLarge) {
		t.Fatalf("want ErrPatchTooLarge, got %v", err)
	}
}

// TestApplyDoesNotMutateInputs 应用后传入文档与补丁保持原值与深度。
func TestApplyDoesNotMutateInputs(t *testing.T) {
	shared := map[string]any{"nested": []any{1.0, 2.0}}
	doc := map[string]any{"keep": shared}
	p := Patch{
		{Op: OpReplace, Path: "/keep/nested/0", Value: 9.0},
		{Op: OpAdd, Path: "/extra", Value: shared},
	}
	docBefore := deepCopy(doc)
	patchBefore := make(Patch, len(p))
	for i, op := range p {
		patchBefore[i] = Op{Op: op.Op, Path: op.Path, Value: deepCopy(op.Value)}
	}
	t.Logf("input doc=%v depth=%d", doc, depth(doc))
	logPatch(t, p)

	got, err := Apply(doc, p)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if !deepEqual(doc, docBefore) {
		t.Fatalf("doc mutated: %v", doc)
	}
	for i := range p {
		if p[i].Op != patchBefore[i].Op || p[i].Path != patchBefore[i].Path ||
			!deepEqual(p[i].Value, patchBefore[i].Value) {
			t.Fatalf("patch mutated at %d: %+v", i, p[i])
		}
	}
	// 结果与输入不共享引用：改结果不影响原文档。
	got.(map[string]any)["keep"].(map[string]any)["nested"].([]any)[0] = -1.0
	if !deepEqual(doc, docBefore) {
		t.Fatalf("result shares references with input doc")
	}
	t.Logf("verdict: doc and patch unchanged, result independent")
}

// TestConcurrent 多执行体并发调用生成、应用与自检。
func TestConcurrent(t *testing.T) {
	oldDoc := map[string]any{
		"a": map[string]any{"x": 1.0, "y": []any{1.0, 2.0}},
		"b": "keep",
	}
	newDoc := map[string]any{
		"a": map[string]any{"x": 2.0, "z": true},
		"b": "keep",
		"c": nil,
	}
	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan error, workers*3)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p, err := Generate(oldDoc, newDoc)
			if err != nil {
				errs <- err
				return
			}
			got, err := Apply(oldDoc, p)
			if err != nil {
				errs <- err
				return
			}
			if !deepEqual(got, newDoc) {
				errs <- errors.New("round-trip mismatch")
				return
			}
			if err := Verify(oldDoc, newDoc); err != nil {
				errs <- err
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent error: %v", err)
	}
	t.Logf("verdict: %d workers x (generate+apply+verify) passed under -race", workers)
}

// TestVerify 自检覆盖嵌套往返。
func TestVerify(t *testing.T) {
	oldDoc := map[string]any{"n": map[string]any{"deep": map[string]any{"v": []any{1.0}}}}
	newDoc := map[string]any{"n": map[string]any{"deep": map[string]any{"v": []any{2.0, 3.0}, "w": "added"}}}
	t.Logf("input old=%v new=%v", oldDoc, newDoc)
	if err := Verify(oldDoc, newDoc); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	t.Logf("verdict: verify ok (round-trip equal, depth matched)")
}
