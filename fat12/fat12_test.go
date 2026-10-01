package fat12

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func errCat(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	case errors.Is(err, ErrNoSpace):
		return "nospace"
	case errors.Is(err, ErrOutOfRange):
		return "outofrange"
	case errors.Is(err, ErrNotFree):
		return "notfree"
	default:
		return "unknown"
	}
}

// imageLen 为 ⌈(C+2)×3/2⌉。
func imageLen(c int) int { return ((c+2)*3 + 1) / 2 }

func TestNewRejectsOutOfRangeC(t *testing.T) {
	for _, c := range []int{0, -1, 4079, 1 << 30} {
		if f, err := New(c); !errors.Is(err, ErrInvalid) || f != nil {
			t.Fatalf("New(%d) = %v,%v, want nil,ErrInvalid", c, f, err)
		}
	}
}

func TestNewLayoutAndPacking(t *testing.T) {
	for _, c := range []int{1, 2, 3, 9, 10, 11, 4078} {
		f, err := New(c)
		if err != nil {
			t.Fatalf("New(%d): %v", c, err)
		}
		img := f.Image()
		if len(img) != imageLen(c) {
			t.Fatalf("C=%d image len=%d, want %d", c, len(img), imageLen(c))
		}
		// 与逐项 uint16 朴素编码逐字节相同。
		m := &model{c: c, max: c + 1, fat: make([]uint16, c+2)}
		m.fat[0], m.fat[1] = 0xFF8, 0xFFF
		if !bytes.Equal(img, m.image()) {
			t.Fatalf("C=%d initial image %x != model %x", c, img, m.image())
		}
		if f.Rover() != 2 || f.Free() != c {
			t.Fatalf("C=%d rover=%d free=%d", c, f.Rover(), f.Free())
		}
	}

	// 奇偶相邻项互不干扰。
	f, _ := New(3)
	f.mu.Lock()
	f.setEntry(2, 0xABC)
	if got := f.getEntry(2); got != 0xABC {
		t.Fatalf("entry2=%03X", got)
	}
	if got := f.getEntry(3); got != 0 {
		t.Fatalf("entry3 affected by even write: %03X", got)
	}
	if f.fat[4]&0xF0 != 0 { // 字节 4 的高半字节属于项 3，应为 0
		t.Fatalf("odd nibble clobbered by even write: %02X", f.fat[4])
	}
	f.setEntry(3, 0x123)
	if got := f.getEntry(3); got != 0x123 {
		t.Fatalf("entry3=%03X", got)
	}
	if got := f.getEntry(2); got != 0xABC {
		t.Fatalf("entry2 affected by odd write: %03X", got)
	}
	f.setEntry(2, 0x000)
	if got := f.getEntry(3); got != 0x123 {
		t.Fatalf("entry3 affected by even clear: %03X", got)
	}
	f.mu.Unlock()

	// 链尾 0xFFF 与坏簇 0xFF7 的区分。
	f, _ = New(4)
	h, err := f.Create(1)
	if err != nil || h != 2 {
		t.Fatalf("Create = %d,%v", h, err)
	}
	if err := f.MarkBad(3); err != nil {
		t.Fatalf("MarkBad: %v", err)
	}
	f.mu.Lock()
	if v := f.getEntry(h); v != 0xFFF {
		t.Fatalf("eoc=%03X want FFF", v)
	}
	if v := f.getEntry(3); v != 0xFF7 {
		t.Fatalf("bad=%03X want FF7", v)
	}
	f.mu.Unlock()
	if ch, err := f.Chain(h); err != nil || fmt.Sprint(ch) != "[2]" {
		t.Fatalf("chain = %v,%v", ch, err)
	}

	// Image 返回副本。
	img := f.Image()
	img[0] ^= 0xFF
	if f.Image()[0] == img[0] {
		t.Fatal("Image() must return a copy")
	}
}

func TestPromptExample(t *testing.T) {
	f, _ := New(10)
	h1, err := f.Create(3)
	if err != nil || h1 != 2 || f.Rover() != 5 {
		t.Fatalf("Create3 = %d,%v rover=%d", h1, err, f.Rover())
	}
	h2, err := f.Create(2)
	if err != nil || h2 != 5 || f.Rover() != 7 {
		t.Fatalf("Create2 = %d,%v rover=%d", h2, err, f.Rover())
	}
	if err := f.Delete(2); err != nil || f.Rover() != 2 {
		t.Fatalf("Delete rover=%d err=%v", f.Rover(), err)
	}
	h3, err := f.Create(4)
	if err != nil || h3 != 7 || f.Rover() != 11 {
		t.Fatalf("Create4 = %d,%v rover=%d (want 7,11)", h3, err, f.Rover())
	}
	if f.Free() != 4 {
		t.Fatalf("free=%d want 4", f.Free())
	}
	h4, err := f.Defrag(7)
	if err != nil || h4 != 2 || f.Rover() != 8 {
		t.Fatalf("Defrag = %d,%v rover=%d (want 2,8)", h4, err, f.Rover())
	}
	ch, _ := f.Chain(2)
	if fmt.Sprint(ch) != "[2 3 4 7]" {
		t.Fatalf("chain=%v", ch)
	}
	if f.Free() != 4 {
		t.Fatalf("free=%d want 4 after defrag", f.Free())
	}
	if _, err := f.Chain(7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old handle still valid: %v", err)
	}
}
