package schedule

import "testing"

func TestParseDateAndWeekday(t *testing.T) {
	cases := []struct {
		in string
		w  int
		ok bool
	}{
		{"1970-01-01", 4, true},
		{"2000-02-29", 2, true}, // 闰年 2 月最后一天，周二
		{"2024-02-29", 4, true},
		{"2200-12-31", 3, true},
		{"2201-01-01", 0, false},
		{"1969-12-31", 0, false},
		{"2023-02-29", 0, false}, // 平年 2 月 30/29 不存在
		{"2024-02-30", 0, false},
		{"2024-13-01", 0, false},
		{"2024-00-01", 0, false},
		{"2024-4-01", 0, false},
		{"20240401", 0, false},
		{"abcd-ef-gh", 0, false},
	}
	for _, c := range cases {
		d, err := ParseDate(c.in)
		if c.ok {
			if err != nil {
				t.Fatalf("ParseDate(%q) unexpected error: %v", c.in, err)
			}
			if got := d.Weekday(); got != c.w {
				t.Fatalf("Weekday(%q)=%d want %d", c.in, got, c.w)
			}
			if d.String() != c.in {
				t.Fatalf("String()=%q want %q", d.String(), c.in)
			}
			if c.in != "2200-12-31" {
				if nd, ok := d.addDays(1); !ok || nd.Before(d) {
					t.Fatalf("addDays(1) broken for %q: ok=%v", c.in, ok)
				}
			}
		} else if err == nil {
			t.Fatalf("ParseDate(%q) expected error, got %v", c.in, d)
		} else if code(err) != ErrInvalidDate {
			t.Fatalf("ParseDate(%q) code=%s want %s", c.in, code(err), ErrInvalidDate)
		}
		t.Logf("判定 ParseDate(%q) => ok=%v weekday=%d", c.in, c.ok, c.w)
	}
}

func TestNthWeekdayInMonth(t *testing.T) {
	// 2024-01：周一为 1,8,15,22,29（存在第 5 个周一）
	d, ok := nthWeekdayInMonth(2024, 1, 5, 1)
	if !ok || d.String() != "2024-01-29" {
		t.Fatalf("5th Monday 2024-01 = %v,%v want 2024-01-29", d, ok)
	}
	// 2024-02：周五为 2,9,16,23（不存在第 5 个周五，跳过）
	if _, ok := nthWeekdayInMonth(2024, 2, 5, 5); ok {
		t.Fatal("5th Friday 2024-02 must not exist")
	}
	// 2024-02 最后一个周五 = 23
	d, ok = nthWeekdayInMonth(2024, 2, -1, 5)
	if !ok || d.String() != "2024-02-23" {
		t.Fatalf("last Friday 2024-02 = %v,%v want 2024-02-23", d, ok)
	}
	// 2024-03 最后一个周日 = 31
	d, ok = nthWeekdayInMonth(2024, 3, -1, 7)
	if !ok || d.String() != "2024-03-31" {
		t.Fatalf("last Sunday 2024-03 = %v,%v want 2024-03-31", d, ok)
	}
	t.Logf("判定依据：按月内首个 w 的日序 firstOcc=1+(w-firstW+7)%%7，第 nth 个为 firstOcc+7*(nth-1)，越界即不存在；-1 取最后一个")
}

func code(err error) ErrorCode {
	if e, ok := err.(*OpError); ok {
		return e.Code
	}
	return ""
}
