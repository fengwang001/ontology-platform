package alias_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/alias"
	"ontology/indexreg"
)

func mustCreate(t *testing.T, r *indexreg.Registry, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := r.CreateIndex(n); err != nil {
			t.Fatalf("CreateIndex(%q): %v", n, err)
		}
	}
}

func mustUpdate(t *testing.T, r *indexreg.Registry, actions ...alias.Action) {
	t.Helper()
	if err := alias.Update(r, actions); err != nil {
		t.Fatalf("Update(%v): %v", actions, err)
	}
}

func TestUpdateArgumentValidation(t *testing.T) {
	r := indexreg.New()
	mustCreate(t, r, "i1")
	base := r.Epoch()
	cases := []struct {
		name    string
		actions []alias.Action
	}{
		{"空批", nil},
		{"超界批", make([]alias.Action, 101)},
		{"别名非法", []alias.Action{alias.Add("Bad", "i1", indexreg.WriteUnspecified, nil)}},
		{"索引非法", []alias.Action{alias.Add("a", "-i", indexreg.WriteUnspecified, nil)}},
		{"RemoveIndex 名非法", []alias.Action{alias.RemoveIndex("_x")}},
		{"未知种类", []alias.Action{{Kind: alias.Kind(99), Alias: "a", Index: "i1"}}},
	}
	for _, c := range cases {
		err := alias.Update(r, c.actions)
		if !errors.Is(err, indexreg.ErrInvalidArgument) {
			t.Errorf("%s: got %v, want ErrInvalidArgument", c.name, err)
		}
	}
	if got := r.Epoch(); got != base {
		t.Fatalf("被拒批改了纪元: %d -> %d", base, got)
	}
}

func TestStepErrors(t *testing.T) {
	r := indexreg.New()
	mustCreate(t, r, "i1", "i2")
	base := r.Epoch()

	// Add 的 index 在工作副本中不是索引。
	err := alias.Update(r, []alias.Action{alias.Add("a", "ghost", indexreg.WriteUnspecified, nil)})
	var se *alias.StepError
	if !errors.As(err, &se) || se.Index != 0 || !errors.Is(err, indexreg.ErrIndexNotFound) {
		t.Fatalf("add ghost: got %v", err)
	}

	// 逐步错误取下标最小者；RemoveIndex 的目标不是索引同样报错。
	err = alias.Update(r, []alias.Action{
		alias.Remove("a", "i1", false), // 空操作
		alias.RemoveIndex("ghost"),
		alias.Add("b", "ghost", indexreg.WriteUnspecified, nil),
	})
	if !errors.As(err, &se) || se.Index != 1 || !errors.Is(err, indexreg.ErrIndexNotFound) {
		t.Fatalf("lowest step index: got %v", err)
	}

	// Remove mustExist 两态。
	err = alias.Update(r, []alias.Action{alias.Remove("a", "i1", true)})
	if !errors.As(err, &se) || se.Index != 0 || !errors.Is(err, alias.ErrMemberNotFound) {
		t.Fatalf("mustExist=true: got %v", err)
	}
	if err := alias.Update(r, []alias.Action{alias.Remove("a", "i1", false)}); err != nil {
		t.Fatalf("mustExist=false 应为空操作: %v", err)
	}
	if got := r.Epoch(); got != base {
		t.Fatalf("被拒/空操作批改了纪元: %d -> %d", base, got)
	}
}

func TestMemberOverwriteAndRemoveIndex(t *testing.T) {
	r := indexreg.New()
	mustCreate(t, r, "i1", "i2")
	f1, f2 := "x>1", "y<2"
	mustUpdate(t, r, alias.Add("a", "i1", indexreg.WriteUnspecified, &f1))
	// 成员已存在则覆盖 isWrite 与 filter。
	mustUpdate(t, r, alias.Add("a", "i1", indexreg.WriteTrue, &f2))
	mustUpdate(t, r, alias.Add("b", "i1", indexreg.WriteUnspecified, nil),
		alias.Add("b", "i2", indexreg.WriteUnspecified, nil))
	// RemoveIndex 删除索引并移除它在所有别名中的成员关系；成员全被移除的别名消失。
	mustUpdate(t, r, alias.RemoveIndex("i1"))
	// a 已消失，名字可再用为索引。
	if err := r.CreateIndex("a"); err != nil {
		t.Fatalf("别名消失后名字应可再用: %v", err)
	}
}

