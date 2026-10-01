package indexorder

import (
	"errors"
	"sync"
	"testing"
)

func item(col string, dir Direction, nulls NullsPos) ColumnItem {
	return ColumnItem{Column: col, Dir: dir, Nulls: nulls}
}

func mustRegister(t *testing.T, r *Registry, idx Index) {
	t.Helper()
	if err := r.Register(idx); err != nil {
		t.Fatalf("Register(%q) 失败: %v", idx.Name, err)
	}
}

func choose(t *testing.T, r *Registry, eq []string, order []ColumnItem) (string, ScanDirection) {
	t.Helper()
	name, dir, err := r.Choose(eq, order)
	if err != nil {
		t.Fatalf("Choose(eq=%v, order=%v) 失败: %v", eq, order, err)
	}
	return name, dir
}

// 索引 (a ASC NULLS LAST, b DESC NULLS FIRST) 对同序 ORDER BY 正向满足。
func TestForwardSameOrder(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "i1", Items: []ColumnItem{
		item("a", Asc, NullsLast),
		item("b", Desc, NullsFirst),
	}})
	name, dir := choose(t, r, nil, []ColumnItem{
		item("a", Asc, NullsLast),
		item("b", Desc, NullsFirst),
	})
	if name != "i1" || dir != Forward {
		t.Fatalf("got (%q, %v), want (i1, Forward)", name, dir)
	}
}

// 同一索引对全部取反的 ORDER BY 反向满足。
func TestBackwardFlippedOrder(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "i1", Items: []ColumnItem{
		item("a", Asc, NullsLast),
		item("b", Desc, NullsFirst),
	}})
	name, dir := choose(t, r, nil, []ColumnItem{
		item("a", Desc, NullsFirst),
		item("b", Asc, NullsLast),
	})
	if name != "i1" || dir != Backward {
		t.Fatalf("got (%q, %v), want (i1, Backward)", name, dir)
	}
}

// 只取反方向而不取反空值位置不满足。
func TestFlipDirWithoutNullsFails(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "i1", Items: []ColumnItem{
		item("a", Asc, NullsLast),
		item("b", Desc, NullsFirst),
	}})
	_, _, err := r.Choose(nil, []ColumnItem{
		item("a", Desc, NullsLast),
		item("b", Asc, NullsFirst),
	})
	if !errors.Is(err, ErrNoSatisfyingIndex) {
		t.Fatalf("got err=%v, want ErrNoSatisfyingIndex", err)
	}
}

// eq 列夹在索引中间被跳过：索引 (a,b,c)、eq{b}、order (a,c) 满足。
func TestEqColumnSkippedInMiddle(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "i1", Items: []ColumnItem{
		item("a", Asc, NullsFirst),
		item("b", Desc, NullsLast),
		item("c", Asc, NullsLast),
	}})
	name, dir := choose(t, r, []string{"b"}, []ColumnItem{
		item("a", Asc, NullsFirst),
		item("c", Asc, NullsLast),
	})
	if name != "i1" || dir != Forward {
		t.Fatalf("got (%q, %v), want (i1, Forward)", name, dir)
	}
}

// order 中含 eq 列被丢弃。
func TestOrderEqColumnDropped(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "i1", Items: []ColumnItem{
		item("a", Asc, NullsFirst),
		item("b", Desc, NullsLast),
	}})
	// order 中 b 属于 eq，无论其方向如何都被丢弃，need 只剩 a。
	name, dir := choose(t, r, []string{"b"}, []ColumnItem{
		item("a", Asc, NullsFirst),
		item("b", Desc, NullsFirst),
	})
	if name != "i1" || dir != Forward {
		t.Fatalf("got (%q, %v), want (i1, Forward)", name, dir)
	}
}

// order 重复列只保留首次。
func TestOrderDuplicateColumnKeepsFirst(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "i1", Items: []ColumnItem{
		item("a", Asc, NullsFirst),
		item("b", Desc, NullsLast),
	}})
	// a 第二次出现为 DESC，被丢弃；need = (a ASC NF, b DESC NL)。
	name, dir := choose(t, r, nil, []ColumnItem{
		item("a", Asc, NullsFirst),
		item("b", Desc, NullsLast),
		item("a", Desc, NullsLast),
	})
	if name != "i1" || dir != Forward {
		t.Fatalf("got (%q, %v), want (i1, Forward)", name, dir)
	}
}

