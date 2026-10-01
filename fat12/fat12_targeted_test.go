package fat12

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func setRover(f *FAT12, r int) {
	f.mu.Lock()
	f.rover = r
	f.mu.Unlock()
}

func TestContiguousAtRoverAndCyclicFallback(t *testing.T) {
	// C=8，簇 4 标坏；rover=2 时 2、3 成段（起点恰等于 rover）。
	f, _ := New(8)
	if err := f.MarkBad(4); err != nil {
		t.Fatal(err)
	}
	h, err := f.Create(2)
	if err != nil || h != 2 || f.Rover() != 4 {
		t.Fatalf("segment at rover = %d,%v rover=%d", h, err, f.Rover())
	}
	// 不环绕范围内坏簇 4 打断后，5、6 命中连续段。
	h, err = f.Create(2)
	if err != nil || h != 5 || f.Rover() != 7 {
		t.Fatalf("want 5,6 got %d,%v rover=%d", h, err, f.Rover())
	}
	// 空闲 7、8、9（C=8 的簇到 9）：取 3 个成段，rover 跨过 C+1 回到 2。
	h, err = f.Create(3)
	if err != nil || h != 7 || f.Rover() != 2 {
		t.Fatalf("run 7,8,9 = %d,%v rover=%d", h, err, f.Rover())
	}

	// 循环回退并环绕过 C+1 回到 2：坏簇 3、4、5、6、8，空闲 2、7，rover=7。
	g, _ := New(7)
	for _, c := range []int{3, 4, 5, 6, 8} {
		if err := g.MarkBad(c); err != nil {
			t.Fatal(err)
		}
	}
	setRover(g, 7)
	h2, err := g.Create(2)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := g.Chain(h2)
	if fmt.Sprint(ch) != "[7 2]" {
		t.Fatalf("wraparound chain=%v want [7 2]", ch)
	}
	if g.Rover() != 3 {
		t.Fatalf("rover=%d want 3", g.Rover())
	}

	// 连续段不跨过 C+1 环绕：rover=9(C=8 最大簇)，仅 9 空闲；取后 rover 回 2。
	k, _ := New(8)
	for c := 2; c <= 8; c++ {
		if err := k.MarkBad(c); err != nil {
			t.Fatal(err)
		}
	}
	setRover(k, 9)
	h3, err := k.Create(1)
	if err != nil || h3 != 9 || k.Rover() != 2 {
		t.Fatalf("single at max = %d,%v rover=%d", h3, err, k.Rover())
	}
	if _, err := k.Create(1); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err=%v", err)
	}
}

func TestNoSpaceLeavesStateUntouched(t *testing.T) {
	f, _ := New(5)
	h, _ := f.Create(3) // 2、3、4，rover=5
	before := f.Image()
	if _, err := f.Create(3); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err=%v", err)
	}
	if !bytes.Equal(f.Image(), before) || f.Rover() != 5 {
		t.Fatal("Create no-space changed state")
	}
	if err := f.Extend(h, 3); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("extend err=%v", err)
	}
	if !bytes.Equal(f.Image(), before) || f.Rover() != 5 {
		t.Fatal("Extend no-space changed state")
	}
	ch, _ := f.Chain(h)
	if fmt.Sprint(ch) != "[2 3 4]" {
		t.Fatalf("chain=%v", ch)
	}
}

func TestTruncateAndDeleteRover(t *testing.T) {
	f, _ := New(10)
	h, _ := f.Create(6)                      // 2..7，rover=8
	if err := f.Truncate(h, 3); err != nil { // 释放 5、6、7
		t.Fatal(err)
	}
	if f.Rover() != 5 {
		t.Fatalf("truncate rover=%d want 5", f.Rover())
	}
	ch, _ := f.Chain(h)
	if fmt.Sprint(ch) != "[2 3 4]" {
		t.Fatalf("chain=%v", ch)
	}
	// 无簇释放：k 等于链长，rover 不变。
	setRover(f, 9)
	if err := f.Truncate(h, 3); err != nil || f.Rover() != 9 {
		t.Fatalf("truncate to same length rover=%d", f.Rover())
	}
	if err := f.Truncate(h, 4); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("err=%v", err)
	}
	if f.Rover() != 9 {
		t.Fatal("failed truncate moved rover")
	}
	if err := f.Delete(h); err != nil || f.Rover() != 2 {
		t.Fatalf("delete rover=%d err=%v", f.Rover(), err)
	}
	if _, err := f.Chain(h); !errors.Is(err, ErrNotFound) {
		t.Fatalf("chain after delete=%v", err)
	}
	if err := f.Delete(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing err=%v", err)
	}
}

func TestExtendNonAdjacentLink(t *testing.T) {
	// C=5（簇 2..6）：文件 5->6，簇 4 坏，空闲 2、3；rover=2。
	f, _ := New(5)
	f.mu.Lock()
	f.setEntry(4, 0xFF7)
	f.setEntry(5, 6)
	f.setEntry(6, 0xFFF)
	f.files[5] = struct{}{}
	f.rover = 2
	f.mu.Unlock()
	if err := f.Extend(5, 2); err != nil {
		t.Fatal(err)
	}
	ch, _ := f.Chain(5)
	if fmt.Sprint(ch) != "[5 6 2 3]" {
		t.Fatalf("extended chain=%v want [5 6 2 3]", ch)
	}
	if f.Rover() != 4 {
		t.Fatalf("rover=%d want 4", f.Rover())
	}
	if err := f.Extend(5, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("n=0 err=%v", err)
	}
	if err := f.Extend(42, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing err=%v", err)
	}
	if err := f.Extend(5, 1); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("full err=%v", err)
	}
}

