package bloom

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

// 异参数 Merge 必须返回可用 errors.Is 判定的错误，且信息中带两边的 (m,k)。
func TestMergeMismatch(t *testing.T) {
	a := newTestFilter(t) // n=10000, p=0.01
	b, err := New(20000, 0.01)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.M() == b.M() && a.K() == b.K() {
		t.Fatal("test premise broken: params should differ")
	}
	_, err = Merge(a, b)
	if !errors.Is(err, ErrMismatch) {
		t.Fatalf("err = %v, want ErrMismatch", err)
	}
	wantL := fmt.Sprintf("m=%d,k=%d", a.M(), a.K())
	wantR := fmt.Sprintf("m=%d,k=%d", b.M(), b.K())
	if !strings.Contains(err.Error(), wantL) || !strings.Contains(err.Error(), wantR) {
		t.Fatalf("error %q should contain %q and %q", err, wantL, wantR)
	}
}

// Merge 结果必须等于把两批元素插入同一个过滤器，且不修改源。
func TestMergeEqualsCombinedInsert(t *testing.T) {
	a := newTestFilter(t)
	b := newTestFilter(t)
	for i := 0; i < testN; i++ {
		a.Add(elem("left", i))
		b.Add(elem("right", i))
	}
	aBefore := a.Bytes()
	bBefore := b.Bytes()

	merged, err := Merge(a, b)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	combined := newTestFilter(t)
	for i := 0; i < testN; i++ {
		combined.Add(elem("left", i))
		combined.Add(elem("right", i))
	}
	if !bytes.Equal(merged.Bytes(), combined.Bytes()) {
		t.Fatal("merged bits differ from combined insert")
	}
	if !bytes.Equal(a.Bytes(), aBefore) || !bytes.Equal(b.Bytes(), bBefore) {
		t.Fatal("Merge modified a source filter")
	}
}

// 合并后 EstimateCount 与真实值（20000）的相对误差须在 10% 以内。
func TestEstimateCount(t *testing.T) {
	a := newTestFilter(t)
	b := newTestFilter(t)
	for i := 0; i < testN; i++ {
		a.Add(elem("left", i))
		b.Add(elem("right", i))
	}
	merged, err := Merge(a, b)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	const want = 2 * testN
	est := merged.EstimateCount()
	rel := math.Abs(est-want) / want
	t.Logf("EstimateCount=%.1f, true=%d, rel err=%.4f", est, want, rel)
	if rel > 0.10 {
		t.Fatalf("relative error %.4f exceeds 10%%", rel)
	}
}
