package shard

import (
	"reflect"
	"testing"
)

func TestLowerMedian(t *testing.T) {
	cases := []struct {
		name string
		vals []int64
		want int64
	}{
		{"empty", nil, 1},
		{"odd", []int64{9, 5, 7, 6, 5}, 6}, // 升序 5,5,6,7,9 取第 3 个
		{"even", []int64{9, 5, 7, 6}, 6},   // 升序 5,6,7,9 取第 2 个
		{"single", []int64{42}, 42},
		{"two", []int64{10, 3}, 3}, // 升序 3,10 取第 1 个
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LowerMedian(c.vals); got != c.want {
				t.Fatalf("LowerMedian(%v) = %d, want %d", c.vals, got, c.want)
			}
		})
	}
}

func TestAssign(t *testing.T) {
	cases := []struct {
		name  string
		ests  map[string]int64
		names []string
		n     int
		want  [][]string
	}{
		{
			// 规格示例：est 为 9,7,6,5,5，f 取下中位数 6。
			name:  "spec example",
			ests:  map[string]int64{"a": 9, "b": 7, "c": 6, "d": 5, "e": 5, "f": 6},
			names: []string{"a", "b", "c", "d", "e", "f"},
			n:     2,
			want:  [][]string{{"a", "f", "e"}, {"b", "c", "d"}},
		},
		{
			// est 并列时按名字升序放入；总和并列时取下标小者。
			name:  "tie by name then index",
			ests:  map[string]int64{"x": 5, "y": 5, "z": 5},
			names: []string{"z", "y", "x"},
			n:     3,
			want:  [][]string{{"x"}, {"y"}, {"z"}},
		},
		{
			// 总和并列（都为 0）时全部先填下标小者的对立检查：
			// 每次放入后总和不再并列，验证取当前最小者。
			name:  "lowest sum wins",
			ests:  map[string]int64{"a": 4, "b": 3, "c": 2, "d": 1},
			names: []string{"a", "b", "c", "d"},
			n:     2,
			want:  [][]string{{"a", "d"}, {"b", "c"}},
		},
		{
			name:  "single shard keeps insertion order",
			ests:  map[string]int64{"b": 1, "a": 2, "c": 3},
			names: []string{"a", "b", "c"},
			n:     1,
			want:  [][]string{{"c", "a", "b"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Assign(c.names, func(s string) int64 { return c.ests[s] }, c.n)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Assign = %v, want %v", got, c.want)
			}
		})
	}
}
