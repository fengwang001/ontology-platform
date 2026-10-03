package plan

import (
	"bytes"
	"errors"
	"testing"

	"ontology/diff"
	"ontology/norm"
)

func i64(v int64) *int64 { return &v }

func specEngine(t *testing.T) *diff.Engine {
	t.Helper()
	e, err := diff.New(2, norm.RMHalfUp, false)
	if err != nil {
		t.Fatal(err)
	}
	put := func(r diff.Row) {
		t.Helper()
		if err := func() error {
			if r.ID <= 4 {
				return e.SrcPut(r)
			}
			return e.TgtPut(r)
		}(); err != nil {
			t.Fatal(err)
		}
	}
	_ = put
	must(t, e.SrcPut(diff.Row{ID: 1, D: i64(125000), C: []byte("ab")}))
	must(t, e.SrcPut(diff.Row{ID: 2, D: i64(-125000), C: []byte("")}))
	must(t, e.SrcPut(diff.Row{ID: 4, D: i64(5000), C: []byte("z")}))
	must(t, e.TgtPut(diff.Row{ID: 1, D: i64(13), C: []byte("ab  ")}))
	must(t, e.TgtPut(diff.Row{ID: 2, D: i64(-12), C: nil}))
	must(t, e.TgtPut(diff.Row{ID: 9, D: i64(5), C: []byte("x")}))
	return e
}

func TestBuildSpecExample(t *testing.T) {
	e := specEngine(t)
	p, err := Build(e, 1, 10, false)
	must(t, err)
	if len(p.Items) != 2 {
		t.Fatalf("want 2 items got %d", len(p.Items))
	}
	it0, it1 := p.Items[0], p.Items[1]
	// 项按 id 升序：先 id 2 Update(2, d=-13, c="")，仅 d、c 两列
	if it0.ID != 2 || it0.Kind != KindUpdate || !it0.Exist || it0.Ver != 1 ||
		it0.D == nil || *it0.D != -13 || string(it0.C) != "" ||
		len(it0.Cols) != 2 {
		t.Fatalf("item0 wrong: %+v d=%v c=%q cols=%v", it0, it0.D, it0.C, it0.Cols)
	}
	// 再 id 4 Insert(4, d=1, c="z")，记录“不存在”
	if it1.ID != 4 || it1.Kind != KindInsert || it1.Exist || it1.Ver != 0 ||
		it1.D == nil || *it1.D != 1 || string(it1.C) != "z" {
		t.Fatalf("item1 wrong: %+v d=%v c=%q", it1, it1.D, it1.C)
	}
}

func TestApplyConvergence(t *testing.T) {
	e := specEngine(t)
	p, _ := Build(e, 1, 10, false)
	ins, upd, delN, err := Apply(e, RoleRepairer, p)
	must(t, err)
	if ins != 1 || upd != 1 || delN != 0 {
		t.Fatalf("counts = (%d,%d,%d) want (1,1,0)", ins, upd, delN)
	}
	rs, _ := e.Compare(1, 10)
	for _, r := range rs {
		if r.Kind == diff.Missing || r.Kind == diff.Changed {
			t.Fatalf("after Apply still %s at %d", kind(r.Kind), r.ID)
		}
	}
	// del=false：Extra(id 9) 仍在
	var extra int
	for _, r := range rs {
		if r.Kind == diff.Extra {
			extra++
		}
	}
	if extra != 1 {
		t.Fatalf("want 1 remaining Extra got %d", extra)
	}

	// del=true 再收敛 Extra
	p2, _ := Build(e, 1, 10, true)
	if len(p2.Items) != 1 || p2.Items[0].Kind != KindDelete || p2.Items[0].ID != 9 {
		t.Fatalf("want single Delete(9), got %+v", p2.Items)
	}
	_, _, _, err = Apply(e, RoleRepairer, p2)
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("repairer must not apply Delete, got %v", err)
	}
	ins, upd, delN, err = Apply(e, RoleAdmin, p2)
	must(t, err)
	if ins != 0 || upd != 0 || delN != 1 {
		t.Fatalf("counts = (%d,%d,%d) want (0,0,1)", ins, upd, delN)
	}
	rs2, _ := e.Compare(1, 10)
	for _, r := range rs2 {
		if r.Kind != diff.Equal {
			t.Fatalf("full convergence broken: id %d %s", r.ID, kind(r.Kind))
		}
	}
	if len(rs2) != 3 { // 1,2,4 三行 Equal
		t.Fatalf("want 3 Equal rows got %d", len(rs2))
	}
}

