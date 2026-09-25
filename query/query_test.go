package query

import (
	"bytes"
	"errors"
	"math/bits"
	"math/rand"
	"testing"

	"ontology/rhash"
)

// gen 生成带重复片段的随机串，使「相等子串」与碰撞压力同时存在。
func gen(rng *rand.Rand, n int) []byte {
	s := make([]byte, n)
	alphabet := []byte("ab") // 小字母表：大量相等子串，更能检验哈希判等
	for i := range s {
		s[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return s
}

func naiveLCP(s []byte, i, j int) int {
	k := 0
	for i+k < len(s) && j+k < len(s) && s[i+k] == s[j+k] {
		k++
	}
	return k
}

// TestEqualNaive 钉住不变量 1：Equal 与 bytes.Equal 对任意合法区间逐组一致。
func TestEqualNaive(t *testing.T) {
	cases := []struct{ seed, n int }{
		{1, 1}, {2, 3}, {3, 50}, {4, 500}, {5, 3000},
	}
	for _, tc := range cases {
		rng := rand.New(rand.NewSource(int64(tc.seed)))
		s := gen(rng, tc.n)
		q := New(rhash.New(s))
		for m := 0; m < 300; m++ {
			l1, r1 := rng.Intn(tc.n+1), rng.Intn(tc.n+1)
			l2, r2 := rng.Intn(tc.n+1), rng.Intn(tc.n+1)
			if l1 > r1 {
				l1, r1 = r1, l1
			}
			if l2 > r2 {
				l2, r2 = r2, l2
			}
			got, err := q.Equal(l1, r1, l2, r2)
			if err != nil {
				t.Fatalf("seed=%d 合法区间出错: %v", tc.seed, err)
			}
			want := bytes.Equal(s[l1:r1], s[l2:r2])
			if got != want {
				t.Fatalf("seed=%d Equal(%d,%d,%d,%d)=%v want %v", tc.seed, l1, r1, l2, r2, got, want)
			}
		}
	}
}

// TestLCPNaive 钉住不变量 3：LCP 与逐字节比较结果一致。
func TestLCPNaive(t *testing.T) {
	cases := []struct{ seed, n int }{
		{11, 1}, {12, 7}, {13, 100}, {14, 1000}, {15, 5000},
	}
	for _, tc := range cases {
		rng := rand.New(rand.NewSource(int64(tc.seed)))
		s := gen(rng, tc.n)
		q := New(rhash.New(s))
		for m := 0; m < 200; m++ {
			i, j := rng.Intn(tc.n+1), rng.Intn(tc.n+1)
			got, err := q.LCP(i, j)
			if err != nil {
				t.Fatalf("合法下标出错: %v", err)
			}
			if want := naiveLCP(s, i, j); got != want {
				t.Fatalf("n=%d LCP(%d,%d)=%d want %d", tc.n, i, j, got, want)
			}
		}
	}
}

// TestLCPComparisonBound：每次 LCP 的哈希比较次数 <= ceil(log2 n)+1。
func TestLCPComparisonBound(t *testing.T) {
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		rng := rand.New(rand.NewSource(int64(n)))
		s := gen(rng, n)
		q := New(rhash.New(s))
		limit := bits.Len(uint(n)) + 1
		for m := 0; m < 100; m++ {
			i, j := rng.Intn(n+1), rng.Intn(n+1)
			_, cmps, err := q.lcp(i, j)
			if err != nil || cmps > limit {
				t.Fatalf("n=%d LCP(%d,%d) cmps=%d limit=%d err=%v", n, i, j, cmps, limit, err)
			}
		}
	}
}

// TestErrorsNoTrace 钉住不变量 4：越界/未构建被整体拒绝、错误可判定且互不相同，
// 拒绝不改状态，之后仍正常可用。
func TestErrorsNoTrace(t *testing.T) {
	if errors.Is(ErrNotBuilt, ErrOutOfRange) {
		t.Fatal("两个哨兵错误必须互不相同")
	}
	s := []byte("abcabc")
	q := New(rhash.New(s))
	bad := [][4]int{
		{-1, 1, 0, 1}, {0, 1, 0, 7}, {3, 2, 0, 1}, {0, 1, 7, 7},
	}
	for _, b := range bad {
		if _, err := q.Equal(b[0], b[1], b[2], b[3]); !errors.Is(err, ErrOutOfRange) {
			t.Fatalf("Equal%v 应返回 ErrOutOfRange，得到 %v", b, err)
		}
	}
	if _, err := q.LCP(-1, 0); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("LCP 越界应返回 ErrOutOfRange，得到 %v", err)
	}
	var nilQ *Query
	if _, err := nilQ.Equal(0, 1, 0, 1); !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("零值查询器应返回 ErrNotBuilt，得到 %v", err)
	}
	if _, err := nilQ.LCP(0, 0); !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("零值查询器 LCP 应返回 ErrNotBuilt，得到 %v", err)
	}
	// 被拒之后状态不变、仍正常工作。
	if ok, err := q.Equal(0, 3, 3, 6); err != nil || !ok {
		t.Fatalf("拒绝后 Equal 应正常且为真，得到 ok=%v err=%v", ok, err)
	}
	if k, err := q.LCP(0, 0); err != nil || k != len(s) {
		t.Fatalf("拒绝后 LCP 应正常，得到 k=%d err=%v", k, err)
	}
}
