package codec

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/ord"
)

var orders = []ord.ByteOrder{ord.BigEndian, ord.LittleEndian}

func fit(vals []int64, w int) []int64 {
	out := make([]int64, len(vals))
	for i, v := range vals {
		out[i] = v << (64 - 8*w) >> (64 - 8*w) // 截到 w 字节有符号范围
	}
	return out
}

// 不变量 1：任意宽度与字节序，Decode(Encode(vals)) == vals。
func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(694))
	for _, o := range orders {
		for _, w := range []int{2, 4, 8} {
			for n := 1; n <= 6; n++ {
				vals := fit([]int64{0, 1, -1, rng.Int63(), -rng.Int63(), rng.Int63()}, w)[:n]
				got, err := Decode(o, w, Encode(o, w, vals))
				if err != nil || len(got) != len(vals) {
					t.Fatalf("order=%d w=%d: err=%v len=%d want %d", o, w, err, len(got), len(vals))
				}
				for i := range vals {
					if got[i] != vals[i] {
						t.Fatalf("order=%d w=%d i=%d: got %d want %d", o, w, i, got[i], vals[i])
					}
				}
			}
		}
	}
}

// 不变量 4：宽度非法与长度不对齐是互不相同的错误；拒绝即 (nil, error)，之后可正常使用。
func TestRejectTotal(t *testing.T) {
	cases := []struct {
		name string
		w    int
		buf  []byte
		want error
	}{
		{"width 0", 0, nil, ErrWidth},
		{"width 3", 3, []byte{1, 2, 3}, ErrWidth},
		{"width 16", 16, make([]byte, 16), ErrWidth},
		{"misaligned 3/2", 2, []byte{1, 2, 3}, ErrAlign},
		{"misaligned 9/4", 4, make([]byte, 9), ErrAlign},
		{"misaligned 7/8", 8, make([]byte, 7), ErrAlign},
	}
	for _, tc := range cases {
		got, err := Decode(ord.BigEndian, tc.w, tc.buf)
		if !errors.Is(err, tc.want) || got != nil {
			t.Fatalf("%s: got (%v, %v), want (nil, %v)", tc.name, got, err, tc.want)
		}
	}
	if errors.Is(ErrWidth, ErrAlign) || errors.Is(ErrAlign, ErrWidth) {
		t.Fatal("ErrWidth and ErrAlign must be distinct")
	}
	if Encode(ord.BigEndian, 5, []int64{1}) != nil {
		t.Fatal("Encode with invalid width must return nil")
	}
	// 被拒后仍可正常使用
	vals := []int64{1, -2, 3}
	got, err := Decode(ord.LittleEndian, 2, Encode(ord.LittleEndian, 2, vals))
	if err != nil || len(got) != 3 || got[0] != 1 || got[1] != -2 || got[2] != 3 {
		t.Fatalf("unusable after rejection: %v %v", got, err)
	}
}

// 复杂度：游标依次解码 m 个 uint64，总字节检查数 == 缓冲长度，回看数恒为 0。
func TestZeroRescan(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		vals := make([]int64, m)
		for i := range vals {
			vals[i] = int64(i)*2654435761 - 12345
		}
		buf := Encode(ord.LittleEndian, 8, vals)
		c := cursor{order: ord.LittleEndian, width: 8, buf: buf}
		for k := 0; k < m; k++ {
			if got := c.next(); got != vals[k] {
				t.Fatalf("m=%d k=%d: got %d want %d", m, k, got, vals[k])
			}
			if c.rescans != 0 {
				t.Fatalf("m=%d k=%d: rescans=%d, want 0 (O(1) direct offset)", m, k, c.rescans)
			}
		}
		if c.checked != len(buf) {
			t.Fatalf("m=%d: checked=%d, want len(buf)=%d (each byte read exactly once)", m, c.checked, len(buf))
		}
	}
}

// 并发：N 路并发 Decode 同一段只读字节结果一致；N 路并发 Encode 与串行一致。
func TestConcurrent(t *testing.T) {
	const n = 32
	rng := rand.New(rand.NewSource(7))
	base := make([]int64, 256)
	for i := range base {
		base[i] = rng.Int63()
	}
	shared := Encode(ord.BigEndian, 8, base)
	want, err := Decode(ord.BigEndian, 8, shared)
	if err != nil {
		t.Fatal(err)
	}
	encIn, encWant := make([][]int64, n), make([][]byte, n)
	for g := 0; g < n; g++ {
		encIn[g] = fit([]int64{rng.Int63(), -rng.Int63(), int64(g)}, 4)
		encWant[g] = Encode(ord.LittleEndian, 4, encIn[g])
	}
	var wg sync.WaitGroup
	fail := make(chan string, 2*n)
	for g := 0; g < n; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			got, err := Decode(ord.BigEndian, 8, shared)
			if err != nil || len(got) != len(want) {
				fail <- "decode mismatch"
				return
			}
			for i := range want {
				if got[i] != want[i] {
					fail <- "decode mismatch"
					return
				}
			}
		}()
		go func(g int) {
			defer wg.Done()
			if !bytes.Equal(Encode(ord.LittleEndian, 4, encIn[g]), encWant[g]) {
				fail <- "encode mismatch"
			}
		}(g)
	}
	wg.Wait()
	close(fail)
	for msg := range fail {
		t.Fatal(msg)
	}
}
