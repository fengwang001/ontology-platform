package catalog

import (
	"errors"
	"testing"
)

func TestAddItem(t *testing.T) {
	cases := []struct {
		name  string
		code  string
		class string
		p     int
		limit int64
		want  error
	}{
		{"甲类p必须为0", "A1", ClassA, 1, 0, ErrInvalidParam},
		{"丙类p必须为0", "C1", ClassC, 5, 0, ErrInvalidParam},
		{"乙类p下界", "B0", ClassB, 0, 0, ErrInvalidParam},
		{"乙类p上界", "B100", ClassB, 100, 0, ErrInvalidParam},
		{"乙类合法", "B1", ClassB, 10, 5000, nil},
		{"甲类合法", "A2", ClassA, 0, 0, nil},
		{"丙类合法", "C2", ClassC, 0, 0, nil},
		{"未知类别", "X", "丁", 0, 0, ErrInvalidParam},
		{"空编码", "", ClassA, 0, 0, ErrInvalidParam},
		{"限价超界", "A3", ClassA, 0, maxMoney + 1, ErrInvalidParam},
	}
	c := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.AddItem(tc.code, tc.class, tc.p, tc.limit)
			if !errors.Is(err, tc.want) {
				t.Fatalf("AddItem(%s) = %v, want %v", tc.name, err, tc.want)
			}
		})
	}
	if err := c.AddItem("B1", ClassB, 10, 5000); !errors.Is(err, ErrDuplicateCode) {
		t.Fatalf("重复编码应报 ErrDuplicateCode，got %v", err)
	}
}

func TestCalcLine(t *testing.T) {
	c := New()
	must(t, c.AddItem("jia", ClassA, 0, 10000))
	must(t, c.AddItem("yi", ClassB, 10, 5000))
	must(t, c.AddItem("bing", ClassC, 0, 0))
	must(t, c.AddItem("yi-nolimit", ClassB, 10, 0))
	must(t, c.AddItem("yi100", ClassB, 10, 100))

	cases := []struct {
		name string
		line Line
		want LineAmount
	}{
		{"甲类限价: a=12000 b=10000 c=0 e=10000",
			Line{"jia", 12000, 1}, LineAmount{12000, 10000, 0, 10000}},
		{"乙类逐条: a=b=9999 c=ceil(999.9)=1000 e=8999",
			Line{"yi", 3333, 3}, LineAmount{9999, 9999, 1000, 8999}},
		{"丙类全额自付 e=0",
			Line{"bing", 5000, 1}, LineAmount{5000, 5000, 5000, 0}},
		{"乙类无限价: c=ceil(3333*1*10/100)=334",
			Line{"yi-nolimit", 3333, 1}, LineAmount{3333, 3333, 334, 2999}},
		{"限价逐条取整: 单价101 限价100 数量3 b=300 c=ceil(30)=30",
			Line{"yi100", 101, 3}, LineAmount{303, 300, 30, 270}},
		{"乙类整除不加点: b=5000 p=10 c=500 e=4500",
			Line{"yi", 5000, 1}, LineAmount{5000, 5000, 500, 4500}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.CalcLine(tc.line)
			if err != nil {
				t.Fatalf("CalcLine err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("CalcLine = %+v, want %+v", got, tc.want)
			}
			t.Logf("输入 %+v => a=%d b=%d c=%d e=%d", tc.line, got.A, got.B, got.C, got.E)
		})
	}

	// 参数与缺失目录项。
	if _, err := c.CalcLine(Line{"yi", 0, 1}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("非法单价, got %v", err)
	}
	if _, err := c.CalcLine(Line{"nope", 1, 1}); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("缺失目录, got %v", err)
	}
}

func TestCalcMissingIndex(t *testing.T) {
	c := New()
	must(t, c.AddItem("a", ClassA, 0, 0))
	_, _, _, idx, err := c.Calc([]Line{
		{"a", 100, 1},
		{"x", 100, 1},
		{"y", 100, 1},
	})
	if !errors.Is(err, ErrCodeNotFound) || idx != 1 {
		t.Fatalf("应报下标 1, got idx=%d err=%v", idx, err)
	}
	if _, _, _, _, err := c.Calc(nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("空明细应非法, got %v", err)
	}
	big := make([]Line, 201)
	if _, _, _, _, err := c.Calc(big); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("201 条应非法, got %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
