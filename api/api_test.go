package api

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

func fmtRanges(rs []Range) string {
	ss := make([]string, len(rs))
	for i, r := range rs {
		ss[i] = fmt.Sprintf("[%d,%d):%d", r.Lo, r.Hi, r.Load)
	}
	return strings.Join(ss, " ")
}
func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Errorf("got %v, want %v", err, want)
	}
}

// 故障注入：表驱动覆盖 New 全部非法参数分支。
func TestInvalidParams(t *testing.T) {
	cases := []struct {
		name   string
		lo, hi int64
		sp, mg int
	}{
		{"low==high", 5, 5, 3, 2}, {"low>high", 8, 3, 3, 2}, {"low<0", -1, 16, 3, 2},
		{"split<2", 0, 16, 1, 1}, {"merge<1", 0, 16, 3, 0},
		{"split==merge", 0, 16, 3, 3}, {"split<merge", 0, 16, 2, 5},
	}
	for _, c := range cases {
		_, err := New(c.lo, c.hi, c.sp, c.mg)
		if !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("%s: want ErrInvalidParams, got %v", c.name, err)
		}
	}
}

// 故障注入：三类哨兵互不相同；失败不留痕；被拒后仍可正常使用。
func TestRejectedNoTrace(t *testing.T) {
	tb, _ := New(0, 16, 3, 2)
	for _, k := range []int64{-1, -100, 16, 1 << 40} { // 越界键
		mustErr(t, tb.Insert(k), ErrOutOfRange)
		_, err := tb.Locate(k)
		mustErr(t, err, ErrOutOfRange)
		mustErr(t, tb.Delete(k), ErrNotFound)
	}
	for _, k := range []int64{0, 7, 15} { // 域内但从未插入
		mustErr(t, tb.Delete(k), ErrNotFound)
	}
	if errors.Is(ErrInvalidParams, ErrOutOfRange) || errors.Is(ErrOutOfRange, ErrNotFound) ||
		errors.Is(ErrInvalidParams, ErrNotFound) {
		t.Fatal("三类哨兵错误必须互不相同")
	}
	tb.Insert(5)
	tb.Insert(11)
	tb.Insert(8)
	before := fmtRanges(tb.Ranges())
	if err := tb.Insert(16); !errors.Is(err, ErrOutOfRange) {
		t.Fatal("Insert(16) 应被拒")
	}
	if err := tb.Delete(0); !errors.Is(err, ErrNotFound) {
		t.Fatal("Delete(0) 应被拒")
	}
	if after := fmtRanges(tb.Ranges()); after != before {
		t.Fatalf("失败不留痕被破坏: before=%s after=%s", before, after)
	}
	if err := tb.Insert(14); err != nil || tb.Delete(5) != nil {
		t.Fatal("被拒后应仍可正常使用")
	}
}

// 第三节十一步场景端到端（含第 3、8、11 步判定）。
func TestElevenStepScenario(t *testing.T) {
	tb, _ := New(0, 16, 3, 2)
	want := []string{
		"[0,16):1", "[0,16):2", "[0,8):1 [8,16):2",
		"[0,8):1 [8,12):2 [12,16):1", "[0,8):2 [8,12):2 [12,16):1",
		"[0,4):1 [4,8):2 [8,12):2 [12,16):1",
		"[0,4):1 [4,8):2 [8,12):2 [12,16):2",
		"[0,4):1 [4,8):2 [8,10):1 [10,12):2 [12,16):2",
		"[0,4):1 [4,8):1 [8,10):1 [10,12):2 [12,16):2",
		"[0,4):0 [4,8):1 [8,10):1 [10,12):2 [12,16):2",
		"[0,10):2 [10,12):2 [12,16):2",
	}
	step := 0
	for _, k := range []int64{5, 11, 8, 14, 2, 6, 12, 10} {
		tb.Insert(k)
		if got := fmtRanges(tb.Ranges()); got != want[step] {
			t.Fatalf("步%d: got %s want %s", step+1, got, want[step])
		}
		step++
	}
	for _, k := range []int64{5, 2} {
		tb.Delete(k)
		if got := fmtRanges(tb.Ranges()); got != want[step] {
			t.Fatalf("步%d: got %s want %s", step+1, got, want[step])
		}
		step++
	}
	tb.Compact()
	if got := fmtRanges(tb.Ranges()); got != want[10] {
		t.Fatalf("步11: got %s want %s", got, want[10])
	}
}

// 并发：N 个 goroutine 并发 Locate 同一批键，逐键结果必须相同（WaitGroup 同步，不用 sleep）。
func TestConcurrentLocate(t *testing.T) {
	tb, _ := New(0, 1<<22, 4, 2)
	var keys []int64
	for k := int64(0); k < 3000; k++ {
		if key := k*1000 + 7; tb.Insert(key) == nil {
			keys = append(keys, key)
		}
	}
	ref := make([]Range, len(keys))
	for i, k := range keys {
		ref[i], _ = tb.Locate(k)
	}
	const N = 16
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, k := range keys {
				r, err := tb.Locate(k)
				if err != nil || !slices.Equal([]Range{r}, []Range{ref[i]}) {
					t.Errorf("并发 Locate(%d) 结果不一致", k)
					return
				}
			}
		}()
	}
	wg.Wait()
}
func TestSelfCheck(t *testing.T) {
	tb, _ := New(0, 16, 3, 2)
	if err := tb.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
