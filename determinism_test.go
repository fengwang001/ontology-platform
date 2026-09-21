package ontology

import (
	"bytes"
	"math/rand"
	"testing"
)

// 同一批元素以任意顺序插入，位数组必须逐字节相同。
func TestInsertOrderDeterminism(t *testing.T) {
	const n = 10000
	base, err := New(testN, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := uint64(0); i < n; i++ {
		base.Add(elem("det", i))
	}

	// 用固定种子的 rand 打乱顺序（打乱方式不影响被测对象的确定性）。
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 2; trial++ {
		order := rng.Perm(n)
		shuffled, err := New(testN, testP)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		for _, idx := range order {
			shuffled.Add(elem("det", uint64(idx)))
		}
		if !bytes.Equal(base.Bytes(), shuffled.Bytes()) {
			t.Fatalf("trial %d: bit arrays differ under shuffled insert order", trial)
		}
	}
}

// 同一元素重复插入一百万次，位数组与只插一次逐字节相同。
func TestRepeatedInsertDeterminism(t *testing.T) {
	once, err := New(testN, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	many, err := New(testN, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := elem("dup", 7)
	once.Add(e)
	for i := 0; i < 1_000_000; i++ {
		many.Add(e)
	}
	if !bytes.Equal(once.Bytes(), many.Bytes()) {
		t.Fatal("bit array changed after 1e6 repeated inserts of same element")
	}
}