func TestTerminalErrorPrecedence(t *testing.T) {
	r := indexreg.New()
	mustCreate(t, r, "i1", "i2", "i3")
	base := r.Epoch()

	// 终态同时有名字冲突（别名 i1 与索引 i1 同名）与多写索引（别名 z 两个真），
	// 名字冲突优先，且报字节序最小的名字。
	err := alias.Update(r, []alias.Action{
		alias.Add("i1", "i2", indexreg.WriteUnspecified, nil),
		alias.Add("z", "i2", indexreg.WriteTrue, nil),
		alias.Add("z", "i3", indexreg.WriteTrue, nil),
	})
	var ce *indexreg.ConflictError
	if !errors.As(err, &ce) || ce.Name != "i1" || !errors.Is(err, indexreg.ErrNameConflict) {
		t.Fatalf("名字冲突应优先: got %v", err)
	}

	// 仅多写索引：报字节序最小的别名。
	err = alias.Update(r, []alias.Action{
		alias.Add("z", "i2", indexreg.WriteTrue, nil),
		alias.Add("z", "i3", indexreg.WriteTrue, nil),
		alias.Add("a", "i2", indexreg.WriteTrue, nil),
		alias.Add("a", "i3", indexreg.WriteTrue, nil),
	})
	var mw *alias.MultiWriteError
	if !errors.As(err, &mw) || mw.Alias != "a" || !errors.Is(err, alias.ErrMultipleWriteIndices) {
		t.Fatalf("多写索引应报最小别名 a: got %v", err)
	}
	if got := r.Epoch(); got != base {
		t.Fatalf("终态错误被拒不应改纪元: %d -> %d", base, got)
	}
}

func TestRenameIndexToAliasBothOrders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []alias.Action
	}{
		{"先删后建", []alias.Action{
			alias.RemoveIndex("i1"),
			alias.Add("i1", "i2", indexreg.WriteUnspecified, nil),
		}},
		{"先建后删（中间态同名不算错）", []alias.Action{
			alias.Add("i1", "i2", indexreg.WriteUnspecified, nil),
			alias.RemoveIndex("i1"),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := indexreg.New()
			mustCreate(t, r, "i1", "i2")
			mustUpdate(t, r, tc.actions...)
			// 终态 i1 是指向 i2 的别名；i1 不再是索引。
			if err := r.CloseIndex("i1"); !errors.Is(err, indexreg.ErrIndexNotFound) {
				t.Fatalf("i1 不应再是索引: %v", err)
			}
		})
	}

	// i1 仍是索引时单独 Add(i1,...) 终态同名，报名字冲突。
	r := indexreg.New()
	mustCreate(t, r, "i1", "i2")
	err := alias.Update(r, []alias.Action{alias.Add("i1", "i2", indexreg.WriteUnspecified, nil)})
	var ce *indexreg.ConflictError
	if !errors.As(err, &ce) || ce.Name != "i1" {
		t.Fatalf("单独 Add 应报终态名字冲突: %v", err)
	}
}

func TestNoEffectBatchAcceptedWithoutEpoch(t *testing.T) {
	r := indexreg.New()
	mustCreate(t, r, "i1")
	mustUpdate(t, r, alias.Add("a", "i1", indexreg.WriteUnspecified, nil))
	base := r.Epoch()
	// 空效果批：覆盖成完全相同的成员 + 空操作 Remove。
	f := "k=1"
	mustUpdate(t, r, alias.Add("a", "i1", indexreg.WriteUnspecified, nil))
	mustUpdate(t, r,
		alias.Add("tmp", "i1", indexreg.WriteUnspecified, &f),
		alias.Remove("tmp", "i1", true),
		alias.Remove("a", "ghost", false),
	)
	if got := r.Epoch(); got != base {
		t.Fatalf("终态相同的批不应加纪元: %d -> %d", base, got)
	}
}

func ExampleUpdate() {
	r := indexreg.New()
	_ = r.CreateIndex("i1")
	_ = r.CreateIndex("i2")
	_ = alias.Update(r, []alias.Action{alias.Add("a", "i1", indexreg.WriteUnspecified, nil)})
	fmt.Println(r.Epoch())
	// Output: 3
}
