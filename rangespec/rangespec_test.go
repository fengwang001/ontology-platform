package rangespec

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		header string
		want   []Spec
	}{
		{"bytes=0-99", []Spec{{Start: 0, End: 99}}},
		{"bytes=100-", []Spec{{Start: 100, End: -1}}},
		{"bytes=-50", []Spec{{Start: -1, Suffix: 50}}},
		{"bytes=-0", []Spec{{Start: -1, Suffix: 0}}}, // 语法合法，归一化时才判不可满足
		{"bytes=0-0", []Spec{{Start: 0, End: 0}}},
		{"bytes=0-1, 4-5,-3", []Spec{{Start: 0, End: 1}, {Start: 4, End: 5}, {Start: -1, Suffix: 3}}},
		{"BYTES=7-8", []Spec{{Start: 7, End: 8}}},
		{"bytes= 10-20 ", []Spec{{Start: 10, End: 20}}},
	}
	for _, c := range cases {
		got, err := Parse(c.header)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", c.header, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q) = %+v, want %+v", c.header, got, c.want)
		}
	}
}

func TestParseSyntaxError(t *testing.T) {
	cases := []struct {
		header  string
		wantOff int
	}{
		{"items=0-1", 0},                       // 缺少 bytes= 单位
		{"bytes=", 6},                          // 空区间
		{"bytes=ab-1", 6},                      // 起点非数字
		{"bytes=1-cd", 8},                      // 终点非数字
		{"bytes=-", 6},                         // 只有横线
		{"bytes=0-1,zz", 10},                   // 第二项无横线
		{"bytes=99999999999999999999999-1", 6}, // 溢出
	}
	for _, c := range cases {
		_, err := Parse(c.header)
		var syn *SyntaxError
		if !errors.As(err, &syn) {
			t.Errorf("Parse(%q): want *SyntaxError, got %v", c.header, err)
			continue
		}
		if syn.Offset != c.wantOff {
			t.Errorf("Parse(%q) offset = %d, want %d", c.header, syn.Offset, c.wantOff)
		}
		var unsat *UnsatisfiableError
		if errors.As(err, &unsat) {
			t.Errorf("Parse(%q): syntax error must not match UnsatisfiableError", c.header)
		}
	}
}