// need 为空时任何索引都以正向满足。
func TestEmptyNeed(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "i1", Items: []ColumnItem{
		item("a", Asc, NullsFirst),
	}})
	// order 全部列都属于 eq，need 为空。
	name, dir := choose(t, r, []string{"a"}, []ColumnItem{
		item("a", Desc, NullsLast),
	})
	if name != "i1" || dir != Forward {
		t.Fatalf("got (%q, %v), want (i1, Forward)", name, dir)
	}
	// order 本身为空，need 也为空。
	name, dir = choose(t, r, nil, nil)
	if name != "i1" || dir != Forward {
		t.Fatalf("got (%q, %v), want (i1, Forward)", name, dir)
	}
}

// 正向索引优先于更短的反向索引。
func TestForwardPreferredOverShorterBackward(t *testing.T) {
	r := NewRegistry()
	// 长索引正向满足。
	mustRegister(t, r, Index{Name: "long", Items: []ColumnItem{
		item("a", Asc, NullsFirst),
		item("x", Asc, NullsFirst),
		item("y", Asc, NullsFirst),
	}})
	// 短索引只能反向满足。
	mustRegister(t, r, Index{Name: "short", Items: []ColumnItem{
		item("a", Desc, NullsLast),
	}})
	name, dir := choose(t, r, nil, []ColumnItem{
		item("a", Asc, NullsFirst),
	})
	if name != "long" || dir != Forward {
		t.Fatalf("got (%q, %v), want (long, Forward)", name, dir)
	}
}

// 列项数并列时按名字字节序取小。
func TestTieBreakByName(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "zb", Items: []ColumnItem{
		item("a", Asc, NullsFirst),
	}})
	mustRegister(t, r, Index{Name: "aa", Items: []ColumnItem{
		item("a", Asc, NullsFirst),
	}})
	name, dir := choose(t, r, nil, []ColumnItem{
		item("a", Asc, NullsFirst),
	})
	if name != "aa" || dir != Forward {
		t.Fatalf("got (%q, %v), want (aa, Forward)", name, dir)
	}
}

// 同方向时列项数少者优先。
func TestShorterPreferredSameDirection(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "long", Items: []ColumnItem{
		item("a", Asc, NullsFirst),
		item("b", Asc, NullsFirst),
	}})
	mustRegister(t, r, Index{Name: "short", Items: []ColumnItem{
		item("a", Asc, NullsFirst),
	}})
	name, _ := choose(t, r, nil, []ColumnItem{
		item("a", Asc, NullsFirst),
	})
	if name != "short" {
		t.Fatalf("got %q, want short", name)
	}
}

// Register 按顺序只报第一个错误。
func TestRegisterErrorOrder(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "dup", Items: []ColumnItem{item("a", Asc, NullsFirst)}})

	cases := []struct {
		name string
		idx  Index
		want error
	}{
		{"空名字优先于一切", Index{Name: "", Items: nil}, ErrEmptyName},
		{"重名优先于空列项", Index{Name: "dup", Items: nil}, ErrDuplicateName},
		{"空列项优先于空列名", Index{Name: "x", Items: nil}, ErrEmptyItems},
		{"空列名优先于非法方向", Index{Name: "x", Items: []ColumnItem{
			item("", 0, 0),
		}}, ErrEmptyColumn},
		{"非法方向优先于非法空值", Index{Name: "x", Items: []ColumnItem{
			item("a", 0, 0),
		}}, ErrInvalidDirection},
		{"非法空值位置", Index{Name: "x", Items: []ColumnItem{
			item("a", Asc, 0),
		}}, ErrInvalidNulls},
		{"列名重复最后检查", Index{Name: "x", Items: []ColumnItem{
			item("a", Asc, NullsFirst),
			item("a", Desc, NullsLast),
		}}, ErrDuplicateColumn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := r.Register(tc.idx)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err=%v, want %v", err, tc.want)
			}
		})
	}
	// 被拒绝的登记不改变索引集合：合法登记仍可进行且 Choose 只见 dup。
	mustRegister(t, r, Index{Name: "ok", Items: []ColumnItem{item("a", Asc, NullsFirst)}})
	if err := r.Drop("x"); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("Drop(x) got %v, want ErrIndexNotFound", err)
	}
}

