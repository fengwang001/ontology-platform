package cursor

import (
	"testing"

	"ontology/slotring"
)

func TestIsBehindBoundary(t *testing.T) {
	// 环容量 4，head=8，满环 oldest=5：差 4（want=5）仍可读，差 5（want=4）掉队。
	cases := []struct {
		want   int64
		oldest int64
		behind bool
		missed int64
	}{
		{6, 5, false, 0},
		{5, 5, false, 0}, // 恰好差 N：要读的就是最旧一条，可读
		{4, 5, true, 1},  // 差 N+1：最旧一条已被覆盖，掉队，错过 1 条
		{1, 5, true, 4},
	}
	for _, tc := range cases {
		c := New(tc.want)
		if got := c.IsBehind(tc.oldest); got != tc.behind {
			t.Errorf("want=%d oldest=%d IsBehind=%v want %v", tc.want, tc.oldest, got, tc.behind)
		}
		if got := c.Missed(tc.oldest); tc.behind && got != tc.missed {
			t.Errorf("Missed=%d want %d", got, tc.missed)
		}
	}
}

func TestAdvanceAndRecover(t *testing.T) {
	c := New(3)
	if c.Want() != 3 || c.Status() != Active {
		t.Fatal("initial cursor wrong")
	}
	c.Advance(3)
	if c.Want() != 4 {
		t.Fatalf("want=%d after advance, expect 4", c.Want())
	}
	c.MarkBehind()
	if c.Status() != Behind {
		t.Fatal("mark behind failed")
	}
	if c.IsBehind(1) {
		t.Fatal("behind cursor must not be reclassified by IsBehind")
	}
	c.Recover(9)
	if c.Status() != Active || c.Want() != 9 {
		t.Fatalf("recover cursor = (want=%d,status=%v)", c.Want(), c.Status())
	}
	c.Close()
	if c.Status() != Closed {
		t.Fatal("close failed")
	}
}

func TestLag(t *testing.T) {
	cases := []struct {
		want int64
		head int64
		lag  int64
	}{
		{1, 10, 9},
		{10, 10, 0},
		{11, 10, 0}, // 追上写入者：暂无数据，落后量记 0
	}
	for _, tc := range cases {
		c := New(tc.want)
		if got := c.Lag(tc.head); got != tc.lag {
			t.Errorf("Lag(want=%d,head=%d)=%d want %d", tc.want, tc.head, got, tc.lag)
		}
	}
}

func TestSlotViaRing(t *testing.T) {
	r := slotring.New(4)
	cases := []struct {
		want int64
		slot int
	}{
		{1, 0},
		{5, 0},
		{6, 1},
	}
	for _, tc := range cases {
		c := New(tc.want)
		if got := c.Slot(r); got != tc.slot {
			t.Errorf("Slot(want=%d)=%d want %d", tc.want, got, tc.slot)
		}
	}
}

func TestFellBehindError(t *testing.T) {
	e := FellBehindError{Missed: 7}
	if e.Error() == "" || e.Missed != 7 {
		t.Fatal("FellBehindError fields wrong")
	}
}
