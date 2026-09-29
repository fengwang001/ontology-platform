package roll

import (
	"errors"
	"testing"
)

func recompute(window int, data []byte) uint32 {
	h := uint32(0)
	for _, b := range data[len(data)-window:] {
		h = (h*base + uint32(b)) & mask
	}
	return h
}

func TestHasher(t *testing.T) {
	cases := []struct {
		name   string
		window int
		n      int
	}{
		{"100k", 16, 100000},
		{"1m", 16, 1000000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := New(tc.window)
			if err != nil {
				t.Fatal(err)
			}
			data := make([]byte, tc.n)
			for i := range data {
				data[i] = byte(i*1103515245 + 12345)
			}
			for _, b := range data {
				h.Push(b)
			}
			adv := h.advances
			if adv > tc.n { // 系数 ≤ 1：每个字节至多推进一次
				t.Fatalf("advances=%d > length=%d", adv, tc.n)
			}
			wantAdv := tc.n - tc.window // 窗口填满后每字节恰好一次推进
			if adv != wantAdv {
				t.Fatalf("advances=%d want %d", adv, wantAdv)
			}
			if h.pushed != tc.n { // 每个输入字节只进入窗口一次：无重复推进
				t.Fatalf("pushed=%d want %d", h.pushed, tc.n)
			}
			if h.h != recompute(tc.window, data) {
				t.Fatal("rolling state disagrees with direct window hash")
			}
			if tc.n == 1000000 && float64(adv)/float64(tc.n) > 1 {
				t.Fatal("coefficient exceeds 1")
			}
		})
	}
}

func TestReset(t *testing.T) {
	cases := []struct{ window int }{
		{1}, {2}, {64},
	}
	for _, tc := range cases {
		h, _ := New(tc.window)
		for i := 0; i < tc.window*3; i++ {
			h.Push(byte(i))
		}
		h.Reset()
		if h.advances != 0 || h.pushed != 0 || h.Full() {
			t.Fatalf("reset did not clear state: %+v", h)
		}
	}
}

func TestNewError(t *testing.T) {
	cases := []struct {
		name   string
		window int
	}{
		{"zero", 0}, {"negative", -3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.window); !errors.Is(err, ErrWindow) {
				t.Fatalf("err=%v want ErrWindow", err)
			}
		})
	}
}
