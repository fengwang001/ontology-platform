package api

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func round2(n int) int {
	s := 1
	for s < n {
		s *= 2
	}
	return s
}

func bl(off, size int) Block { return Block{Off: off, Size: size} }

// 随机交错 Alloc/Free，每步后调 hook（可钉不变量），返回仍持有的 (偏移,大小)。
func scramble(a *Allocator, seed int64, ops int, hook func(map[int]int)) map[int]int {
	r := rand.New(rand.NewSource(seed))
	held := map[int]int{}
	for i := 0; i < ops; i++ {
		if len(held) > 0 && r.Intn(2) == 0 {
			for off := range held {
				if err := a.Free(off); err != nil {
					panic(err)
				}
				delete(held, off)
				break
			}
		} else if n := 1 + r.Intn(64); true {
			if off, err := a.Alloc(n); err == nil {
				held[off] = round2(n)
			}
		}
		if hook != nil {
			hook(held)
		}
	}
	return held
}

func TestEightSteps(t *testing.T) {
	a := New(5)
	want := [8]string{
		"[{4 4} {8 8} {16 16}]", "[{8 8} {16 16}]", "[{12 4} {16 16}]", "[{16 16}]", "[{4 4} {16 16}]", "[{4 4} {8 4} {16 16}]", "[{4 4} {8 8} {16 16}]", "[{0 32}]",
	}
	for i := 0; i < 4; i++ {
		off, err := a.Alloc(4)
		if err != nil || off != 4*i || fmt.Sprint(a.FreeList()) != want[i] {
			t.Fatalf("第%d步 Alloc: off=%d err=%v free=%v", i+1, off, err, a.FreeList())
		}
	}
	for i, off := range []int{4, 8, 12, 0} {
		if err := a.Free(off); err != nil || fmt.Sprint(a.FreeList()) != want[4+i] {
			t.Fatalf("第%d步 Free(%d): err=%v free=%v", i+5, off, err, a.FreeList())
		}
	}
}

func TestConservation(t *testing.T) { // 不变量 1：已分配+空闲==S，SelfCheck 复核不重叠
	for _, tc := range []struct{ n, ops int }{{5, 500}, {8, 2000}, {12, 3000}} {
		a := New(tc.n)
		bad := ""
		scramble(a, int64(tc.n), tc.ops, func(held map[int]int) {
			sum := 0
			for _, b := range a.FreeList() {
				sum += b.Size
			}
			for _, s := range held {
				sum += s
			}
			if sum != 1<<tc.n || a.SelfCheck() != nil {
				bad = fmt.Sprintf("sum=%d", sum)
			}
		})
		if bad != "" {
			t.Fatalf("n=%d 守恒被破坏 %s", tc.n, bad)
		}
	}
}

func TestFullReleaseReturnsToSingle(t *testing.T) { // 不变量 2
	for _, tc := range []struct{ n, seed, ops int }{{5, 1, 300}, {8, 2, 1500}, {10, 3, 3000}} {
		a := New(tc.n)
		for off := range scramble(a, int64(tc.seed), tc.ops, nil) {
			if err := a.Free(off); err != nil {
				t.Fatal(err)
			}
		}
		if got := fmt.Sprint(a.FreeList()); got != fmt.Sprintf("[{0 %d}]", 1<<tc.n) {
			t.Fatalf("n=%d 全释放后=%s，应回单块", tc.n, got)
		}
	}
}

func TestStructure(t *testing.T) { // 不变量 3：2 的幂、对齐、无应合并伙伴
	for _, seed := range []int64{7, 8, 9} {
		a := New(9)
		scramble(a, seed, 1500, nil)
		seen := map[Block]bool{}
		for _, b := range a.FreeList() {
			if b.Size <= 0 || b.Size&(b.Size-1) != 0 || b.Off%b.Size != 0 {
				t.Fatalf("结构非法: %+v", b)
			}
			if seen[bl(b.Off^b.Size, b.Size)] {
				t.Fatalf("存在应合并的空闲伙伴: %+v", b)
			}
			seen[b] = true
		}
	}
}

func TestFailureAtomic(t *testing.T) { // 不变量 4：失败不留痕
	a := New(5)
	full, err := a.Alloc(32)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"n为0", func() error { _, e := a.Alloc(0); return e }, ErrBadSize},
		{"n为负", func() error { _, e := a.Alloc(-3); return e }, ErrBadSize},
		{"n超池", func() error { _, e := a.Alloc(33); return e }, ErrBadSize},
		{"池已满", func() error { _, e := a.Alloc(1); return e }, ErrFull},
		{"释放合法块", func() error { return a.Free(full) }, nil},
		{"重复释放", func() error { return a.Free(full) }, ErrBadFree},
		{"从未分配", func() error { return a.Free(7) }, ErrBadFree},
	}
	for _, tc := range cases {
		before := fmt.Sprint(a.FreeList())
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: 得 %v，期望 %v", tc.name, err, tc.want)
		}
		if tc.want != nil && before != fmt.Sprint(a.FreeList()) {
			t.Fatalf("%s: 被拒后状态被改动", tc.name)
		}
	}
	if ErrBadSize == ErrFull || ErrFull == ErrBadFree || ErrBadSize == ErrBadFree {
		t.Fatal("三类错误必须互不相同")
	}
	if _, err := a.Alloc(32); err != nil {
		t.Fatalf("被拒后分配器不可用: %v", err)
	}
}
