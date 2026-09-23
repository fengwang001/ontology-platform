package roll

import (
	"errors"
	"testing"
)

// refHash 直接按窗口重算哈希，作为“无重复推进、结果一致”的判定基准。
func refHash(data []byte) uint64 {
	h := uint64(0)
	for _, b := range data {
		h = h*prime + uint64(b)
	}
	return h
}

func TestWindowError(t *testing.T) {
	for _, w := range []int{0, -1} {
		if _, err := New(w); !errors.Is(err, ErrWindow) {
			t.Fatalf("New(%d) err=%v, want ErrWindow", w, err)
		}
	}
}

// TestPushCount：推进次数 = max(0, n-w+1)；系数 ≤1；
// 每个尾窗口哈希必须等于直接重算值——任一字节被重复推进都会使等式失败。
func TestPushCount(t *testing.T) {
	cases := []struct {
		name string
		n, w int
	}{
		{"100k", 100000, 16},
		{"1m", 1000000, 16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := New(c.w)
			data := make([]byte, c.n)
			for i := range data {
				data[i] = byte(i*2654435761 + 7)
			}
			for _, b := range data {
				h.Push(b)
			}
			got := h.pushesCount()
			want := int64(c.n - c.w)
			if got != want {
				t.Fatalf("pushes=%d want=%d (系数应≤1)", got, want)
			}
			if got > int64(c.n) {
				t.Fatalf("pushes %d 超过字节数 %d", got, c.n)
			}
			if h.Hash() != refHash(data[c.n-c.w:]) {
				t.Fatal("尾窗口哈希与直接重算不一致：存在重复/遗漏推进")
			}
			h.Reset()
			if h.pushesCount() != 0 {
				t.Fatal("Reset 未清零推进计数")
			}
		})
	}
}

func TestIncrementalEqualsDirect(t *testing.T) {
	for _, w := range []int{1, 2, 5, 31} {
		h, _ := New(w)
		data := []byte("the quick brown fox jumps over")
		for i, b := range data {
			h.Push(b)
			if i+1 >= w && h.Hash() != refHash(data[i+1-w:i+1]) {
				t.Fatalf("w=%d i=%d 增量哈希与窗口重算不符", w, i)
			}
		}
	}
}
