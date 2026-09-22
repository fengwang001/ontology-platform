package budget

import (
	"errors"
	"testing"
)

type op struct {
	acquire int64 // >0 表示 TryAcquire
	release int64 // >0 表示 Release
	wantErr bool
}

func TestBudgetAccounting(t *testing.T) {
	cases := []struct {
		name     string
		limit    int64
		ops      []op
		wantUsed int64
		wantMax  int64
	}{
		{"never over limit", 100, []op{{acquire: 60}, {acquire: 40}, {acquire: 1, wantErr: true}}, 100, 100},
		{"release reopens room", 100, []op{{acquire: 90}, {release: 50}, {acquire: 60}}, 100, 100},
		{"exact limit ok", 10, []op{{acquire: 10}}, 10, 10},
		{"zero limit rejects", 0, []op{{acquire: 1, wantErr: true}}, 0, 0},
		{"max tracks peak", 1000, []op{{acquire: 300}, {release: 300}, {acquire: 100}}, 100, 300},
		{"over-release clamps", 100, []op{{acquire: 10}, {release: 99}}, 0, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := New(tc.limit)
			for i, o := range tc.ops {
				if o.acquire > 0 {
					err := b.TryAcquire(o.acquire)
					if o.wantErr && !errors.Is(err, ErrOverBudget) {
						t.Fatalf("op %d: want ErrOverBudget, got %v", i, err)
					}
					if !o.wantErr && err != nil {
						t.Fatalf("op %d: unexpected %v", i, err)
					}
				}
				if o.release > 0 {
					b.Release(o.release)
				}
				if b.Used() > b.Limit() {
					t.Fatalf("op %d: used %d exceeds limit %d", i, b.Used(), b.Limit())
				}
			}
			if b.Used() != tc.wantUsed || b.Max() != tc.wantMax {
				t.Fatalf("used=%d max=%d, want used=%d max=%d",
					b.Used(), b.Max(), tc.wantUsed, tc.wantMax)
			}
		})
	}
}
