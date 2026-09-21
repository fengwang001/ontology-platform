package ontology

import (
	"bytes"
	"testing"
)

// TestNilAndEmptyAreSameElement nil 与空切片必须被视为同一个
// 合法元素：插入 nil 后空切片命中，反之亦然，且位数组一致。
func TestNilAndEmptyAreSameElement(t *testing.T) {
	f1, err := New(100, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	f1.Add(nil)
	if !f1.MayContain([]byte{}) {
		t.Fatal("empty slice not found after adding nil")
	}

	f2, err := New(100, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	f2.Add([]byte{})
	if !f2.MayContain(nil) {
		t.Fatal("nil not found after adding empty slice")
	}

	if !bytes.Equal(f1.Bytes(), f2.Bytes()) {
		t.Fatal("nil and empty slice produced different bit arrays")
	}
}

// TestHugeElement 长度为一百万字节的元素必须能正常插入与查询。
func TestHugeElement(t *testing.T) {
	huge := make([]byte, 1000000)
	for i := range huge {
		huge[i] = byte(i * 31)
	}
	f, err := New(10, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	f.Add(huge)
	if !f.MayContain(huge) {
		t.Fatal("1MB element not found after Add")
	}
	// 改动一个字节后应判为不存在（大概率；此处翻转大量位使其
	// 必然是不同的元素，且单次未插入查询假阳性概率仅约 1%）。
	other := bytes.Clone(huge)
	for i := 0; i < len(other); i += 997 {
		other[i] ^= 0xFF
	}
	if f.MayContain(other) {
		t.Fatal("modified 1MB element unexpectedly reported present")
	}
}
