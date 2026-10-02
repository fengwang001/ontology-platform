package frame

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestCompressRuns(t *testing.T) {
	cases := []struct {
		n    int
		want []byte
	}{
		{3, []byte{0x80, 'a'}},
		{70, []byte{0xC3, 'a'}},
		{130, []byte{0xFF, 'a'}},
		{131, []byte{0xFF, 'a', 0x00, 'a'}},
		{132, []byte{0xFF, 'a', 0x01, 'a', 'a'}},
		{133, []byte{0xFF, 'a', 0x80, 'a'}},
	}
	for _, c := range cases {
		got := compress(bytes.Repeat([]byte{'a'}, c.n))
		if !bytes.Equal(got, c.want) {
			t.Errorf("compress('a'*%d) = % X, want % X", c.n, got, c.want)
		}
	}
}

func TestCompressLiteralBuffer(t *testing.T) {
	// 128 个互不相同的字节：字面缓冲恰满 128 立即写出。
	src := make([]byte, 128)
	for i := range src {
		src[i] = byte(i)
	}
	want := append([]byte{0x7F}, src...)
	if got := compress(src); !bytes.Equal(got, want) {
		t.Errorf("compress(128 distinct) = % X..., want 7F + 128 bytes", got[:4])
	}
	// 129 个：先写 128，再写余下 1 个。
	src = make([]byte, 129)
	for i := range src {
		src[i] = byte(i)
	}
	want = append(append([]byte{0x7F}, src[:128]...), 0x00, src[128])
	if got := compress(src); !bytes.Equal(got, want) {
		t.Errorf("compress(129 distinct) mismatch")
	}
	// 不足 3 的段进字面缓冲，与后续段拼接。
	got := compress([]byte{'x', 'y', 'z', 'z', 'z'})
	want = []byte{0x01, 'x', 'y', 0x80, 'z'}
	if !bytes.Equal(got, want) {
		t.Errorf("compress(x y z z z) = % X, want % X", got, want)
	}
	// 空输入。
	if got := compress(nil); len(got) != 0 {
		t.Errorf("compress(nil) = % X, want empty", got)
	}
}

func TestDecompressErrors(t *testing.T) {
	if _, err := decompress([]byte{0x05, 'a', 'b'}, maxUncompressed); err != ErrDecode {
		t.Errorf("truncated literal: got %v", err)
	}
	if _, err := decompress([]byte{0x80}, maxUncompressed); err != ErrDecode {
		t.Errorf("truncated repeat: got %v", err)
	}
	// 解出超过上限。
	big := bytes.Repeat([]byte{0xFF, 'a'}, 5) // 650 字节
	if _, err := decompress(big, 100); err != ErrDecode {
		t.Errorf("overflow: got %v", err)
	}
}

func TestCompressRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 500; i++ {
		n := rng.Intn(2000)
		src := make([]byte, n)
		for j := range src {
			if j > 0 && rng.Intn(2) == 0 {
				src[j] = src[j-1] // 制造游程
			} else {
				src[j] = byte(rng.Intn(256))
			}
		}
		dec, err := decompress(compress(src), maxUncompressed)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if !bytes.Equal(dec, src) {
			t.Fatalf("iter %d: round trip mismatch (n=%d)", i, n)
		}
	}
}
