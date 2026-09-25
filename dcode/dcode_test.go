package dcode

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/bits"
)

// naiveEncode 独立朴素参照：手算 gamma(L)+值层位串，再大端打包。
func naiveEncode(vs []int64) []byte {
	s := ""
	for _, v := range vs {
		bn := fmt.Sprintf("%b", v)
		bl := fmt.Sprintf("%b", len(bn))
		s += strings.Repeat("0", len(bl)-1) + bl + bn[1:] // (k-1)个0 + gamma(L) + 去最高位值层
	}
	s += strings.Repeat("0", (8-len(s)%8)%8) // 末字节低位补 0
	out := make([]byte, len(s)/8)
	for i := 0; i < len(s); i += 8 {
		for j := 0; j < 8; j++ {
			if s[i+j] == '1' {
				out[i/8] |= 1 << (7 - j)
			}
		}
	}
	return out
}

func TestNaiveReference(t *testing.T) {
	cases := [][]int64{{1}, {1, 2, 4, 10, 16}, {3, 1 << 40, 255, 1 << 62}}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 20; i++ {
		vs := make([]int64, rng.Intn(30)+1)
		for j := range vs {
			vs[j] = rng.Int63n(1<<50) + 1
		}
		cases = append(cases, vs)
	}
	for _, vs := range cases {
		got, err := Encode(vs)
		if err != nil || !bytes.Equal(got, naiveEncode(vs)) {
			t.Fatalf("Encode %v 与朴素参照不一致: %x", vs, got)
		}
		if back, err := Decode(got); err != nil || fmt.Sprint(back) != fmt.Sprint(vs) {
			t.Fatalf("Decode 往返不一致: %v -> %v", vs, back)
		}
	}
}

func TestChunking(t *testing.T) {
	enc, _ := Encode([]int64{1, 2, 4, 10, 16, 1 << 40})
	want, _ := Decode(enc)
	for i := 0; i <= len(enc); i++ {
		sd := NewStreamDecoder()
		sd.Feed(enc[:i])
		sd.Feed(enc[i:])
		if got, err := sd.Decode(); err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("切点 %d 结果不一致: %v", i, got)
		}
	}
}

func TestDeterministic(t *testing.T) {
	vs := []int64{9, 1 << 33, 2, 1 << 62}
	a, _ := Encode(vs)
	for i := 0; i < 5; i++ {
		if b, _ := Encode(vs); !bytes.Equal(a, b) {
			t.Fatal("多次编码结果不同")
		}
	}
	if _, err := Decode(a); err != nil { // γ 无前缀+值层定长 ⇒ δ 流唯一可解
		t.Fatal("唯一可解性被破坏")
	}
}

func TestFailuresNoPartial(t *testing.T) {
	enc, _ := Encode([]int64{1, 2, 4, 10, 16})
	if _, err := Encode([]int64{0}); !errors.Is(err, ErrNonPositive) {
		t.Fatal("0 未报 ErrNonPositive")
	}
	if _, err := Encode([]int64{-3}); !errors.Is(err, ErrNonPositive) {
		t.Fatal("负数未报 ErrNonPositive")
	}
	if got, err := Decode(enc[:3]); !errors.Is(err, ErrTruncated) || got != nil {
		t.Fatal("截断未报 ErrTruncated 或留下部分输出")
	}
	w := bits.NewWriter()
	w.WriteBits(0, 6)
	w.WriteBits(64, 7)
	w.WriteBits(0, 63)
	if got, err := Decode(w.Bytes()); !errors.Is(err, ErrOverflow) || got != nil {
		t.Fatal("L=64 未报 ErrOverflow 或留下部分输出")
	}
	if errors.Is(ErrTruncated, ErrOverflow) || errors.Is(ErrNonPositive, ErrTruncated) {
		t.Fatal("哨兵错误不互不相同")
	}
	if _, err := Decode(enc); err != nil {
		t.Fatal("失败后状态被污染") // 纯函数：被拒后仍可继续使用
	}
}

func TestDecodeCounter(t *testing.T) {
	const want = 11 + 40 // gamma(41) 11 位 + 值层 40 位
	for _, m := range []int{100, 1000, 10000} {
		vs := make([]int64, m+1)
		for i := range vs {
			vs[i] = 1
		}
		vs[m] = 1 << 40
		b, _ := Encode(vs)
		r := bits.NewReader(b)
		var checked uint64
		for i := 0; i <= m; i++ {
			var err error
			if _, checked, err = decodeOneCount(r); err != nil {
				t.Fatal(err)
			}
		}
		if checked != want {
			t.Fatalf("m=%d: 解码 2^40 检查 %d 位, 期望 %d", m, checked, want)
		}
	}
}
func TestConcurrent(t *testing.T) {
	in := []int64{5, 1 << 40, 8, 1 << 62, 1}
	serial, _ := Encode(in)
	serialDec, _ := Decode(serial)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				b, _ := Encode(in)
				d, _ := Decode(b)
				if !bytes.Equal(b, serial) || fmt.Sprint(d) != fmt.Sprint(serialDec) {
					t.Error("并发结果与串行不一致")
				}
			}
		}()
	}
	wg.Wait()
}
