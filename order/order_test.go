package order

import (
	"reflect"
	"testing"
)

func TestSteps(t *testing.T) {
	cases := []struct {
		name         string
		n, b1, c, b2 int
		want         []Step
	}{
		{
			name: "题面示例 n=2 b1=1 c=2 b2=1",
			n:    2, b1: 1, c: 2, b2: 1,
			want: []Step{
				{Ban, 1, 0}, {Ban, 2, 0},
				{Pick, 1, 0}, {Pick, 2, 1},
				{Ban, 2, 0}, {Ban, 1, 0}, // 第 2 手归队 2，二阶段队 2 先禁
				{Pick, 2, 2}, {Pick, 1, 3},
			},
		},
		{
			name: "无禁用 n=1",
			n:    1, b1: 0, c: 0, b2: 0,
			want: []Step{{Pick, 1, 0}, {Pick, 2, 1}},
		},
		{
			name: "c=2n 时无二阶段禁用",
			n:    2, b1: 1, c: 4, b2: 0,
			want: []Step{
				{Ban, 1, 0}, {Ban, 2, 0},
				{Pick, 1, 0}, {Pick, 2, 1}, {Pick, 2, 2}, {Pick, 1, 3},
			},
		},
		{
			name: "c=0 时二阶段由第 0 手所属队 1 先禁",
			n:    2, b1: 0, c: 0, b2: 1,
			want: []Step{
				{Ban, 1, 0}, {Ban, 2, 0},
				{Pick, 1, 0}, {Pick, 2, 1}, {Pick, 2, 2}, {Pick, 1, 3},
			},
		},
		{
			name: "n=5 c=6 二阶段由队 2 先禁",
			n:    5, b1: 0, c: 6, b2: 1,
			want: []Step{
				{Pick, 1, 0}, {Pick, 2, 1}, {Pick, 2, 2}, {Pick, 1, 3},
				{Pick, 1, 4}, {Pick, 2, 5},
				{Ban, 2, 0}, {Ban, 1, 0},
				{Pick, 2, 6}, {Pick, 1, 7}, {Pick, 1, 8}, {Pick, 2, 9},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Steps(tc.n, tc.b1, tc.c, tc.b2)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Steps(%d,%d,%d,%d)=%v, want %v", tc.n, tc.b1, tc.c, tc.b2, got, tc.want)
			}
		})
	}
}

func TestPickTeam(t *testing.T) {
	// 题面：n=5、c=6 时选人归属依次为 1、2、2、1、1、2、2、1、1、2
	want := []int{1, 2, 2, 1, 1, 2, 2, 1, 1, 2}
	for i, w := range want {
		if got := PickTeam(i); got != w {
			t.Fatalf("PickTeam(%d)=%d, want %d", i, got, w)
		}
	}
}
