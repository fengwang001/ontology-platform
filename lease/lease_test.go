package lease

import (
	"errors"
	"testing"
	"time"
)

func TestLease(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		length  int64
		wantErr error
	}{
		{"段长为负", -3, ErrInvalidLength},
		{"段长为零", 0, ErrInvalidLength},
		{"段长为一", 1, nil},
		{"段长正常", 100, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, err := New(100, tc.length, t0)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if l.End() != 100+uint64(tc.length) {
				t.Fatalf("End()=%d", l.End())
			}
		})
	}
	l, _ := New(10, 5, t0.Add(time.Minute))
	if l.Expired(t0) || !l.Expired(t0.Add(time.Minute)) {
		t.Fatal("到期判定错误：到期点即作废")
	}
	if got := l.Remaining(12); got != 3 {
		t.Fatalf("Remaining(12)=%d, want 3", got)
	}
	if got := l.Remaining(15); got != 0 {
		t.Fatalf("Remaining(15)=%d, want 0", got)
	}
}
