package idx

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/norm"
)

// 等价类：é 两种写法、ẹ+acute 三种写法、가 两种写法、Å/Å 两种写法。
var equivInputs = []string{"\u00e9", "e\u0301", "\u1eb9\u0301", "e\u0323\u0301", "e\u0301\u0323", "\uac00", "\u1100\u1161", "\u00c5", "\u212b"}

func TestEquivalenceKeys(t *testing.T) {
	x, _ := New(10)
	for _, s := range equivInputs {
		if err := x.Put(s); err != nil {
			t.Fatal(err)
		}
	}
	if x.Distinct() != 4 {
		t.Fatalf("Distinct = %d, want 4", x.Distinct())
	}
	for _, tc := range []struct {
		q    string
		want []string
	}{
		{"e\u0301", equivInputs[0:2]},
		{"e\u0323\u0301", equivInputs[2:5]},
		{"\u1100\u1161", equivInputs[5:7]},
		{"\u212b", equivInputs[7:9]},
	} {
		got, err := x.Get(tc.q)
		if err != nil || fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Fatalf("Get(%q) = %v, %v; want %v", tc.q, got, err, tc.want)
		}
	}
	if g, _ := x.Get("a"); len(g) != 0 { // 未命中返回空
		t.Fatalf("Get(a) = %v, want empty", g)
	}
	_ = x.Put("\u00e9") // 重复 Put 去重，失败会被下一检查捕获
	if g, _ := x.Get("e\u0301"); len(g) != 2 {
		t.Fatalf("after dup Put, Get = %v", g)
	}
}
func TestErrorsDistinct(t *testing.T) {
	_, e0 := New(0)
	x, _ := New(1)
	_ = x.Put("a")
	errs := []error{e0, x.Put("\xff"), x.Put("0"), x.Put("b")}
	wants := []error{ErrBadMaxKeys, norm.ErrInvalidUTF8, norm.ErrUnsupported, ErrTooManyKeys}
	seen := map[error]bool{}
	for i, e := range errs {
		if !errors.Is(e, wants[i]) {
			t.Fatalf("err %d = %v, want %v", i, e, wants[i])
		}
		seen[e] = true
	}
	if len(seen) != 4 {
		t.Fatal("four sentinel errors must be mutually distinct")
	}
}
func TestFailureAtomic(t *testing.T) {
	x, _ := New(1)
	if err := x.Put("\u00e9"); err != nil {
		t.Fatal(err)
	}
	before, _ := x.Get("e\u0301")
	for _, s := range []string{"\xff", "0", "\u00c5"} { // 非法 UTF-8 / 不支持码点 / 键数超限
		if err := x.Put(s); err == nil {
			t.Fatalf("Put(%q) should be rejected", s)
		}
	}
	after, _ := x.Get("e\u0301")
	if x.Distinct() != 1 || fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatal("state changed after rejected puts")
	}
	if err := x.Put("e\u0301"); err != nil { // 拒绝后仍可正常使用
		t.Fatal(err)
	}
	if g, _ := x.Get("\u00e9"); len(g) != 2 {
		t.Fatalf("Get after recovery = %v", g)
	}
}

// TestGetCheckedConstant 证明 Get 按 NFC 键哈希定位：检查个数不随 m 增长。
func TestGetCheckedConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		x, _ := New(m)
		for i := 0; i < m; i++ {
			_ = x.Put(string(rune(0xAC00 + i)))
		}
		_, _ = x.Get(string(rune(0xAC00 + m/2)))
		if x.checked > 1 || x.Distinct() != m { // 只命中目标桶自身，与 m 无关
			t.Fatalf("m=%d: checked=%d distinct=%d", m, x.checked, x.Distinct())
		}
	}
}
func TestConcurrent(t *testing.T) {
	const n = 256
	x, _ := New(n)
	done := make(chan struct{})
	var samples []int
	var swg, wg sync.WaitGroup
	swg.Add(1)
	go func() { // 采样 Distinct，不许 sleep
		defer swg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			samples = append(samples, x.Distinct())
		}
	}()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = x.Put(string(rune(0xAC00 + i))) }()
	}
	wg.Wait()
	close(done)
	swg.Wait()
	got := make([][]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := x.Get(string(rune(0xAC00 + i)))
			if err == nil {
				got[i] = g
			}
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if want := string(rune(0xAC00 + i)); len(got[i]) != 1 || got[i][0] != want {
			t.Fatalf("Get %q = %v", want, got[i])
		}
	}
	for i := 1; i < len(samples); i++ {
		if samples[i] < samples[i-1] {
			t.Fatal("Distinct decreased")
		}
	}
	if x.Distinct() != n {
		t.Fatalf("Distinct = %d, want %d", x.Distinct(), n)
	}
}
