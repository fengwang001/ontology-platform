package cursor

import (
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		c    Cursor
	}{
		{"正向", Cursor{Forward, 2.5, "r0042"}},
		{"反向", Cursor{Backward, -1.25, "x"}},
		{"空 ID", Cursor{Forward, 0, ""}},
		{"长 ID 截断", Cursor{Forward, 1, string(make([]byte, 300))}},
		{"零游标", Cursor{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decode(Encode(tt.c))
			if err != nil {
				t.Fatalf("Decode 报错: %v", err)
			}
			want := tt.c
			if len(want.ID) > maxIDLen {
				want.ID = want.ID[:maxIDLen]
			}
			if got != want {
				t.Fatalf("roundtrip = %+v, want %+v", got, want)
			}
		})
	}
}

func TestDecodeEmpty(t *testing.T) {
	c, err := Decode(nil)
	if err != nil || !c.IsZero() {
		t.Fatalf("空游标应解码为零游标, got %+v, err=%v", c, err)
	}
}

// TestBitFlip 对合法游标逐字节逐比特翻转，每个变体都必须被拒绝并正确分类。
func TestBitFlip(t *testing.T) {
	src := Encode(Cursor{Forward, 2.5, "r0042"})
	counts := map[error]int{
		ErrChecksum:     0,
		ErrTruncated:    0,
		ErrBadDirection: 0,
	}
	accepted := 0
	other := 0
	for i := range src {
		for bit := 0; bit < 8; bit++ {
			variant := make([]byte, len(src))
			copy(variant, src)
			variant[i] ^= 1 << bit
			_, err := Decode(variant)
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, ErrChecksum):
				counts[ErrChecksum]++
			case errors.Is(err, ErrTruncated):
				counts[ErrTruncated]++
			case errors.Is(err, ErrBadDirection):
				counts[ErrBadDirection]++
			default:
				other++
			}
		}
	}
	total := len(src) * 8
	if accepted != 0 || other != 0 {
		t.Fatalf("accepted=%d other=%d, 都必须为 0", accepted, other)
	}
	if counts[ErrBadDirection] != 8 || counts[ErrTruncated] != 8 {
		t.Fatalf("分类不符: %v (总长 %d 变体)", counts, total)
	}
	if counts[ErrChecksum] != total-16 {
		t.Fatalf("校验失败类应为 %d, got %d", total-16, counts[ErrChecksum])
	}
	t.Logf("变体总数=%d 校验失败=%d 字段不完整=%d 方向非法=%d 误接受=0",
		total, counts[ErrChecksum], counts[ErrTruncated], counts[ErrBadDirection])
}

func TestErrorDistinct(t *testing.T) {
	errs := []error{ErrChecksum, ErrTruncated, ErrBadDirection, ErrDirectionMismatch}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Fatalf("%v 与 %v 不可区分", a, b)
			}
		}
	}
}

// TestDecodeAllocs 断言单次解码分配次数为常数（O(1)）。
func TestDecodeAllocs(t *testing.T) {
	src := Encode(Cursor{Forward, 2.5, "r0042"})
	allocs := testing.AllocsPerRun(100, func() {
		if _, err := Decode(src); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 2 {
		t.Fatalf("单次解码分配 %v 次, 超过常数上界 2", allocs)
	}
}
