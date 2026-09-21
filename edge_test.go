package ontology

import (
	"bytes"
	"errors"
	"testing"
)

// 三类非法构造参数：n<=0、p<=0、p>=1，都必须返回可判定错误。
func TestInvalidParams(t *testing.T) {
	if _, err := New(0, 0.01); !errors.Is(err, ErrInvalidN) {
		t.Fatalf("New(0, 0.01) err = %v, want ErrInvalidN", err)
	}
	for _, p := range []float64{0, -0.5} {
		if _, err := New(100, p); !errors.Is(err, ErrInvalidP) {
			t.Fatalf("New(100, %v) err = %v, want ErrInvalidP", p, err)
		}
	}
	for _, p := range []float64{1, 1.5} {
		if _, err := New(100, p); !errors.Is(err, ErrInvalidP) {
			t.Fatalf("New(100, %v) err = %v, want ErrInvalidP", p, err)
		}
	}
}

// 空过滤器对任何元素都必须返回假。
func TestEmptyFilterAlwaysFalse(t *testing.T) {
	f, err := New(100, 0.01)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, e := range [][]byte{nil, {}, []byte("a"), elem("x", 1)} {
		if f.MayContain(e) {
			t.Fatalf("empty filter returned true for %q", e)
		}
	}
}

// 空切片与 nil 必须被当作同一个合法元素。
func TestNilEqualsEmptySlice(t *testing.T) {
	f, err := New(100, 0.01)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.Add(nil)
	if !f.MayContain([]byte{}) {
		t.Fatal("added nil, but empty slice not found")
	}
	g, err := New(100, 0.01)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	g.Add([]byte{})
	if !g.MayContain(nil) {
		t.Fatal("added empty slice, but nil not found")
	}
	if !bytes.Equal(f.Bytes(), g.Bytes()) {
		t.Fatal("nil and empty slice produced different bit arrays")
	}
}

// 长度为一百万字节的元素必须能正常处理。
func TestHugeElement(t *testing.T) {
	f, err := New(100, 0.01)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	huge := make([]byte, 1_000_000)
	for i := range huge {
		huge[i] = byte(i * 31)
	}
	f.Add(huge)
	if !f.MayContain(huge) {
		t.Fatal("1MB element not found after Add")
	}
	other := make([]byte, 1_000_000)
	other[999999] = 1
	if f.MayContain(other) {
		t.Fatal("unexpected positive for a different 1MB element")
	}
}
