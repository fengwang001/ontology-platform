package bloom

import (
	"errors"
	"testing"
)

// 三类非法构造参数都必须返回可用 errors.Is 判定的错误。
func TestInvalidParams(t *testing.T) {
	cases := []struct {
		name string
		n    uint64
		p    float64
	}{
		{"n=0", 0, 0.01},
		{"p=0", 1000, 0},
		{"p<0", 1000, -0.5},
		{"p=1", 1000, 1},
		{"p>1", 1000, 1.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := New(c.n, c.p)
			if !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("err = %v, want ErrInvalidParam", err)
			}
			if f != nil {
				t.Fatal("filter should be nil on error")
			}
		})
	}
}

// 空过滤器对任何元素都返回假。
func TestEmptyFilter(t *testing.T) {
	f := newTestFilter(t)
	for i := 0; i < 1000; i++ {
		if f.MayContain(elem("any", i)) {
			t.Fatalf("empty filter claims to contain %d", i)
		}
	}
	if f.MayContain(nil) || f.MayContain([]byte{}) {
		t.Fatal("empty filter claims to contain empty element")
	}
}

// nil 与空切片是同一个合法元素。
func TestNilEqualsEmpty(t *testing.T) {
	f := newTestFilter(t)
	f.Add(nil)
	if !f.MayContain([]byte{}) {
		t.Fatal("nil and empty slice must be the same element")
	}
	g := newTestFilter(t)
	g.Add([]byte{})
	if !g.MayContain(nil) {
		t.Fatal("empty slice and nil must be the same element")
	}
}

// 长度为一百万字节的元素必须能正常处理。
func TestHugeElement(t *testing.T) {
	f := newTestFilter(t)
	huge := make([]byte, 1000000)
	for i := range huge {
		huge[i] = byte(i * 31)
	}
	f.Add(huge)
	if !f.MayContain(huge) {
		t.Fatal("1MB element lost after Add")
	}
	other := make([]byte, 1000000)
	copy(other, huge)
	other[len(other)-1] ^= 0xFF
	if f.MayContain(other) {
		t.Log("note: different 1MB element happened to be a false positive")
	}
}
