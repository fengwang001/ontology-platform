package patch

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

func sampleVersions() (any, any) {
	source := map[string]any{
		"name": "本体",
		"meta": map[string]any{
			"tags":  []any{"a", "b/b", "c~d"},
			"count": float64(3),
			"null":  nil,
		},
		"nested": map[string]any{"deep": []any{map[string]any{"k": "v"}}},
		"gone":   1,
	}
	target := map[string]any{
		"name": "本体-v2",
		"meta": map[string]any{
			"tags":  []any{"a", "x"},
			"count": float64(4),
			"null":  "now-string",
		},
		"nested": map[string]any{"deep": []any{map[string]any{"k": "v2", "q": 1}}},
		"new":    nil,
	}
	return source, target
}

// 往返相等：Generate(source) -> Apply -> 深度等于 target；
// 应用前后传入的 doc 与补丁深度不变。
func TestRoundTrip(t *testing.T) {
	source, target := sampleVersions()

	p, err := Generate(source, target, WithDecisionLogger(func(d Decision) {
		t.Logf("step path=%q kind=%s srcType=%s dstType=%s op=%+v",
			d.Path, d.Kind, d.SourceType, d.TargetType, d.Op)
	}))
	if err != nil {
		t.Fatal(err)
	}
	for i, op := range p.Ops {
		t.Logf("patch[%d] = %+v", i, op)
	}

	srcSnap, _ := deepCopy(source)
	patchSnap := deepCopyOps(p.Ops)

	got, err := Apply(source, p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !reflect.DeepEqual(got, target) {
		t.Fatalf("round-trip mismatch:\n got %#v\nwant %#v", got, target)
	}
	if !reflect.DeepEqual(source, srcSnap) {
		t.Fatal("Apply mutated the caller's document")
	}
	if !reflect.DeepEqual(p.Ops, patchSnap) {
		t.Fatal("Apply mutated the caller's patch")
	}

	// 二次应用必须可复现：相同输入产生相同结果。
	again, err := Apply(srcSnap, p)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatal("patch is not reproducible across applications")
	}

	// 目标版本上生成空补丁，且空补丁应用为恒等。
	idPatch, err := Generate(target, target)
	if err != nil || len(idPatch.Ops) != 0 {
		t.Fatalf("identical versions yield %#v, %v", idPatch.Ops, err)
	}
	idResult, err := Apply(target, Patch{})
	if err != nil || !reflect.DeepEqual(idResult, target) {
		t.Fatalf("empty patch: %#v %v", idResult, err)
	}
}

// 特殊字符键的真实端到端往返（编码 -> 生成 -> 应用）。
func TestSpecialKeyRoundTrip(t *testing.T) {
	source := map[string]any{"a/b~c": map[string]any{"x": 1}}
	target := map[string]any{"a/b~c": map[string]any{"x": 2, "y/z~w": nil}}

	p, err := Generate(source, target)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"/a~0b~1c/x", "/a~0b~1c/y~0z~1w"}
	var addSeen bool
	for i, op := range p.Ops {
		t.Logf("special-key patch[%d] = %+v", i, op)
		if op.Path == wantPaths[0] && op.Type != "replace" {
			t.Fatalf("x must be replaced, got %s", op.Type)
		}
		if op.Path == wantPaths[1] {
			addSeen = true
		}
	}
	if !addSeen {
		t.Fatalf("escaped add path missing, ops = %#v", p.Ops)
	}
	got, err := Apply(source, p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !reflect.DeepEqual(got, target) {
		t.Fatalf("special-key round-trip:\n got %#v\nwant %#v", got, target)
	}
}

// SelfCheck：往返相等 + 输入不被修改，可被多个执行体并发调用。
func TestSelfCheck(t *testing.T) {
	source, target := sampleVersions()
	p, err := SelfCheck(context.Background(), source, target)
	if err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if len(p.Ops) == 0 {
		t.Fatal("SelfCheck patch unexpectedly empty")
	}
}

// 根不是对象时（标量/数组/null）整体替换；根上 add/remove 非法。
func TestRootReplacement(t *testing.T) {
	cases := []struct {
		source, target any
	}{
		{[]any{1, 2}, []any{3}},
		{1, map[string]any{"a": 1}},
		{nil, "x"},
		{map[string]any{"a": 1}, []any{1}},
	}
	for _, tc := range cases {
		p, err := Generate(tc.source, tc.target)
		if err != nil {
			t.Fatalf("Generate(%#v, %#v): %v", tc.source, tc.target, err)
		}
		if len(p.Ops) != 1 || p.Ops[0].Type != "replace" || p.Ops[0].Path != "" {
			t.Fatalf("root diff must be single root replace, got %#v", p.Ops)
		}
		got, err := Apply(tc.source, p)
		if err != nil || !reflect.DeepEqual(got, tc.target) {
			t.Fatalf("root round-trip got %#v err %v", got, err)
		}
	}

	for _, op := range []Op{OpAdd("", 1), OpRemove("")} {
		if _, err := Apply(map[string]any{"a": 1}, Patch{Ops: []Op{op}}); !errors.Is(err, ErrInvalidOperation) {
			t.Fatalf("root %s err = %v, want ErrInvalidOperation", op.Type, err)
		}
	}
}

func TestConcurrentGenerateApplySelfCheck(t *testing.T) {
	source, target := sampleVersions()
	const workers = 32
	const iterations = 20

	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				label := strconv.Itoa(id) + ":" + strconv.Itoa(i)
				p, err := Generate(source, target, WithDecisionLogger(func(Decision) {}))
				if err != nil {
					errCh <- errors.New(label + " generate: " + err.Error())
					return
				}
				got, err := Apply(source, p)
				if err != nil {
					errCh <- errors.New(label + " apply: " + err.Error())
					return
				}
				if !reflect.DeepEqual(got, target) {
					errCh <- errors.New(label + " round-trip mismatch")
					return
				}
				if _, err := SelfCheck(context.Background(), source, target); err != nil {
					errCh <- errors.New(label + " selfcheck: " + err.Error())
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}
	// 并发之后源文档必须原封不动。
	srcNow, _ := sampleVersions()
	if !reflect.DeepEqual(source, srcNow) {
		t.Fatal("shared source was mutated under concurrent use")
	}
}
