package plan_test

import (
	"errors"
	"testing"

	"ontology/diff"
	"ontology/norm"
	"ontology/plan"
)

func pi64(v int64) *int64 { return &v }

func buildExample(t *testing.T, sd, rm int, ne bool) (*plan.Planner, *diff.T) {
	t.Helper()
	cfg, err := norm.NewCfg(sd, rm, ne)
	if err != nil {
		t.Fatal(err)
	}
	d := diff.New(cfg)
	must(t, d.SrcPut(norm.Row{ID: 1, D: pi64(125000), C: []byte("ab")}))
	must(t, d.SrcPut(norm.Row{ID: 2, D: pi64(-125000), C: []byte("")}))
	must(t, d.SrcPut(norm.Row{ID: 4, D: pi64(5000), C: []byte("z")}))
	must(t, d.TgtPut(norm.Row{ID: 1, D: pi64(13), C: []byte("ab  ")}))
	must(t, d.TgtPut(norm.Row{ID: 2, D: pi64(-12), C: nil}))
	must(t, d.TgtPut(norm.Row{ID: 9, D: pi64(5), C: []byte("x")}))
	return plan.New(d, cfg), d
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestMakeAndConvergence(t *testing.T) {
	p, d := buildExample(t, 2, norm.RmHalfAway, false)
	pl, err := p.Make(1, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if pl.Len() != 2 {
		t.Fatalf("plan len = %d, want 2", pl.Len())
	}
	ops := pl.Ops()
	// 按 id 升序：Update(2, d=-13, c="")、Insert(4, d=1, c="z")
	if ops[0].Kind != plan.OpUpdate || ops[0].ID != 2 || *ops[0].D != -13 ||
		len(ops[0].C) != 0 || ops[0].C == nil || ops[0].Mask != diff.ColD|diff.ColC ||
		ops[0].Version != 1 {
		t.Errorf("op0 = %+v", ops[0])
	}
	if ops[1].Kind != plan.OpInsert || ops[1].ID != 4 || *ops[1].D != 1 ||
		string(ops[1].C) != "z" || ops[1].Version != 0 {
		t.Errorf("op1 = %+v", ops[1])
	}
	if pl.HasDelete() {
		t.Errorf("del=false plan must not contain Delete")
	}

	ni, nu, nd, err := p.Apply(plan.RoleFixer, pl)
	if err != nil || ni != 1 || nu != 1 || nd != 0 {
		t.Fatalf("apply = (%d,%d,%d) err=%v", ni, nu, nd, err)
	}
	rows, err := d.Compare(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Class == diff.ClsMissing || r.Class == diff.ClsChanged {
			t.Errorf("not converged: %+v", r)
		}
	}
	// 只剩 id9 的 Extra，其余全部 Equal
	var extra []int64
	for _, r := range rows {
		if r.Class == diff.ClsExtra {
			extra = append(extra, r.ID)
		} else if r.Class != diff.ClsEqual {
			t.Errorf("unexpected non-equal row: %+v", r)
		}
	}
	if len(extra) != 1 || extra[0] != 9 {
		t.Errorf("extra = %v, want [9]; rows=%v", extra, rows)
	}

	// del=true 再生成：删除 9
	pl2, _ := p.Make(1, 10, true)
	if !pl2.HasDelete() || pl2.Len() != 1 || pl2.Ops()[0].ID != 9 {
		t.Errorf("delete plan = %+v", pl2.Ops())
	}
	if _, _, _, err := p.Apply(plan.RoleFixer, pl2); !errors.Is(err, plan.ErrPermission) {
		t.Errorf("fixer delete err=%v, want ErrPermission", err)
	}
	ni, nu, nd, err = p.Apply(plan.RoleAdmin, pl2)
	if err != nil || nd != 1 {
		t.Fatalf("admin delete = (%d,%d,%d) err=%v", ni, nu, nd, err)
	}
	rows, _ = d.Compare(1, 10)
	for _, r := range rows {
		if r.Class != diff.ClsEqual {
			t.Errorf("after delete non-equal row: %+v", r)
		}
	}
}

func TestApplyStaleScenarios(t *testing.T) {
	t.Run("updated_after_plan", func(t *testing.T) {
		p, d := buildExample(t, 2, norm.RmHalfAway, false)
		pl, _ := p.Make(1, 10, false)
		must(t, d.TgtPut(norm.Row{ID: 2, D: pi64(-12), C: nil})) // 版本 1->2
		_, _, _, err := p.Apply(plan.RoleFixer, pl)
		se, ok := err.(*plan.ErrStale)
		if !ok || se.ID != 2 {
			t.Fatalf("err=%v, want ErrStale(2)", err)
		}
		// id4 也不得被插入
		if _, ex := d.Version(4); ex {
			t.Errorf("partial apply: id4 inserted")
		}
	})
	t.Run("deleted_after_plan", func(t *testing.T) {
		p, d := buildExample(t, 2, norm.RmHalfAway, false)
		pl, _ := p.Make(1, 10, false)
		must(t, d.TgtDel(2))
		_, _, _, err := p.Apply(plan.RoleFixer, pl)
		if se, ok := err.(*plan.ErrStale); !ok || se.ID != 2 {
			t.Fatalf("err=%v, want ErrStale(2)", err)
		}
		if _, ex := d.Version(4); ex {
			t.Errorf("partial apply after delete")
		}
	})
	t.Run("insert_race", func(t *testing.T) {
		p, d := buildExample(t, 2, norm.RmHalfAway, false)
		pl, _ := p.Make(1, 10, false)
		must(t, d.TgtPut(norm.Row{ID: 4, D: pi64(0), C: []byte("q")})) // 抢先插入
		_, _, _, err := p.Apply(plan.RoleFixer, pl)
		if se, ok := err.(*plan.ErrStale); !ok || se.ID != 4 {
			t.Fatalf("err=%v, want ErrStale(4)", err)
		}
		if v, _ := d.Version(2); v != 1 {
			t.Errorf("id2 version = %d, unchanged expected", v)
		}
	})
	t.Run("smallest_stale_id", func(t *testing.T) {
		p, d := buildExample(t, 2, norm.RmHalfAway, false)
		pl, _ := p.Make(1, 10, true) // 含 Delete(9)
		must(t, d.TgtPut(norm.Row{ID: 4, D: pi64(1), C: []byte("z")}))
		must(t, d.TgtDel(9))
		_, _, _, err := p.Apply(plan.RoleAdmin, pl)
		if se, ok := err.(*plan.ErrStale); !ok || se.ID != 2 && se.ID != 4 {
			// 2 仍匹配；最小失配应为 4
			t.Fatalf("err=%v", err)
		} else if se.ID != 4 {
			t.Fatalf("stale id = %d, want 4", se.ID)
		}
	})
}

func TestApplyPermissionsAndEmpty(t *testing.T) {
	p, d := buildExample(t, 2, norm.RmHalfAway, false)
	pl, _ := p.Make(1, 10, false)
	if _, _, _, err := p.Apply(0, pl); !errors.Is(err, plan.ErrPermission) {
		t.Errorf("role=0 err=%v", err)
	}
	if _, _, _, err := p.Apply(3, pl); !errors.Is(err, plan.ErrPermission) {
		t.Errorf("role=3 err=%v", err)
	}
	if _, _, _, err := p.Apply(plan.RoleFixer, nil); !errors.Is(err, plan.ErrInvalid) {
		t.Errorf("nil plan err=%v", err)
	}
	// 空计划：任何合法角色成功且不改版本
	empty, _ := p.Make(1, 2, false) // 范围内 id1 已 Equal
	if empty.Len() != 0 {
		t.Fatalf("want empty plan, got %d", empty.Len())
	}
	before, _ := d.Version(1)
	ni, nu, nd, err := p.Apply(plan.RoleAdmin, empty)
	if err != nil || ni+nu+nd != 0 {
		t.Fatalf("empty apply = (%d,%d,%d) err=%v", ni, nu, nd, err)
	}
	after, _ := d.Version(1)
	if before != after {
		t.Errorf("empty plan changed version %d->%d", before, after)
	}
}

func TestPlanImmutability(t *testing.T) {
	p, _ := buildExample(t, 2, norm.RmHalfAway, false)
	pl, _ := p.Make(1, 10, false)
	ops := pl.Ops()
	ops[1].C[0] = 'X'
	*ops[0].D = 999
	again := pl.Ops()
	if string(again[1].C) != "z" || *again[0].D != -13 {
		t.Errorf("plan mutated: %+v", again)
	}
}

func TestReplanAfterNoChanges(t *testing.T) {
	// 归一后完全相同的两侧不产生任何计划项
	cfg, _ := norm.NewCfg(6, norm.RmHalfAway, false)
	d2 := diff.New(cfg)
	p2 := plan.New(d2, cfg)
	must(t, d2.SrcPut(norm.Row{ID: 1, D: pi64(7), C: []byte("a ")}))
	must(t, d2.TgtPut(norm.Row{ID: 1, D: pi64(7), C: []byte("a   ")}))
	pl, _ := p2.Make(1, 10, true)
	if pl.Len() != 0 {
		t.Errorf("identical normalized rows produce plan len %d", pl.Len())
	}
	rows, _ := d2.Compare(1, 10)
	if len(rows) != 1 || rows[0].Class != diff.ClsEqual {
		t.Errorf("rows = %v", rows)
	}
}
