package api

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func mustNew(t *testing.T, s string) *String {
	t.Helper()
	st, err := New(s)
	if err != nil {
		t.Fatalf("New(%q) 意外失败: %v", s, err)
	}
	return st
}

// TestCyclicEqual 钉住不变量 3：循环等价当且仅当等长且互为旋转。
func TestCyclicEqual(t *testing.T) {
	cases := []struct {
		s, t string
		want bool
	}{
		{"baabaa", "aabaab", true},
		{"aaabb", "baaab", true},
		{"aabaa", "baaab", false}, // b 的个数 1 vs 2，不可能互为旋转
		{"abc", "bca", true},
		{"abc", "cba", false},
		{"abc", "ab", false},   // 不等长
		{"abc", "abcc", false}, // 不等长
		{"aaaa", "aaaa", true},
	}
	for _, c := range cases {
		st := mustNew(t, c.s)
		if got := st.CyclicEqual(c.t); got != c.want {
			t.Errorf("CyclicEqual(%q, %q)=%v, want %v", c.s, c.t, got, c.want)
		}
	}
	// 真值表：t 为 s 的每个旋转时必为 true。
	st := mustNew(t, "baabaa")
	for j := 0; j < len("baabaa"); j++ {
		rot := "baabaa"[j:] + "baabaa"[:j]
		if !st.CyclicEqual(rot) {
			t.Errorf("CyclicEqual(baabaa, 其旋转@%d=%q)=false", j, rot)
		}
	}
}

// TestRejectedOpsLeaveNoTrace 钉住不变量 4：三类拒绝互不相同且不留痕。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := New(""); !errors.Is(err, ErrEmpty) {
		t.Errorf("New(\"\") err=%v, want ErrEmpty", err)
	}
	if _, err := New(strings.Repeat("x", maxLen+1)); !errors.Is(err, ErrTooLong) {
		t.Errorf("New(超长) err=%v, want ErrTooLong", err)
	}
	st := mustNew(t, "baabaa")
	for _, k := range []int{-1, 6, 100} {
		if _, err := st.Rotate(k); !errors.Is(err, ErrBadIndex) {
			t.Errorf("Rotate(%d) err=%v, want ErrBadIndex", k, err)
		}
	}
	// 三者互不相同。
	if errors.Is(ErrEmpty, ErrTooLong) || errors.Is(ErrEmpty, ErrBadIndex) || errors.Is(ErrTooLong, ErrBadIndex) {
		t.Error("三类哨兵错误必须互不相同")
	}
	// 拒绝之后实例行为不变。
	k, err := st.MinRotation()
	if err != nil || k != 1 {
		t.Errorf("被拒后 MinRotation=(%d,%v), want (1,nil)", k, err)
	}
	r, err := st.Rotate(k)
	if err != nil || r != "aabaab" {
		t.Errorf("被拒后 Rotate(1)=(%q,%v), want (aabaab,nil)", r, err)
	}
	if !st.CyclicEqual("aabaab") || st.CyclicEqual("baaab") {
		t.Error("被拒后 CyclicEqual 行为改变")
	}
}

// TestSelfCheck 钉住自检方法本身：对正常实例必须通过。
func TestSelfCheck(t *testing.T) {
	for _, s := range []string{"baabaa", "banana", "aaaa", "z"} {
		if err := mustNew(t, s).SelfCheck(); err != nil {
			t.Errorf("SelfCheck(%q)=%v, want nil", s, err)
		}
	}
}

// TestConcurrentReads 钉住并发：N 个 goroutine 只读同一实例，结果逐项相同。
func TestConcurrentReads(t *testing.T) {
	st := mustNew(t, "baabaa")
	k0, _ := st.MinRotation()
	r0, _ := st.Rotate(k0)
	const G = 64
	var wg sync.WaitGroup
	errs := make(chan string, G)
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iter := 0; iter < 200; iter++ {
				k, err := st.MinRotation()
				if err != nil || k != k0 {
					errs <- "MinRotation 结果分叉"
					return
				}
				r, err := st.Rotate(k)
				if err != nil || r != r0 {
					errs <- "Rotate 结果分叉"
					return
				}
				if !st.CyclicEqual(r0) || st.SelfCheck() != nil {
					errs <- "CyclicEqual/SelfCheck 结果分叉"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
