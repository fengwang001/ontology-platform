package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func TestAdmissionWindowAndBumpRules(t *testing.T) {
	t.Run("exact limit and one extra rejected", func(t *testing.T) {
		b, _ := NewCapacityBook(10, 2, 3000, 50)
		if err := b.Book(1, 1, 1, 10, 0); err != nil {
			t.Fatal(err)
		}
		if err := b.Book(2, 1, 1, 1, 0); !errors.Is(err, ErrCapacityLimit) {
			t.Fatalf("error = %v, want capacity", err)
		}
	})

	t.Run("oversell floor and cap", func(t *testing.T) {
		b, _ := NewCapacityBook(10, 2, 3000, 0)
		bookMany(t, b, [][5]int64{{1, 1, 0, 7, 0}})
		if _, err := b.Settle(0, []Arrival{{1, 4}}); err != nil {
			t.Fatal(err)
		}
		if got := b.CurrentOversell(); got != 3000 {
			t.Fatalf("capped O = %d, want 3000; raw floor is 4285", got)
		}

		b2, _ := NewCapacityBook(10, 2, 10000, 0)
		bookMany(t, b2, [][5]int64{{1, 1, 0, 7, 0}})
		if _, err := b2.Settle(0, []Arrival{{1, 4}}); err != nil {
			t.Fatal(err)
		}
		if got := b2.CurrentOversell(); got != 4285 {
			t.Fatalf("floored O = %d, want 4285", got)
		}
	})

	t.Run("empty slot occupies window and old record slides out", func(t *testing.T) {
		b, _ := NewCapacityBook(10, 2, 10000, 0)
		bookMany(t, b, [][5]int64{{1, 1, 0, 10, 0}})
		if _, err := b.Settle(0, nil); err != nil {
			t.Fatal(err)
		}
		if b.CurrentOversell() != 10000 {
			t.Fatalf("O after no-show = %d", b.CurrentOversell())
		}
		if _, err := b.Settle(1, nil); err != nil {
			t.Fatal(err)
		}
		bookMany(t, b, [][5]int64{{2, 1, 2, 10, 0}})
		if _, err := b.Settle(2, []Arrival{{2, 10}}); err != nil {
			t.Fatal(err)
		}
		want := []SettlementRecord{{Reservations: 0, Arrivals: 0}, {Reservations: 10, Arrivals: 10}}
		if !reflect.DeepEqual(b.Window(), want) || b.CurrentOversell() != 0 {
			t.Fatalf("window/O = %+v/%d", b.Window(), b.CurrentOversell())
		}
	})

	t.Run("changed oversell is not retroactive", func(t *testing.T) {
		b, _ := NewCapacityBook(10, 2, 10000, 0)
		bookMany(t, b, [][5]int64{{1, 1, 0, 10, 0}})
		if _, err := b.Settle(0, nil); err != nil {
			t.Fatal(err)
		}
		if err := b.Book(2, 1, 2, 20, 0); err != nil {
			t.Fatal(err)
		}
		bookMany(t, b, [][5]int64{{3, 1, 1, 10, 0}})
		if _, err := b.Settle(1, []Arrival{{3, 10}}); err != nil {
			t.Fatal(err)
		}
		if err := b.Book(4, 1, 2, 1, 0); !errors.Is(err, ErrCapacityLimit) {
			t.Fatalf("existing booking was retroactively changed: %v", err)
		}
		result, err := b.Settle(2, []Arrival{{2, 20}})
		if err != nil {
			t.Fatal(err)
		}
		if result.WindowRecord.Reservations != 20 {
			t.Fatalf("reserved total = %d, want 20", result.WindowRecord.Reservations)
		}
	})

	t.Run("skipped slots do not enter window", func(t *testing.T) {
		b, _ := NewCapacityBook(10, 2, 10000, 0)
		bookMany(t, b, [][5]int64{{1, 1, 5, 10, 0}})
		if _, err := b.Settle(5, nil); err != nil {
			t.Fatal(err)
		}
		if b.LastSettled() != 5 || len(b.Window()) != 1 {
			t.Fatalf("last/window = %d/%+v", b.LastSettled(), b.Window())
		}
	})

	t.Run("no bump when arrivals fit capacity", func(t *testing.T) {
		b, _ := NewCapacityBook(10, 2, 0, 100)
		bookMany(t, b, [][5]int64{{1, 1, 0, 10, 0}})
		result, err := b.Settle(0, []Arrival{{1, 10}})
		if err != nil {
			t.Fatal(err)
		}
		if result.BumpedTotal != 0 || result.ServedTotal != 10 || result.TotalCompensation != 0 {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("tier then sequence and partial bump", func(t *testing.T) {
		b, _ := NewCapacityBook(10, 2, 10000, 100)
		bookMany(t, b, [][5]int64{{100, 100, 0, 10, 0}})
		if _, err := b.Settle(0, nil); err != nil {
			t.Fatal(err)
		}
		bookMany(t, b, [][5]int64{
			{1, 1, 1, 6, 0},
			{2, 2, 1, 6, 1},
			{3, 3, 1, 6, 1},
		})
		result, err := b.Settle(1, []Arrival{{1, 6}, {2, 6}, {3, 6}})
		if err != nil {
			t.Fatal(err)
		}
		one := findRecord(t, result, 1)
		two := findRecord(t, result, 2)
		three := findRecord(t, result, 3)
		if three.Bumped != 6 || two.Bumped != 2 || one.Bumped != 0 {
			t.Fatalf("bumps id1/id2/id3 = %d/%d/%d", one.Bumped, two.Bumped, three.Bumped)
		}
		if result.BumpedTotal != 8 || result.ServedTotal != 10 || result.ArrivedTotal != 18 {
			t.Fatalf("totals = %+v", result)
		}
	})

	t.Run("same owner multiple bumps increments once with prior k", func(t *testing.T) {
		b, _ := NewCapacityBook(10, 2, 10000, 10)
		bookMany(t, b, [][5]int64{{100, 100, 0, 10, 0}})
		if _, err := b.Settle(0, nil); err != nil {
			t.Fatal(err)
		}
		bookMany(t, b, [][5]int64{
			{1, 99, 1, 6, 1},
			{2, 99, 1, 6, 1},
		})
		result, err := b.Settle(1, []Arrival{{1, 6}, {2, 6}})
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalCompensation != 20 || b.BumpCount(99) != 1 {
			t.Fatalf("compensation/k = %d/%d", result.TotalCompensation, b.BumpCount(99))
		}
	})

	t.Run("bump multiplier caps at three previous bumps", func(t *testing.T) {
		b, _ := NewCapacityBook(10, 2, 10000, 10)
		bookMany(t, b, [][5]int64{{1, 99, 0, 10, 0}})
		b.bumpCounts[99] = 5
		b.window = []SettlementRecord{{Reservations: 100, Arrivals: 0}}
		if err := b.Book(2, 99, 1, 11, 0); err != nil {
			t.Fatal(err)
		}
		result, err := b.Settle(1, []Arrival{{2, 11}})
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalCompensation != 40 || b.BumpCount(99) != 6 {
			t.Fatalf("compensation/k = %d/%d", result.TotalCompensation, b.BumpCount(99))
		}
	})
}

func TestRejectionOrderAndAtomicity(t *testing.T) {
	b, _ := NewCapacityBook(10, 2, 3000, 50)
	bookMany(t, b, [][5]int64{{1, 1, 0, 10, 0}})
	if _, err := b.Settle(0, []Arrival{{1, 10}}); err != nil {
		t.Fatal(err)
	}
	bookMany(t, b, [][5]int64{{2, 2, 1, 10, 0}})

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"book invalid first", func() error { return b.Book(-1, 1, 1, 1, 0) }, ErrInvalidArgument},
		{"book duplicate before settled", func() error { return b.Book(1, 1, 0, 1, 0) }, ErrDuplicateID},
		{"book settled slot", func() error { return b.Book(100, 1, 0, 1, 0) }, ErrSlotSettled},
		{"book capacity", func() error { return b.Book(101, 1, 1, 1, 0) }, ErrCapacityLimit},
	}
	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}

	settleCases := []struct {
		name     string
		slot     int64
		arrivals []Arrival
		want     error
	}{
		{"settle invalid amount", 1, []Arrival{{1, 11}}, ErrInvalidArgument},
		{"settle unknown id", 1, []Arrival{{999, 1}}, ErrInvalidArgument},
		{"settle duplicate id", 1, []Arrival{{1, 1}, {1, 1}}, ErrInvalidArgument},
	}
	for _, tc := range settleCases {
		if _, err := b.Settle(tc.slot, tc.arrivals); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, err := b.Settle(0, nil); !errors.Is(err, ErrSlotRollback) {
		t.Errorf("rollback error = %v", err)
	}

	if b.LastSettled() != 0 || b.BumpCount(1) != 0 || b.nextSeq != 3 {
		t.Fatalf("state changed from rejected operations: %+v", b)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := [][4]int64{
		{0, 2, 100, 1},
		{1_000_001, 2, 100, 1},
		{1, 0, 100, 1},
		{1, 65, 100, 1},
		{1, 2, -1, 1},
		{1, 2, 10001, 1},
		{1, 2, 100, -1},
		{1, 2, 100, 1_000_001},
	}
	for _, input := range cases {
		if _, err := NewCapacityBook(input[0], input[1], input[2], input[3]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("NewCapacityBook(%v): %v", input, err)
		}
	}
}
