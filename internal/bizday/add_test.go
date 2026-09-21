package bizday

import (
	"errors"
	"testing"
)

var plainCalendar = mustNew(nil, nil)

func TestAddBusinessDays_Positive(t *testing.T) {
	cases := []struct {
		name string
		d, n int
		want int
	}{
		{"周五加一跳过周末", 20260925, 1, 20260928},
		{"周六加一自身不计", 20260926, 1, 20260928},
		{"周一加五到下周一", 20260921, 5, 20260928},
		{"跨月", 20260930, 1, 20261001},
		{"跨年", 20261231, 1, 20270101},
		{"跨闰年2月29日", 20240228, 1, 20240229},
		{"非闰年2月底", 20230228, 1, 20230301},
	}
	for _, tc := range cases {
		got, err := plainCalendar.AddBusinessDays(tc.d, tc.n)
		if err != nil {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: AddBusinessDays(%d, %d) = %d, want %d",
				tc.name, tc.d, tc.n, got, tc.want)
		}
	}
}

func TestAddBusinessDays_Negative(t *testing.T) {
	cases := []struct {
		name string
		d, n int
		want int
	}{
		{"周一减一到上周五", 20260921, -1, 20260918},
		{"周一减五到上周一", 20260928, -5, 20260921},
		{"跨月向前", 20261001, -1, 20260930},
		{"跨年向前", 20270101, -1, 20261231},
		{"跨闰年2月29日向前", 20240301, -1, 20240229},
	}
	for _, tc := range cases {
		got, err := plainCalendar.AddBusinessDays(tc.d, tc.n)
		if err != nil {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: AddBusinessDays(%d, %d) = %d, want %d",
				tc.name, tc.d, tc.n, got, tc.want)
		}
	}
}

func TestAddBusinessDays_Zero(t *testing.T) {
	got, err := plainCalendar.AddBusinessDays(20260923, 0)
	if err != nil || got != 20260923 {
		t.Errorf("AddBusinessDays(周三, 0) = %d, %v; want 20260923, nil", got, err)
	}
	if _, err := plainCalendar.AddBusinessDays(20260926, 0); !errors.Is(err, ErrNotBusinessDay) {
		t.Errorf("AddBusinessDays(周六, 0) = %v, want ErrNotBusinessDay", err)
	}
	if _, err := testCalendar.AddBusinessDays(20260923, 0); !errors.Is(err, ErrNotBusinessDay) {
		t.Errorf("AddBusinessDays(节假日, 0) = %v, want ErrNotBusinessDay", err)
	}
}

func TestAddBusinessDays_WithCalendar(t *testing.T) {
	// 20260923 是节假日，应被跳过。
	if got, _ := testCalendar.AddBusinessDays(20260922, 1); got != 20260924 {
		t.Errorf("跳过节假日 = %d, want 20260924", got)
	}
	// 20260926 同时在两者中按节假日跳过，20260927 调休上班。
	if got, _ := testCalendar.AddBusinessDays(20260925, 1); got != 20260927 {
		t.Errorf("节假日优先于调休 = %d, want 20260927", got)
	}
}

func TestAddBusinessDays_Errors(t *testing.T) {
	if _, err := plainCalendar.AddBusinessDays(20260230, 1); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("非法日期 = %v, want ErrInvalidDate", err)
	}
	if _, err := plainCalendar.AddBusinessDays(29991231, 1); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("计数越界 = %v, want ErrInvalidDate", err)
	}
	if _, err := plainCalendar.AddBusinessDays(19000101, -1); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("向前越界 = %v, want ErrInvalidDate", err)
	}
}