func TestApplyStaleScenarios(t *testing.T) {
	// 1) 计划生成后 Update 目标行被他人改写
	e := specEngine(t)
	p, _ := Build(e, 1, 10, false)
	verBefore := targetVer(t, e, 4)
	must(t, e.TgtPut(diff.Row{ID: 2, D: i64(99), C: nil}))
	nIns, nUpd, nDel, err := Apply(e, RoleRepairer, p)
	se := &StaleError{}
	if !errors.As(err, &se) || se.ID != 2 {
		t.Fatalf("want ErrStale(2) got %v", err)
	}
	if !errors.Is(err, diff.ErrStale) {
		t.Fatalf("StaleError must satisfy errors.Is(diff.ErrStale)")
	}
	if nIns|nUpd|nDel != 0 {
		t.Fatalf("rejected apply must return zero counts")
	}
	if _, _, ver, exist := e.TgtSnapshot(4); exist || ver != verBefore {
		t.Fatalf("id4 must not be inserted: exist=%v ver=%d", exist, ver)
	}

	// 2) Update 目标行被删除
	e = specEngine(t)
	p, _ = Build(e, 1, 10, false)
	must(t, e.TgtDel(2))
	if _, _, _, err = Apply(e, RoleRepairer, p); !staleID(err, 2) {
		t.Fatalf("deleted row: want ErrStale(2) got %v", err)
	}

	// 3) Insert 被抢先插入
	e = specEngine(t)
	p, _ = Build(e, 1, 10, false)
	must(t, e.TgtPut(diff.Row{ID: 4, D: i64(7), C: []byte("q")}))
	if _, _, _, err = Apply(e, RoleRepairer, p); !staleID(err, 4) {
		t.Fatalf("preemptive insert: want ErrStale(4) got %v", err)
	}
	// id2 未被更新（零改动）
	if d, _, _, _ := e.TgtSnapshot(2); *d != -12 {
		t.Fatalf("id2 must remain -12 after rejected apply")
	}

	// 4) Delete 目标行被删：Extra 计划 stale
	e = specEngine(t)
	pd, _ := Build(e, 1, 10, true)
	must(t, e.TgtDel(9))
	if _, _, _, err = Apply(e, RoleAdmin, pd); !staleID(err, 9) {
		t.Fatalf("deleted extra: want ErrStale(9) got %v", err)
	}

	// 5) 多项失配时报 id 最小者
	e = specEngine(t)
	p, _ = Build(e, 1, 10, false)
	must(t, e.TgtPut(diff.Row{ID: 2, D: i64(99), C: nil}))
	must(t, e.TgtPut(diff.Row{ID: 4, D: i64(98), C: nil}))
	if _, _, _, err = Apply(e, RoleRepairer, p); !staleID(err, 2) {
		t.Fatalf("multiple stale: want smallest id 2 got %v", err)
	}
}

func TestApplyPermissionsAndValidation(t *testing.T) {
	e := specEngine(t)
	// 空计划成功且不改版本
	v2 := targetVer(t, e, 2)
	ins, upd, delN, err := Apply(e, RoleRepairer, &Plan{})
	must(t, err)
	if ins|upd|delN != 0 || targetVer(t, e, 2) != v2 {
		t.Fatalf("empty plan must succeed without version change")
	}
	// nil 计划
	if _, _, _, err = Apply(e, RoleRepairer, nil); !errors.Is(err, ErrParam) {
		t.Fatalf("nil plan -> ErrParam, got %v", err)
	}
	// 非法角色
	p, _ := Build(e, 1, 10, false)
	if _, _, _, err = Apply(e, 0, p); !errors.Is(err, ErrPermission) {
		t.Fatalf("role 0 -> ErrPermission, got %v", err)
	}
	if _, _, _, err = Apply(e, 3, p); !errors.Is(err, ErrPermission) {
		t.Fatalf("role 3 -> ErrPermission, got %v", err)
	}
	// Build 参数非法
	if _, err := Build(nil, 1, 10, false); !errors.Is(err, ErrParam) {
		t.Fatalf("nil engine -> ErrParam")
	}
	if _, err := Build(e, 0, 10, false); !errors.Is(err, ErrParam) {
		t.Fatalf("bad range -> ErrParam")
	}
}

func TestPlanIsImmutableSnapshot(t *testing.T) {
	e := specEngine(t)
	p, _ := Build(e, 1, 10, false)
	// 篡改计划内切片不影响引擎，且计划携带的是独立拷贝
	for i := range p.Items {
		for j := range p.Items[i].C {
			p.Items[i].C[j] = '?'
		}
	}
	rs, _ := e.Compare(1, 10)
	for _, r := range rs {
		if r.ID == 2 && bytes.Equal(r.TC, []byte("??")) {
			t.Fatalf("plan must not alias engine storage")
		}
	}
	// Update 只写不等列：构造 c 相等、d 不等的场景，应用后 c 保持原值
	e2, _ := diff.New(2, norm.RMHalfUp, false)
	must(t, e2.SrcPut(diff.Row{ID: 1, D: i64(130000), C: []byte("k")}))
	must(t, e2.TgtPut(diff.Row{ID: 1, D: i64(12), C: []byte("k")}))
	p2, _ := Build(e2, 1, 2, false)
	if len(p2.Items[0].Cols) != 1 || p2.Items[0].Cols[0] != diff.ColD {
		t.Fatalf("want only ColD changed")
	}
	_, _, _, err := Apply(e2, RoleRepairer, p2)
	must(t, err)
	if _, c, _, _ := e2.TgtSnapshot(1); string(c) != "k" {
		t.Fatalf("unchanged c column must be preserved, got %q", c)
	}
	if d, _, _, _ := e2.TgtSnapshot(1); *d != 13 {
		t.Fatalf("d must update to 13, got %d", *d)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func kind(k int) string {
	return [...]string{"Equal", "Changed", "Missing", "Extra"}[k]
}

func staleID(err error, id int64) bool {
	se := &StaleError{}
	return errors.As(err, &se) && se.ID == id
}

func targetVer(t *testing.T, e *diff.Engine, id int64) int64 {
	t.Helper()
	_, _, v, _ := e.TgtSnapshot(id)
	return v
}
