package ontology

import (
	"bytes"
	"errors"
	"testing"
)

// TestMergeParamMismatch 异参数 Merge 必须返回可判定错误，
// 且错误中携带两侧的 (m, k)。
func TestMergeParamMismatch(t *testing.T) {
	a, err := New(10000, 0.01) // m=95851, k=7
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	b, err := New(1000, 0.01) // m=9586, k=7：m 不同
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	merged, err := Merge(a, b)
	if !errors.Is(err, ErrParamMismatch) {
		t.Fatalf("want ErrParamMismatch, got merged=%v err=%v", merged, err)
	}
	var pme *ParamMismatchError
	if !errors.As(err, &pme) {
		t.Fatalf("error is not *ParamMismatchError: %T", err)
	}
	if pme.AM != a.M() || pme.AK != a.K() || pme.BM != b.M() || pme.BK != b.K() {
		t.Fatalf("error carries (%d,%d)/(%d,%d), want (%d,%d)/(%d,%d)",
			pme.AM, pme.AK, pme.BM, pme.BK, a.M(), a.K(), b.M(), b.K())
	}
	t.Logf("mismatch error: %v", err)
}

// TestMergeEqualsCombinedInsert Merge 结果必须等于把两批元素
// 插入同一个过滤器，且两个源过滤器不被修改。
func TestMergeEqualsCombinedInsert(t *testing.T) {
	const (
		n     = 4000
		p     = 0.01
		split = 1500
	)
	a, err := New(n, p)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	b, err := New(n, p)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	combined, err := New(n, p)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	for i := 0; i < split; i++ {
		a.Add(insElem(i))
		combined.Add(insElem(i))
	}
	for i := split; i < n; i++ {
		b.Add(insElem(i))
		combined.Add(insElem(i))
	}
	aBefore, bBefore := a.Bytes(), b.Bytes()
	merged, err := Merge(a, b)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if !bytes.Equal(merged.Bytes(), combined.Bytes()) {
		t.Fatal("merged filter differs from combined insertion")
	}
	if !bytes.Equal(a.Bytes(), aBefore) || !bytes.Equal(b.Bytes(), bBefore) {
		t.Fatal("Merge mutated one of the source filters")
	}
	// 合并结果对两批元素都必须零假阴性。
	for i := 0; i < n; i++ {
		if !merged.MayContain(insElem(i)) {
			t.Fatalf("merged filter false negative for element %d", i)
		}
	}
}
