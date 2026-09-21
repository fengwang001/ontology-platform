package ontology

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

// 异参数 Merge 必须返回可判定错误，并携带两侧的 (m, k)。
func TestMergeParamMismatch(t *testing.T) {
	a, err := New(10000, 0.01)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b, err := New(20000, 0.01) // m 不同
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before := a.Bytes()
	err = a.Merge(b)
	if !errors.Is(err, ErrParamMismatch) {
		t.Fatalf("err = %v, want ErrParamMismatch", err)
	}
	var me *MismatchError
	if !errors.As(err, &me) {
		t.Fatalf("err is not *MismatchError: %T", err)
	}
	if me.LeftM != a.M() || me.LeftK != a.K() || me.RightM != b.M() || me.RightK != b.K() {
		t.Fatalf("mismatch detail = %+v, want left (%d,%d) right (%d,%d)",
			me, a.M(), a.K(), b.M(), b.K())
	}
	if !bytes.Equal(a.Bytes(), before) {
		t.Fatal("failed Merge modified the receiver")
	}
}

// 同参数 Merge 的结果必须与"两批元素插入同一过滤器"逐字节相同，
// 且不得修改两个源过滤器。
func TestMergeEqualsCombinedInsert(t *testing.T) {
	const half = 5000
	f1, _ := New(testN, testP)
	f2, _ := New(testN, testP)
	combined, _ := New(testN, testP)
	for i := uint64(0); i < half; i++ {
		f1.Add(elem("m1", i))
		combined.Add(elem("m1", i))
	}
	for i := uint64(0); i < half; i++ {
		f2.Add(elem("m2", i))
		combined.Add(elem("m2", i))
	}
	f1Before, f2Before := f1.Bytes(), f2.Bytes()
	if err := f1.Merge(f2); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !bytes.Equal(f1.Bytes(), combined.Bytes()) {
		t.Fatal("merged bits differ from combined-insert bits")
	}
	if !bytes.Equal(f2.Bytes(), f2Before) {
		t.Fatal("Merge modified the source filter")
	}
	if bytes.Equal(f1.Bytes(), f1Before) {
		t.Fatal("Merge had no effect on the receiver")
	}
	// 合并后的估计元素个数与真实值（2*half = 10000）相对误差须在 10% 以内。
	est := f1.EstimateCount()
	if rel := math.Abs(est-2*half) / (2 * half); rel > 0.10 {
		t.Fatalf("EstimateCount after merge = %.1f, rel err %.2f%% > 10%%", est, rel*100)
	}
}

// EstimateCount 在 n=10000、p=0.01 数据上的相对误差须在 10% 以内。
func TestEstimateCountAccuracy(t *testing.T) {
	f, err := New(testN, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := f.EstimateCount(); got != 0 {
		t.Fatalf("empty EstimateCount = %v, want 0", got)
	}
	for i := uint64(0); i < testN; i++ {
		f.Add(elem("est", i))
	}
	est := f.EstimateCount()
	rel := math.Abs(est-testN) / testN
	if rel > 0.10 {
		t.Fatalf("EstimateCount = %.1f, relative error %.2f%% > 10%%", est, rel*100)
	}
	t.Logf("EstimateCount = %.1f (true %d, rel err %.2f%%)", est, testN, rel*100)
}
