package version

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		lo   int
		hi   int
		ok   bool
		note string
	}{
		{"1", 1, 1, true, "单数字"},
		{"4", 4, 4, true, "单数字"},
		{"1000", 1000, 1000, true, "上界单数字"},
		{"1-1", 1, 1, true, "等长区间"},
		{"1-3", 1, 3, true, "普通区间"},
		{"100-200", 100, 200, true, "多位区间"},
		{"1-1000", 1, 1000, true, "到上界"},
		{"1-", 1, 1000, true, "开放尾"},
		{"500-", 500, 1000, true, "开放尾多位数"},
		{"1000-", 1000, 1000, true, "上界开放尾"},

		{"", 0, 0, false, "空串"},
		{"0", 0, 0, false, "零越界"},
		{"01", 0, 0, false, "前导零"},
		{"007", 0, 0, false, "前导零多位"},
		{"+1", 0, 0, false, "带符号"},
		{"-1", 0, 0, false, "负号开头"},
		{" 1", 0, 0, false, "空白"},
		{"1 ", 0, 0, false, "尾部空白"},
		{"1001", 0, 0, false, "超上界"},
		{"1-0", 0, 0, false, "下界大于上界且零非法"},
		{"2-1", 0, 0, false, "lo>hi"},
		{"1-01", 0, 0, false, "hi 前导零"},
		{"01-2", 0, 0, false, "lo 前导零"},
		{"1-1001", 0, 0, false, "hi 越界"},
		{"a", 0, 0, false, "非数字"},
		{"1-a", 0, 0, false, "右段非数字"},
		{"1--", 0, 0, false, "多连字符"},
		{"1-2-3", 0, 0, false, "多段"},
		{"-", 0, 0, false, "仅连字符"},
		{"-2", 0, 0, false, "空前缀"},
		{"1 -2", 0, 0, false, "内嵌空白"},
		{"000-1", 0, 0, false, "lo 为零串"},
	}
	for _, c := range cases {
		t.Run(c.in+"/"+c.note, func(t *testing.T) {
			r, err := Parse(c.in)
			if c.ok {
				if err != nil {
					t.Fatalf("Parse(%q) 返回错误 %v，依据：%s", c.in, err, c.note)
				}
				if r.Lo != c.lo || r.Hi != c.hi {
					t.Fatalf("Parse(%q) = %+v，期望 [%d,%d]，依据：%s", c.in, r, c.lo, c.hi, c.note)
				}
				t.Logf("输入=%q 输出=[%d,%d] 判定依据=%s", c.in, r.Lo, r.Hi, c.note)
			} else if !errors.Is(err, ErrSyntax) {
				t.Fatalf("Parse(%q) err=%v，期望 ErrSyntax，依据：%s", c.in, err, c.note)
			} else {
				t.Logf("输入=%q 输出=语法错误 判定依据=%s", c.in, c.note)
			}
		})
	}
}
