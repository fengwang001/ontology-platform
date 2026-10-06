package speq

import "testing"

func ord(y, m, d int) int {
	o, ok := DateToOrdinal(y, m, d)
	if !ok {
		panic("bad date")
	}
	return o
}

func TestCalendarRoundtrip(t *testing.T) {
	for _, o := range []int{0, 1, 365, 366, 700000, 735000} {
		y, m, d, ok := OrdinalToDate(o)
		if !ok {
			t.Fatalf("ordinal %d invalid", o)
		}
		back, _ := DateToOrdinal(y, m, d)
		if back != o {
			t.Fatalf("roundtrip %d -> %04d-%02d-%02d -> %d", o, y, m, d, back)
		}
	}
}

func TestMonthEndClamp(t *testing.T) {
	// 2020-01-31 +1 月 = 2020-02-29（闰年月末）
	jan31 := ord(2020, 1, 31)
	if got := AddCalendarMonths(jan31, 1); got != ord(2020, 2, 29) {
		t.Fatalf("leap clamp: got %d want 2020-02-29", got)
	}
	// 2019-01-31 +1 月 = 2019-02-28（平年月末）
	jan31p := ord(2019, 1, 31)
	if got := AddCalendarMonths(jan31p, 1); got != ord(2019, 2, 28) {
		t.Fatalf("flat clamp: got %d", got)
	}
	// 2020-03-31 +1 月 = 2020-04-30
	mar31 := ord(2020, 3, 31)
	if got := AddCalendarMonths(mar31, 1); got != ord(2020, 4, 30) {
		t.Fatalf("apr clamp: got %d", got)
	}
	// 月末加 12 个月仍是月末（2020-02-29 -> 2021-02-28）
	feb29 := ord(2020, 2, 29)
	if got := AddCalendarMonths(feb29, 12); got != ord(2021, 2, 28) {
		t.Fatalf("year clamp: got %d", got)
	}
	// 普通日号保持
	if got := AddCalendarMonths(ord(2020, 1, 15), 13); got != ord(2021, 2, 15) {
		t.Fatalf("normal add: got %d", got)
	}
}

func TestInvalidDate(t *testing.T) {
	if _, ok := DateToOrdinal(2021, 2, 29); ok {
		t.Fatal("2021-02-29 should be invalid")
	}
	if _, _, _, ok := OrdinalToDate(-1); ok {
		t.Fatal("-1 should be invalid")
	}
}
