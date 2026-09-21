package ontology

import (
	"bytes"
	"math/rand"
	"testing"
)

// TestShuffledOrderSameBytes 同一批元素以两种不同顺序插入，
// 最终位数组必须逐字节相同。
func TestShuffledOrderSameBytes(t *testing.T) {
	const n = 5000
	f1, err := New(n, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	for i := 0; i < n; i++ {
		f1.Add(insElem(i))
	}

	// 固定种子的洗牌只决定插入顺序，不影响过滤器内部状态。
	idx := rand.New(rand.NewSource(42)).Perm(n)
	f2, err := New(n, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	for _, i := range idx {
		f2.Add(insElem(i))
	}

	if !bytes.Equal(f1.Bytes(), f2.Bytes()) {
		t.Fatal("bit arrays differ after shuffled insertion order")
	}
}

// TestRepeatedAddSameBytes 同一元素重复插入一百万次，位数组
// 与只插一次逐字节相同（Add 幂等）。
func TestRepeatedAddSameBytes(t *testing.T) {
	f1, err := New(1000, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	f1.Add(insElem(7))

	f2, err := New(1000, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	for i := 0; i < 1000000; i++ {
		f2.Add(insElem(7))
	}

	if !bytes.Equal(f1.Bytes(), f2.Bytes()) {
		t.Fatal("bit array changed after 1e6 repeated adds of one element")
	}
}
