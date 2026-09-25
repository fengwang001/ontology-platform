package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func naivePeriod(rs []rune) int {
	for p := 1; p < len(rs); p++ {
		ok := true
		for i := 0; i < len(rs)-p; i++ {
			if rs[i] != rs[i+p] {
				ok = false
				break
			}
		}
		if ok {
			return p
		}
	}
	return len(rs)
}

// TestPeriodNaive 钉住不变量 3：Period 等于朴素枚举 p=1..n-1 的结果。
func TestPeriodNaive(t *testing.T) {
	cases := map[string]int{
		"ababa": 2, "aaaa": 1, "abab": 2, "abcabcabc": 3,
		"abcdef": 6, "αβαβα": 2, "世界世界": 2,
	}
	for s, want := range cases {
		var a api.API
		if err := a.New(s); err != nil {
			t.Fatalf("New(%q): %v", s, err)
		}
		if got, _ := a.Period(); got != want || got != naivePeriod([]rune(s)) {
			t.Fatalf("%q: period=%d want %d", s, got, want)
		}
	}
	// 整除 n 不参与判定：ababa 周期 2（2 不整除 5）必须被接受。
	var a api.API
	if err := a.New("ababa"); err != nil {
		t.Fatal(err)
	}
	if p, _ := a.Period(); p != 2 {
		t.Fatalf("ababa period=%d want 2 (period need not divide n)", p)
	}
}

// TestErrorsAndNoTrace 钉住不变量 4：三类错误可判定、互不相同、失败不留痕。
func TestErrorsAndNoTrace(t *testing.T) {
	var z api.API
	if err := z.New(""); !errors.Is(err, api.ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
	badCases := []struct {
		in  string
		off int
	}{
		{string([]byte{0xff}), 0},
		{"ab" + string([]byte{0xc0}), 2},
		{"a" + string([]byte{0xc3}), 1}, // 截断的多字节序列
	}
	for _, c := range badCases {
		var b api.API
		err := b.New(c.in)
		var bad *api.InvalidUTF8Error
		if !errors.As(err, &bad) || !errors.Is(err, api.ErrInvalidUTF8) || bad.Offset != c.off {
			t.Fatalf("invalid % x: err=%v off=%d", c.in, err, c.off)
		}
	}
	var fresh api.API
	if _, err := fresh.Z(); !errors.Is(err, api.ErrNotBuilt) {
		t.Fatalf("Z before New: %v", err)
	}
	if _, err := fresh.Period(); !errors.Is(err, api.ErrNotBuilt) {
		t.Fatalf("Period before New: %v", err)
	}
	if errors.Is(api.ErrEmpty, api.ErrInvalidUTF8) || errors.Is(api.ErrEmpty, api.ErrNotBuilt) {
		t.Fatal("sentinel errors must be distinct")
	}
	// 未构建对象被拒后仍可正常构建。
	if err := fresh.New("ababa"); err != nil {
		t.Fatalf("rebuild after rejection: %v", err)
	}
	if g, _ := fresh.Z(); !reflect.DeepEqual(g, []int{0, 0, 3, 0, 1}) {
		t.Fatalf("after rebuild Z=%v", g)
	}
	// 已构建对象被拒时，旧状态保持不变。
	if err := fresh.New(""); !errors.Is(err, api.ErrEmpty) {
		t.Fatalf("re-New empty: %v", err)
	}
	if p, _ := fresh.Period(); p != 2 {
		t.Fatalf("state changed after rejected New: period=%d", p)
	}
	if err := fresh.New("aaaa"); err != nil {
		t.Fatal(err)
	}
	if p, _ := fresh.Period(); p != 1 {
		t.Fatalf("valid re-New did not take effect: period=%d", p)
	}
}

// TestConcurrent 并发只读 Z/Period，开始栅栏同步（不用 sleep），结果逐字段相同。
func TestConcurrent(t *testing.T) {
	var a api.API
	if err := a.New("abababababab"); err != nil {
		t.Fatal(err)
	}
	const N = 128
	start := make(chan struct{})
	var wg sync.WaitGroup
	zs := make([][]int, N)
	ps := make([]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var err error
			if zs[i], err = a.Z(); err != nil {
				t.Errorf("Z: %v", err)
				return
			}
			if ps[i], err = a.Period(); err != nil {
				t.Errorf("Period: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < N; i++ {
		if ps[i] != ps[0] || !reflect.DeepEqual(zs[i], zs[0]) {
			t.Fatalf("goroutine %d differs: p=%d/%d z=%v/%v", i, ps[i], ps[0], zs[i], zs[0])
		}
	}
}

func TestSelfCheck(t *testing.T) {
	var a api.API
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
