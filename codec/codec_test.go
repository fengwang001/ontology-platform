package codec

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/b64"
)

func TestBlockCount(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"Zm9v", 1},
		{"Zg==", 1},
		{"Zm9vYmFyYg==", 3},
	}
	for _, c := range cases {
		if got := BlockCount([]byte(c.in)); got != c.want {
			t.Errorf("BlockCount(%q)=%d, 应为 %d", c.in, got, c.want)
		}
	}
}

func TestDecodeBlockAt(t *testing.T) {
	enc := b64.Encode([]byte("foobarb")) // 3 块，末块 1 字节
	cases := []struct {
		k    int
		want string
	}{
		{0, "foo"},
		{1, "bar"},
		{2, "b"},
	}
	for _, c := range cases {
		got, err := DecodeBlockAt(enc, c.k)
		if err != nil || string(got) != c.want {
			t.Errorf("DecodeBlockAt(k=%d)=(%q,%v), 应为 (%q,nil)", c.k, got, err, c.want)
		}
	}
	// 末块 2 字节（1 个 =）。
	if got, err := DecodeBlockAt(b64.Encode([]byte("fo")), 0); err != nil || string(got) != "fo" {
		t.Errorf("末块 2 字节: (%q,%v)", got, err)
	}
	// 越界。
	for _, k := range []int{-1, 3, 100} {
		if _, err := DecodeBlockAt(enc, k); !errors.Is(err, ErrOutOfRange) {
			t.Errorf("k=%d: err=%v, 应为 ErrOutOfRange", k, err)
		}
	}
	// 总长非 4 倍数。
	if _, err := DecodeBlockAt([]byte("TWE"), 0); !errors.Is(err, b64.ErrLength) {
		t.Errorf("长度非法: err=%v, 应为 ErrLength", err)
	}
}

// TestDecodeBlockAtReadsConstant 证明块定位是 O(1) 定长寻址：
// 无论块总数 n 多大、取第 1 个还是第 n-1 个块，定位读取的输入字节数恒为 4。
func TestDecodeBlockAtReadsConstant(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		raw := make([]byte, 3*n) // 总长 4n，无填充
		for i := range raw {
			raw[i] = byte(r.Intn(256))
		}
		buf := b64.Encode(raw)
		if BlockCount(buf) != n {
			t.Fatalf("n=%d: BlockCount=%d", n, BlockCount(buf))
		}
		for _, k := range []int{n - 1, 1} {
			got, err := DecodeBlockAt(buf, k)
			if err != nil {
				t.Fatalf("n=%d k=%d: %v", n, k, err)
			}
			if !bytes.Equal(got, raw[3*k:3*k+3]) {
				t.Fatalf("n=%d k=%d: 块内容不符", n, k)
			}
			if c := lastRead.Load(); c != 4 {
				t.Fatalf("n=%d k=%d: 定位读取 %d 字节, 应恒为 4", n, k, c)
			}
		}
	}
}
