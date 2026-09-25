package api

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// naive 是朴素参照：map + 排序（对拍测试单线程使用，无需锁）。
type naive map[int]bool

func (n naive) list() []int {
	var out []int
	for k, v := range n {
		if v {
			out = append(out, k)
		}
	}
	sort.Ints(out)
	return out
}

func mustErr(t *testing.T, err, want error) {
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

// TestMatchesNaiveReference 不变量 1：随机操作序列与朴素参照逐次对拍。
func TestMatchesNaiveReference(t *testing.T) {
	for _, trial := range []uint32{1, 7, 42, 2026} {
		l, ref, seed := New(), naive{}, trial
		rnd := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>8) % n }
		for i := 0; i < 3000; i++ {
			k := rnd(100)
			switch rnd(3) {
			case 0:
				if (l.Insert(k) == nil) == ref[k] {
					t.Fatalf("trial=%d i=%d insert(%d) 与参照不一致", trial, i, k)
				}
				ref[k] = true
			case 1:
				if (l.Delete(k) == nil) != ref[k] {
					t.Fatalf("trial=%d i=%d delete(%d) 与参照不一致", trial, i, k)
				}
				ref[k] = false
			default:
				if l.Contains(k) != ref[k] {
					t.Fatalf("trial=%d i=%d contains(%d) 与参照不一致", trial, i, k)
				}
			}
		}
		if fmt.Sprint(l.List()) != fmt.Sprint(ref.list()) {
			t.Fatalf("trial=%d 最终 List 与参照不一致", trial)
		}
	}
}

// TestSortedUnique 不变量 2：List 严格升序、无重复。
func TestSortedUnique(t *testing.T) {
	l, seed := New(), uint32(9)
	for i := 0; i < 500; i++ {
		seed = seed*1664525 + 1013904223
		_ = l.Insert(int(seed>>8) % 300)
	}
	got := l.List()
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("List 非严格升序: %v", got)
		}
	}
}

// TestDeleteVisibility 不变量 3：删除后立即不可见，且不影响其他 key。
func TestDeleteVisibility(t *testing.T) {
	l := New()
	for _, k := range []int{5, 2, 8, 3} {
		_ = l.Insert(k)
	}
	if err := l.Delete(2); err != nil || l.Contains(2) {
		t.Fatalf("Delete(2): err=%v contains=%v", err, l.Contains(2))
	}
	if fmt.Sprint(l.List()) != "[3 5 8]" {
		t.Fatalf("删除影响了其他 key: %v", l.List())
	}
	mustErr(t, l.Delete(2), ErrNotFound) // 已删除的 key 再删报不存在
}

// TestRejectedOpsLeaveNoTrace 不变量 4：被拒操作零变化，三类错误互不相同，关闭为终态。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	l := New()
	_ = l.Insert(1)
	before := fmt.Sprint(l.List())
	mustErr(t, l.Insert(1), ErrDuplicate)
	mustErr(t, l.Delete(99), ErrNotFound)
	if fmt.Sprint(l.List()) != before {
		t.Fatal("被拒操作改变了链表")
	}
	for _, p := range [][2]error{{ErrDuplicate, ErrNotFound}, {ErrDuplicate, ErrClosed}, {ErrNotFound, ErrClosed}} {
		if errors.Is(p[0], p[1]) {
			t.Fatalf("哨兵错误不互异: %v vs %v", p[0], p[1])
		}
	}
	_ = l.Close()
	for i := 0; i < 3; i++ { // 关闭是终态，持续报 ErrClosed
		mustErr(t, l.Insert(2), ErrClosed)
		mustErr(t, l.Delete(1), ErrClosed)
	}
	if !l.Contains(1) || fmt.Sprint(l.List()) != before {
		t.Fatal("关闭后状态被改变")
	}
}

// TestConcurrent 并发：N 插入 + M 删除 + 并发 Contains，收尾后 List 恰为应保留集合。
func TestConcurrent(t *testing.T) {
	for _, n := range []int{16, 64, 256} {
		l := New()
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(2)
			go func(k int) { defer wg.Done(); _ = l.Insert(k) }(i)
			go func(k int) { defer wg.Done(); _ = l.Contains(k) }(i)
		}
		wg.Wait()
		for i := 0; i < n; i += 2 {
			wg.Add(1)
			go func(k int) { defer wg.Done(); _ = l.Delete(k) }(i)
		}
		wg.Wait()
		want := []int{}
		for i := 1; i < n; i += 2 {
			want = append(want, i)
			if !l.Contains(i) { // 保留的 key 必须可见
				t.Fatalf("n=%d 保留的 key %d 不可见", n, i)
			}
		}
		if fmt.Sprint(l.List()) != fmt.Sprint(want) {
			t.Fatalf("n=%d 并发后 List 不等于应保留集合", n)
		}
	}
}

// TestSelfCheck 自检方法本身必须通过。
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