func TestDefragAlreadyAligned(t *testing.T) {
	f, _ := New(10)
	h, _ := f.Create(3) // 2、3、4
	got, err := f.Defrag(h)
	if err != nil || got != h || f.Rover() != 5 {
		t.Fatalf("defrag aligned = %d,%v rover=%d", got, err, f.Rover())
	}
	ch, _ := f.Chain(h)
	if fmt.Sprint(ch) != "[2 3 4]" {
		t.Fatalf("chain=%v", ch)
	}
}

func TestDefragSameSetReorderOnly(t *testing.T) {
	// C=5（簇 2..6）：文件链 4->2->3（集合 {2,3,4}），空闲 5、6。
	f, _ := New(5)
	f.mu.Lock()
	f.setEntry(4, 2)
	f.setEntry(2, 3)
	f.setEntry(3, 0xFFF)
	f.files[4] = struct{}{}
	f.rover = 5
	f.mu.Unlock()
	got, err := f.Defrag(4)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := f.Chain(got)
	if fmt.Sprint(ch) != "[2 3 4]" {
		t.Fatalf("reordered chain=%v want [2 3 4]", ch)
	}
	if got != 2 {
		t.Fatalf("new handle=%d want 2", got)
	}
	if f.Rover() != 5 {
		t.Fatalf("rover changed=%d want 5 (no release)", f.Rover())
	}
	if f.Free() != 2 {
		t.Fatalf("free=%d want 2", f.Free())
	}
	if _, err := f.Chain(4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old handle valid: %v", err)
	}
}

func TestDefragSkipsBadAndMovesHandle(t *testing.T) {
	// C=8（簇 2..9）：文件 6->7->8；坏簇 3；空闲 2、4、5、9。
	// S 中最小 3 个（跳过坏 3）= 2、4、5。
	g, _ := New(8)
	g.mu.Lock()
	g.setEntry(3, 0xFF7)
	g.setEntry(6, 7)
	g.setEntry(7, 8)
	g.setEntry(8, 0xFFF)
	g.files[6] = struct{}{}
	g.rover = 8
	g.mu.Unlock()
	got, err := g.Defrag(6)
	if err != nil || got != 2 {
		t.Fatalf("defrag = %d,%v want handle 2", got, err)
	}
	ch, _ := g.Chain(2)
	if fmt.Sprint(ch) != "[2 4 5]" {
		t.Fatalf("chain=%v want [2 4 5] (bad 3 skipped)", ch)
	}
	g.mu.Lock()
	bad := g.getEntry(3)
	g.mu.Unlock()
	if bad != 0xFF7 {
		t.Fatalf("bad cluster touched: %03X", bad)
	}
	if g.Free() != 4 {
		t.Fatalf("free=%d want 4", g.Free())
	}
	if g.Rover() != 6 {
		t.Fatalf("rover=%d want 6", g.Rover())
	}
	if _, err := g.Chain(6); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old handle valid: %v", err)
	}
}

func TestErrorOrdering(t *testing.T) {
	f, _ := New(5)
	if _, err := f.Create(0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("create n=0 err=%v", err)
	}
	if err := f.Extend(99, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("extend order err=%v", err)
	}
	if err := f.Truncate(99, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("truncate order err=%v", err)
	}
	if err := f.Delete(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete err=%v", err)
	}
	if _, err := f.Defrag(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("defrag err=%v", err)
	}
}

func TestBoundaryC1AndC4078(t *testing.T) {
	for _, c := range []int{1, 4078} {
		f, err := New(c)
		if err != nil {
			t.Fatalf("New(%d): %v", c, err)
		}
		h, err := f.Create(c)
		if err != nil || h != 2 {
			t.Fatalf("C=%d Create(%d)=%d,%v", c, c, h, err)
		}
		ch, err := f.Chain(h)
		if err != nil || len(ch) != c {
			t.Fatalf("C=%d chain len=%d err=%v", c, len(ch), err)
		}
		if f.Free() != 0 {
			t.Fatalf("C=%d free=%d", c, f.Free())
		}
		if _, err := f.Create(1); !errors.Is(err, ErrNoSpace) {
			t.Fatalf("C=%d full err=%v", c, err)
		}
		// 与逐项 uint16 朴素模型编码一致（含奇数项时末字节半字节）。
		m := &model{c: c, max: c + 1, fat: make([]uint16, c+2)}
		m.fat[0], m.fat[1] = 0xFF8, 0xFFF
		for i := 2; i <= c+1; i++ {
			if i == c+1 {
				m.fat[i] = 0xFFF
			} else {
				m.fat[i] = uint16(i + 1)
			}
		}
		if !bytes.Equal(f.Image(), m.image()) {
			t.Fatalf("C=%d image mismatch vs model", c)
		}
		if got, err := f.Defrag(h); err != nil || got != h {
			t.Fatalf("C=%d defrag aligned = %d,%v", c, got, err)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	f, _ := New(64)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				h, err := f.Create(1)
				if err == nil {
					_ = f.Extend(h, 1)
					_, _ = f.Chain(h)
					_ = f.Free()
					_ = f.Rover()
					_ = f.Image()
					_ = f.Truncate(h, 1)
					_ = f.Delete(h)
				}
			}
		}()
	}
	wg.Wait()
	if f.Free() != 64 {
		t.Fatalf("free after concurrent churn=%d want 64", f.Free())
	}
}
