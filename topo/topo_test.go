package topo

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"ontology/dag"
)

func build(t *testing.T, n int, edges [][2]int) *dag.Graph {
	t.Helper()
	g := dag.New(n)
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("AddEdge(%d,%d): %v", e[0], e[1], err)
		}
	}
	return g
}

// TestMinFirstOrder 钉住「最小编号优先」的精确输出（不变量2）。
func TestMinFirstOrder(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		edges [][2]int
		want  []int
	}{
		{"第三节七边", 7, [][2]int{{2, 0}, {2, 1}, {4, 1}, {3, 1}, {5, 6}, {0, 6}, {1, 6}}, []int{2, 0, 3, 4, 1, 5, 6}},
		{"空图", 4, nil, []int{0, 1, 2, 3}},
		{"单点", 1, nil, []int{0}},
		{"链", 4, [][2]int{{3, 2}, {2, 1}, {1, 0}}, []int{3, 2, 1, 0}},
		{"并列取小", 5, [][2]int{{4, 0}, {1, 3}}, []int{1, 2, 3, 4, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Sort(build(t, c.n, c.edges))
			if err != nil {
				t.Fatalf("Sort: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestCycle 钉住含环整体失败、无部分序列（不变量3）。
func TestCycle(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		edges [][2]int
	}{
		{"二元环", 2, [][2]int{{0, 1}, {1, 0}}},
		{"第三节加0→2", 7, [][2]int{{2, 0}, {2, 1}, {4, 1}, {3, 1}, {5, 6}, {0, 6}, {1, 6}, {0, 2}}},
		{"大环带无环部分", 5, [][2]int{{4, 3}, {1, 2}, {2, 3}, {3, 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Sort(build(t, c.n, c.edges))
			if !errors.Is(err, ErrCycle) {
				t.Fatalf("err = %v, want ErrCycle", err)
			}
			if got != nil {
				t.Fatalf("got partial order %v, want nil", got)
			}
		})
	}
}

// TestCheckedBounded 钉住复杂度约束：m 个互不相连节点，每步选节点
// 检查的候选数不随 m 增长（≤2），证明用了堆而非全表扫描。
func TestCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run("m="+strconv.Itoa(m), func(t *testing.T) {
			indeg := make([]int, m)
			adj := make([][]int, m)
			s := &sorter{}
			order, err := s.run(indeg, adj)
			if err != nil || len(order) != m {
				t.Fatalf("run: %v, len=%d", err, len(order))
			}
			if s.checked > 2 || s.maxChecked > 2 {
				t.Fatalf("m=%d: checked=%d maxChecked=%d, want <=2", m, s.checked, s.maxChecked)
			}
		})
	}
}
