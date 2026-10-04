package nhgroup

import (
	"reflect"
	"testing"
)

func TestTargets(t *testing.T) {
	tests := []struct {
		name  string
		n     int
		specs []Spec
		alive map[int]bool
		want  map[int]int
	}{
		{"等权建组", 8, []Spec{{1, 1}, {2, 1}}, nil, map[int]int{1: 4, 2: 4}},
		{"三人均权余数并列编号小者多得", 8, []Spec{{1, 1}, {2, 1}, {3, 1}}, nil, map[int]int{1: 3, 2: 3, 3: 2}},
		{"权重 1:1:2@N8", 8, []Spec{{1, 1}, {2, 1}, {3, 2}}, nil, map[int]int{1: 2, 2: 2, 3: 4}},
		{"余数并列取编号小", 10, []Spec{{5, 1}, {9, 1}, {3, 1}}, nil, map[int]int{3: 4, 5: 3, 9: 3}},
		{"N 小于成员数", 2, []Spec{{1, 1}, {2, 1}, {3, 1}}, nil, map[int]int{1: 1, 2: 1, 3: 0}},
		{"失效成员不参与分配", 8, []Spec{{1, 1}, {2, 1}, {3, 2}}, map[int]bool{3: false}, map[int]int{1: 4, 2: 4}},
		{"全部失效目标为空", 8, []Spec{{1, 1}}, map[int]bool{1: false}, map[int]int{}},
		{"大权重无余数", 4096, []Spec{{7, 3}, {8, 5}}, nil, map[int]int{7: 1536, 8: 2560}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := New(tt.specs)
			for nh, a := range tt.alive {
				g.SetAlive(nh, a)
			}
			got := g.Targets(tt.n)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Targets=%v want %v | 依据: 最大余数法 floor 后余数降序并列编号升序", got, tt.want)
			}
		})
	}
}

func TestMembership(t *testing.T) {
	g := New([]Spec{{3, 2}, {1, 5}})
	if got := g.Members(); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("Members=%v want [1 3]", got)
	}
	g.Add(2, 9, true)
	if got := g.Members(); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("Add 后 Members=%v", got)
	}
	g.SetWeight(2, 1)
	if g.Weight(2) != 1 {
		t.Fatalf("SetWeight 失效")
	}
	g.SetAlive(2, false)
	if got := g.AliveMembers(); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("AliveMembers=%v want [1 3]", got)
	}
	g.Remove(1)
	if g.Has(1) {
		t.Fatalf("Remove 后仍存在")
	}
	if got := g.Members(); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Fatalf("Remove 后 Members=%v", got)
	}
}
