package rhash_test

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/query"
	"ontology/rhash"
)

// naive 用与实现相同的常量逐字节重算子串双哈希（参照实现）。
func naive(s []byte, l, r int) (uint64, uint64) {
	var a, b uint64
	for i := l; i < r; i++ {
		c := uint64(s[i])
		a, b = (a*rhash.B+c)%rhash.M1, (b*rhash.B+c)%rhash.M2
	}
	return a, b
}

func randBytes(rng *rand.Rand, n int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte(rng.Intn(4)) + 'a' // 小字母表，制造大量相等子串
	}
	return s
}

func span(rng *rand.Rand, n int) (int, int) {
	l, r := rng.Intn(n+1), rng.Intn(n+1)
	if l > r {
		l, r = r, l
	}
	return l, r
}

// TestAPIErrors 钉住不变量 4 与三类互不相同的哨兵：必须在任何成功 New 前先验未构建态。
func TestAPIErrors(t *testing.T) {
	if _, err := api.Equal(0, 0, 0, 0); !errors.Is(err, api.ErrNotBuilt) {
		t.Fatalf("未构建应 ErrNotBuilt，得到 %v", err)
	}
	if err := api.New(nil); !errors.Is(err, api.ErrEmptyInput) {
		t.Fatalf("空串应 ErrEmptyInput，得到 %v", err)
	}
	if _, err := api.LCP(0, 0); !errors.Is(err, api.ErrNotBuilt) {
		t.Fatalf("空串失败后应仍未构建，得到 %v", err)
	}
	if errors.Is(api.ErrEmptyInput, api.ErrNotBuilt) ||
		errors.Is(api.ErrOutOfRange, api.ErrNotBuilt) || errors.Is(api.ErrOutOfRange, api.ErrEmptyInput) {
		t.Fatal("三类哨兵错误必须互不相同")
	}
	if err := api.New([]byte("abcabc")); err != nil {
		t.Fatalf("正常 New 失败: %v", err)
	}
	if _, err := api.Equal(-1, 1, 0, 1); !errors.Is(err, api.ErrOutOfRange) {
		t.Fatalf("越界应 ErrOutOfRange，得到 %v", err)
	}
	if err := api.New(nil); !errors.Is(err, api.ErrEmptyInput) { // 被整体拒绝
		t.Fatalf("再次空串应 ErrEmptyInput，得到 %v", err)
	}
	// 被拒不留痕：旧态仍可用，自检仍通过。
	if ok, err := api.Equal(0, 3, 3, 6); err != nil || !ok {
		t.Fatalf("拒绝后查询应正常且为真，ok=%v err=%v", ok, err)
	}
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("拒绝后自检应通过: %v", err)
	}
}

// TestSubstringHash 钉住不变量 2：Hash 与逐字节重算在两个模上一致。
func TestSubstringHash(t *testing.T) {
	for _, n := range []int{1, 2, 10, 300, 5000} {
		rng := rand.New(rand.NewSource(int64(n)))
		s := randBytes(rng, n)
		h := rhash.New(s)
		for m := 0; m < 200; m++ {
			l, r := span(rng, n)
			a1, a2 := h.Hash(l, r)
			b1, b2 := naive(s, l, r)
			if a1 != b1 || a2 != b2 {
				t.Fatalf("n=%d [%d,%d) 哈希(%d,%d)!=重算(%d,%d)", n, l, r, a1, a2, b1, b2)
			}
		}
	}
}

// TestZeroRescan：多档 n、m 次不同子串 Equal 后重扫字符总数恒为 0。
// 不读取非导出计数器，只由 SelfCheck 给成败。
func TestZeroRescan(t *testing.T) {
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		rng := rand.New(rand.NewSource(int64(n + 1)))
		s := randBytes(rng, n)
		q := query.New(rhash.New(s))
		for m := 0; m < 200; m++ {
			l1, r1 := span(rng, n)
			l2, r2 := span(rng, n)
			if got, err := q.Equal(l1, r1, l2, r2); err != nil || got != bytes.Equal(s[l1:r1], s[l2:r2]) {
				t.Fatalf("n=%d Equal 出错或与参照不符: %v", n, err)
			}
		}
		if err := q.SelfCheck(); err != nil {
			t.Fatalf("n=%d 重扫计数非 0 或自检失败: %v", n, err)
		}
	}
}

// TestConcurrent：N 个 goroutine 并发 Equal/LCP，结果逐项一致（无 sleep）。
func TestConcurrent(t *testing.T) {
	if err := api.New([]byte("the quick brown fox jumps over the lazy dog dog")); err != nil {
		t.Fatal(err)
	}
	tasks := []func() any{
		func() any { ok, _ := api.Equal(0, 3, 32, 35); return ok },
		func() any { ok, _ := api.Equal(0, 9, 0, 9); return ok },
		func() any { k, _ := api.LCP(0, 32); return k },
		func() any { k, _ := api.LCP(35, 40); return k },
	}
	const N = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	all := make([][]any, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			row := make([]any, len(tasks))
			for i, task := range tasks {
				row[i] = task()
			}
			all[g] = row
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < N; g++ {
		for i := range all[0] {
			if all[g][i] != all[0][i] {
				t.Fatalf("goroutine %d 第 %d 项 %v != %v", g, i, all[g][i], all[0][i])
			}
		}
	}
}
