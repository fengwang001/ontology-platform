package bom_test

import (
	"errors"
	"testing"

	"ontology/bom"
	"ontology/stock"
)

func newBOMWithItems(ids ...string) *bom.BOM {
	st := stock.New()
	for _, id := range ids {
		if err := st.AddItem([]byte(id), 0, 0, 0, 1, 1); err != nil {
			panic(err)
		}
	}
	return bom.New(st)
}

func TestAddComponent(t *testing.T) {
	cases := []struct {
		name          string
		items         []string
		edges         [][4]uint64 // 预置关系 {parentIdx, childIdx, per, scrap}，索引见 parent/child
		parent, child string
		per, scrap    uint64
		want          error
	}{
		{"合法声明", []string{"A", "B"}, nil, "A", "B", 2, 0, nil},
		{"边界值", []string{"A", "B"}, nil, "A", "B", 10_000, 999, nil},
		{"per为0", []string{"A", "B"}, nil, "A", "B", 0, 0, bom.ErrInvalid},
		{"per超上限", []string{"A", "B"}, nil, "A", "B", 10_001, 0, bom.ErrInvalid},
		{"scrap超上限", []string{"A", "B"}, nil, "A", "B", 1, 1000, bom.ErrInvalid},
		{"父物料号为空", []string{"A", "B"}, nil, "", "B", 1, 0, bom.ErrInvalid},
		{"父不存在", []string{"A", "B"}, nil, "X", "A", 1, 0, bom.ErrNotExist},
		{"子不存在", []string{"A", "B"}, nil, "A", "X", 1, 0, bom.ErrNotExist},
		{"非法且不存在时报参数非法", []string{"A"}, nil, "X", "Y", 0, 0, bom.ErrInvalid},
		{"重复声明冲突", []string{"A", "B"}, [][4]uint64{{0, 1, 2, 0}}, "A", "B", 3, 100, bom.ErrConflict},
		{"不存在且重复时报不存在", []string{"A", "B"}, [][4]uint64{{0, 1, 2, 0}}, "X", "B", 1, 0, bom.ErrNotExist},
		{"自环报成环", []string{"A"}, nil, "A", "A", 1, 0, bom.ErrCycle},
		{"间接成环", []string{"A", "B", "C"}, [][4]uint64{{0, 1, 1, 0}, {1, 2, 1, 0}}, "C", "A", 1, 0, bom.ErrCycle},
		{"反向边成环", []string{"A", "B"}, [][4]uint64{{0, 1, 1, 0}}, "B", "A", 1, 0, bom.ErrCycle},
		{"菱形不成环", []string{"A", "B", "C", "D"}, [][4]uint64{{0, 1, 1, 0}, {0, 2, 1, 0}, {1, 3, 1, 0}}, "C", "D", 1, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBOMWithItems(tc.items...)
			for _, e := range tc.edges {
				if err := b.AddComponent([]byte(tc.items[e[0]]), []byte(tc.items[e[1]]), e[2], e[3]); err != nil {
					t.Fatalf("预置关系失败: %v", err)
				}
			}
			before := b.Components()
			err := b.AddComponent([]byte(tc.parent), []byte(tc.child), tc.per, tc.scrap)
			if !errors.Is(err, tc.want) {
				t.Fatalf("AddComponent 错误 = %v, 期望 %v", err, tc.want)
			}
			if err != nil && len(b.Components()) != len(before) {
				t.Fatalf("被拒绝的 AddComponent 改变了状态")
			}
		})
	}
}

func TestComponentsSnapshotSorted(t *testing.T) {
	b := newBOMWithItems("A", "B", "C")
	_ = b.AddComponent([]byte("B"), []byte("C"), 1, 0)
	_ = b.AddComponent([]byte("A"), []byte("C"), 2, 10)
	_ = b.AddComponent([]byte("A"), []byte("B"), 3, 20)
	got := b.Components()
	want := []bom.Component{
		{Parent: "A", Child: "B", Per: 3, Scrap: 20},
		{Parent: "A", Child: "C", Per: 2, Scrap: 10},
		{Parent: "B", Child: "C", Per: 1, Scrap: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("关系数 = %d, 期望 %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Components()[%d] = %+v, 期望 %+v", i, got[i], want[i])
		}
	}
}
