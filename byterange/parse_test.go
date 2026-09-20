package byterange

import (
	"errors"
	"reflect"
	"testing"
)

// 语义 1：三种写法都归一化为绝对闭区间。
func TestParseThreeForms(t *testing.T) {
	cases := []struct {
		header string
		want   []Range
	}{
		{"bytes=0-499", []Range{{0, 499}}},
		{"bytes=500-", []Range{{500, 999}}},
		{"bytes=-500", []Range{{500, 999}}},
		{"bytes=0-0", []Range{{0, 0}}},
	}
	for _, c := range cases {
		got, err := Parse(c.header, 1000)
		if err != nil {
			t.Fatalf("Parse(%q) err = %v", c.header, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

// 语义 2：越界裁剪而非报错。
func TestParseClamping(t *testing.T) {
	cases := []struct {
		header string
		want   []Range
	}{
		{"bytes=0-999999", []Range{{0, 999}}},
		{"bytes=-999999", []Range{{0, 999}}},
		{"bytes=999-999999", []Range{{999, 999}}},
	}
	for _, c := range cases {
		got, err := Parse(c.header, 1000)
		if err != nil {
			t.Fatalf("Parse(%q) err = %v", c.header, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

// 语义 3：单个不可满足跳过，全部不可满足才报 ErrUnsatisfiable。
func TestParseUnsatisfiable(t *testing.T) {
	got, err := Parse("bytes=1000-,0-9", 1000)
	if err != nil || !reflect.DeepEqual(got, []Range{{0, 9}}) {
		t.Errorf("partial = %v, %v", got, err)
	}
	for _, h := range []string{"bytes=1000-", "bytes=-0", "bytes=1000-,2000-"} {
		if got, err := Parse(h, 1000); !errors.Is(err, ErrUnsatisfiable) || got != nil {
			t.Errorf("Parse(%q) = %v, %v; want nil, ErrUnsatisfiable", h, got, err)
		}
	}
}

// 语义 4：size 为 0 时任何区间都不可满足。
func TestParseZeroSize(t *testing.T) {
	for _, h := range []string{"bytes=0-", "bytes=0-0", "bytes=-1"} {
		if got, err := Parse(h, 0); !errors.Is(err, ErrUnsatisfiable) || got != nil {
			t.Errorf("Parse(%q, 0) = %v, %v; want nil, ErrUnsatisfiable", h, got, err)
		}
	}
}

// 语义 5：重叠与相邻合并，结果升序且互不相交。
func TestParseMerge(t *testing.T) {
	got, err := Parse("bytes=100-199,0-99,200-299,500-", 1000)
	if err != nil {
		t.Fatal(err)
	}
	want := []Range{{0, 299}, {500, 999}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merge = %v, want %v", got, want)
	}
	got, err = Parse("bytes=10-20,5-15", 1000)
	if err != nil || !reflect.DeepEqual(got, []Range{{5, 20}}) {
		t.Errorf("overlap = %v, %v", got, err)
	}
}
