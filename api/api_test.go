package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/ops"
)

func randStr(r *rand.Rand, n int) string {
	const alpha = "abcd"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}

// 故障注入：三类错误可判定且互不相同；被拒后状态不变、仍可正常使用。
func TestFaultInjection(t *testing.T) {
	if _, err := api.New(-1); !errors.Is(err, api.ErrNegativeK) {
		t.Fatalf("New(-1) = %v; 应为 ErrNegativeK", err)
	}
	if api.ErrNegativeK == api.ErrTooLong || api.ErrTooLong == api.ErrExceedsCap || api.ErrNegativeK == api.ErrExceedsCap {
		t.Fatal("三类哨兵错误必须互不相同")
	}
	c, err := api.New(2)
	if err != nil {
		t.Fatal(err)
	}
	before, err := c.Distance("abc", "yabd")
	if err != nil || before != 2 {
		t.Fatalf("前置计算 = %d,%v", before, err)
	}
	if _, err := c.Distance(string(make([]byte, 1<<20)), "x"); !errors.Is(err, api.ErrTooLong) {
		t.Fatalf("超长输入 = %v; 应为 ErrTooLong", err)
	}
	c1, _ := api.New(1)
	if _, err := c1.Distance("abc", "yabd"); !errors.Is(err, api.ErrExceedsCap) {
		t.Fatalf("超上限 = %v; 应为 ErrExceedsCap", err)
	}
	if _, err := c1.EditScript("abc", "yabd"); !errors.Is(err, api.ErrExceedsCap) {
		t.Fatalf("EditScript 超上限 = %v; 应为 ErrExceedsCap", err)
	}
	// 不变量 4：拒绝不留痕，之后结果与之前逐对相同。
	after, err := c.Distance("abc", "yabd")
	if err != nil || after != before {
		t.Fatalf("被拒后状态改变: before=%d after=%d err=%v", before, after, err)
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("被拒后 SelfCheck 失败: %v", err)
	}
}

// 不变量 2：脚本应用后 a 变 b，长度恰等于距离。
func TestEditScriptValid(t *testing.T) {
	c, _ := api.New(12)
	cases := [][2]string{{"", ""}, {"", "abc"}, {"abc", ""}, {"abc", "yabd"}, {"kitten", "sitting"}}
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 200; i++ {
		cases = append(cases, [2]string{randStr(r, r.Intn(10)), randStr(r, r.Intn(10))})
	}
	for _, p := range cases {
		d, err := c.Distance(p[0], p[1])
		if err != nil {
			t.Fatalf("Distance(%q,%q): %v", p[0], p[1], err)
		}
		sc, err := c.EditScript(p[0], p[1])
		if err != nil {
			t.Fatalf("EditScript(%q,%q): %v", p[0], p[1], err)
		}
		if len(sc) != d {
			t.Errorf("(%q,%q): 脚本长度 %d != 距离 %d", p[0], p[1], len(sc), d)
		}
		out, err := ops.Apply(p[0], sc)
		if err != nil || out != p[1] {
			t.Errorf("(%q,%q): 应用脚本得 %q,%v", p[0], p[1], out, err)
		}
	}
}

// SelfCheck 对多种 k 都必须通过。
func TestSelfCheck(t *testing.T) {
	for _, k := range []int{0, 1, 2, 5, 50} {
		c, err := api.New(k)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.SelfCheck(); err != nil {
			t.Errorf("k=%d: SelfCheck = %v", k, err)
		}
	}
}

// 并发：N 个 goroutine 对同一实例并发调用，结果与串行逐对相同。
func TestConcurrentConsistent(t *testing.T) {
	c, _ := api.New(10) // 串长 ≤ 8，真实距离必 ≤ 8，不会超上限
	r := rand.New(rand.NewSource(4))
	pairs := make([][2]string, 64)
	want := make([]int, len(pairs))
	for i := range pairs {
		pairs[i] = [2]string{randStr(r, r.Intn(9)), randStr(r, r.Intn(9))}
		d, err := c.Distance(pairs[i][0], pairs[i][1])
		if err != nil {
			t.Fatalf("串行 Distance(%v): %v", pairs[i], err)
		}
		want[i] = d
	}
	const G = 16
	got := make([][]int, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		got[g] = make([]int, len(pairs))
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i, p := range pairs {
				d, err := c.Distance(p[0], p[1])
				if err != nil {
					t.Errorf("并发 Distance(%v): %v", p, err)
					return
				}
				got[g][i] = d
			}
			if err := c.SelfCheck(); err != nil {
				t.Errorf("并发 SelfCheck: %v", err)
			}
			if _, err := c.EditScript("abc", "yabd"); err != nil {
				t.Errorf("并发 EditScript: %v", err)
			}
		}(g)
	}
	wg.Wait()
	for g := 0; g < G; g++ {
		for i := range pairs {
			if got[g][i] != want[i] {
				t.Errorf("goroutine %d 对 %v 得 %d; 串行为 %d", g, pairs[i], got[g][i], want[i])
			}
		}
	}
}
