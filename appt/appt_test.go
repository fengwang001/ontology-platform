package appt

import (
	"errors"
	"strings"
	"testing"
)

func testCfg() Config { return Config{S: 20, E: 30, L: 15, Wmax: 60, K: 2} }

func TestBookTable(t *testing.T) {
	cases := []struct {
		name string
		run  func(b *Bookings) error
		want error
	}{
		{"ok", func(b *Bookings) error { return b.Book([]byte("T1"), Dry, 100, 50) }, nil},
		{"s 非 S 倍数", func(b *Bookings) error { return b.Book([]byte("T1"), Dry, 110, 50) }, ErrInvalid},
		{"s<now", func(b *Bookings) error { return b.Book([]byte("T1"), Dry, 40, 50) }, ErrInvalid},
		{"非法 kind", func(b *Bookings) error { return b.Book([]byte("T1"), Kind(9), 100, 50) }, ErrInvalid},
		{"空编号", func(b *Bookings) error { return b.Book(nil, Dry, 100, 50) }, ErrInvalid},
		{"超长编号", func(b *Bookings) error {
			return b.Book([]byte(strings.Repeat("x", 33)), Dry, 100, 50)
		}, ErrInvalid},
		{"名额取等 K:第1辆", func(b *Bookings) error { return b.Book([]byte("A"), Dry, 100, 0) }, nil},
		{"名额取等 K:第2辆", func(b *Bookings) error { return b.Book([]byte("B"), Dry, 100, 0) }, nil},
		{"名额已满:第3辆", func(b *Bookings) error {
			b.Book([]byte("A"), Dry, 100, 0)
			b.Book([]byte("B"), Dry, 100, 0)
			return b.Book([]byte("C"), Dry, 100, 0)
		}, ErrCapacity},
		{"不同 kind 不占名额", func(b *Bookings) error { return b.Book([]byte("C"), Reefer, 100, 0) }, nil},
		{"不同窗口不占名额", func(b *Bookings) error { return b.Book([]byte("C"), Dry, 120, 0) }, nil},
		{"重复预约冲突", func(b *Bookings) error {
			if err := b.Book([]byte("D"), Dry, 100, 0); err != nil {
				t.Fatal(err)
			}
			return b.Book([]byte("D"), Reefer, 120, 0)
		}, ErrDuplicate},
		{"冲突优先于名额已满", func(b *Bookings) error {
			b.Book([]byte("A"), Dry, 100, 0)
			b.Book([]byte("B"), Dry, 100, 0)
			return b.Book([]byte("A"), Dry, 100, 0)
		}, ErrDuplicate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(New(testCfg()))
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestClockRollback(t *testing.T) {
	b := New(testCfg())
	if err := b.Book([]byte("A"), Dry, 100, 50); err != nil {
		t.Fatal(err)
	}
	// 非法参数先于时钟回退。
	if err := b.Book(nil, Dry, 100, 10); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid first, got %v", err)
	}
	// 时钟回退先于冲突/名额。
	if err := b.Book([]byte("A"), Dry, 120, 10); !errors.Is(err, ErrClock) {
		t.Fatalf("clock before duplicate, got %v", err)
	}
	// 被拒绝的操作不推进时钟。
	if err := b.Book([]byte("E"), Dry, 120, 50); err != nil {
		t.Fatalf("state unchanged, book should work: %v", err)
	}
}

func TestConsumeAndVoidDoNotRefund(t *testing.T) {
	b := New(testCfg())
	for _, id := range []string{"A", "B"} {
		if err := b.Book([]byte(id), Dry, 100, 0); err != nil {
			t.Fatal(err)
		}
	}
	b.Consume([]byte("A"))
	if _, ok := b.Lookup([]byte("A")); ok {
		t.Fatal("A should be consumed")
	}
	if err := b.Book([]byte("C"), Dry, 100, 0); !errors.Is(err, ErrCapacity) {
		t.Fatalf("consumed reservation must not refund quota, got %v", err)
	}
	b.Void([]byte("B"))
	if err := b.Book([]byte("D"), Dry, 100, 0); !errors.Is(err, ErrCapacity) {
		t.Fatalf("voided reservation must not refund quota, got %v", err)
	}
}
