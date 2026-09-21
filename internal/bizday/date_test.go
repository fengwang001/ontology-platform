package bizday

import (
	"errors"
	"testing"
)

func TestValidateDate_Valid(t *testing.T) {
	valid := []int{
		19000101, 29991231, 20260921,
		20240229, // 闰年 2 月 29 日
		20000229, // 能被 400 整除，闰年
		20260131, 20260430,
	}
	for _, d := range valid {
		if err := validateDate(d); err != nil {
			t.Errorf("validateDate(%d) = %v, want nil", d, err)
		}
	}
}

func TestValidateDate_Invalid(t *testing.T) {
	invalid := []int{
		20260230, // 2 月没有 30 日
		20261301, // 月份 13
		20260931, // 9 月只有 30 天
		20230229, // 非闰年 2 月 29 日
		21000229, // 能被 100 整除但不能被 400 整除，非闰年
		18991231, // 年份低于下限
		30000101, // 年份高于上限
		20260001, // 日为 0
		20260100, // 日为 0
	}
	for _, d := range invalid {
		err := validateDate(d)
		if !errors.Is(err, ErrInvalidDate) {
			t.Errorf("validateDate(%d) = %v, want ErrInvalidDate", d, err)
		}
	}
}

func TestWeekday(t *testing.T) {
	// 2026-09-21 是周一。
	cases := []struct {
		d    int
		want int // 0=周一 ... 6=周日
	}{
		{20260921, 0},
		{20260922, 1},
		{20260923, 2},
		{20260924, 3},
		{20260925, 4},
		{20260926, 5},
		{20260927, 6},
		{20260928, 0},
		{20240229, 3}, // 2024-02-29 周四
		{20000101, 5}, // 2000-01-01 周六
		{19000101, 0}, // 1900-01-01 周一
	}
	for _, tc := range cases {
		if got := weekday(tc.d); got != tc.want {
			t.Errorf("weekday(%d) = %d, want %d", tc.d, got, tc.want)
		}
	}
}

func TestShiftDay(t *testing.T) {
	cases := []struct {
		d, delta, want int
	}{
		{20260921, 1, 20260922},
		{20260921, -1, 20260920},
		{20260930, 1, 20261001}, // 跨月
		{20261001, -1, 20260930},
		{20261231, 1, 20270101}, // 跨年
		{20270101, -1, 20261231},
		{20240228, 1, 20240229}, // 跨闰年 2 月 29 日
		{20240301, -1, 20240229},
		{20230228, 1, 20230301},  // 非闰年
		{19000101, -1, 18991231}, // 越界由调用方校验
	}
	for _, tc := range cases {
		if got := shiftDay(tc.d, tc.delta); got != tc.want {
			t.Errorf("shiftDay(%d, %d) = %d, want %d", tc.d, tc.delta, got, tc.want)
		}
	}
}