// Drop 后索引不再被选；Drop 不存在的名字报错。
func TestDrop(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, Index{Name: "i1", Items: []ColumnItem{item("a", Asc, NullsFirst)}})
	if err := r.Drop("i1"); err != nil {
		t.Fatalf("Drop 失败: %v", err)
	}
	if _, _, err := r.Choose(nil, []ColumnItem{item("a", Asc, NullsFirst)}); !errors.Is(err, ErrNoSatisfyingIndex) {
		t.Fatalf("got %v, want ErrNoSatisfyingIndex", err)
	}
	if err := r.Drop("i1"); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("got %v, want ErrIndexNotFound", err)
	}
}

// Choose 非法输入先于无索引满足，且各类原因可区分。
func TestChooseInvalidInputPrecedence(t *testing.T) {
	r := NewRegistry() // 空登记器：任何合法查询都会是无索引满足。

	if _, _, err := r.Choose([]string{""}, nil); !errors.Is(err, ErrEmptyEqColumn) {
		t.Fatalf("got %v, want ErrEmptyEqColumn", err)
	}
	if _, _, err := r.Choose(nil, []ColumnItem{item("", Asc, NullsFirst)}); !errors.Is(err, ErrEmptyOrderColumn) {
		t.Fatalf("got %v, want ErrEmptyOrderColumn", err)
	}
	if _, _, err := r.Choose(nil, []ColumnItem{item("a", 0, NullsFirst)}); !errors.Is(err, ErrInvalidOrderDirection) {
		t.Fatalf("got %v, want ErrInvalidOrderDirection", err)
	}
	if _, _, err := r.Choose(nil, []ColumnItem{item("a", Asc, 0)}); !errors.Is(err, ErrInvalidOrderNulls) {
		t.Fatalf("got %v, want ErrInvalidOrderNulls", err)
	}
	if _, _, err := r.Choose(nil, []ColumnItem{item("a", Asc, NullsFirst)}); !errors.Is(err, ErrNoSatisfyingIndex) {
		t.Fatalf("got %v, want ErrNoSatisfyingIndex", err)
	}
}

// 并发调用 Register/Drop/Choose 等价于某个串行顺序（配合 -race）。
func TestConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			name := string(rune('a'+w%3)) + string(rune('0'+w/3))
			idx := Index{Name: name, Items: []ColumnItem{item("a", Asc, NullsFirst)}}
			for i := 0; i < 50; i++ {
				_ = r.Register(idx)
				_, _, _ = r.Choose(nil, []ColumnItem{item("a", Asc, NullsFirst)})
				_ = r.Drop(name)
			}
		}(w)
	}
	wg.Wait()
}

// 相同操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	run := func() (string, ScanDirection, error) {
		r := NewRegistry()
		mustRegister(t, r, Index{Name: "b", Items: []ColumnItem{item("a", Asc, NullsFirst)}})
		mustRegister(t, r, Index{Name: "a", Items: []ColumnItem{item("a", Asc, NullsFirst)}})
		mustRegister(t, r, Index{Name: "c", Items: []ColumnItem{item("a", Desc, NullsLast)}})
		return r.Choose(nil, []ColumnItem{item("a", Asc, NullsFirst)})
	}
	n1, d1, e1 := run()
	for i := 0; i < 20; i++ {
		n2, d2, e2 := run()
		if n1 != n2 || d1 != d2 || !errors.Is(e2, e1) {
			t.Fatalf("重放结果不一致: (%q,%v,%v) vs (%q,%v,%v)", n1, d1, e1, n2, d2, e2)
		}
	}
	if n1 != "a" || d1 != Forward {
		t.Fatalf("got (%q, %v), want (a, Forward)", n1, d1)
	}
}
