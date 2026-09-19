package ontology

import (
	"strconv"
	"sync"
	"testing"
)

func TestCommitRecordAndRangeQuery(t *testing.T) {
	st := NewStore()
	e := NewEngine(st)
	e.Register(NewActionType("build", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
		{Name: "kind", Type: TypeString, Required: false, Default: "widget"},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		id := args["id"].(string)
		if err := tx.CreateObject(id, testType, map[string]any{"kind": args["kind"]}); err != nil {
			return err
		}
		if err := tx.SetAttribute(id, "v", 1); err != nil {
			return err
		}
		return tx.AddRelation(Relation{Type: "self", FromID: id, ToID: id})
	}))

	for i := 0; i < 5; i++ {
		if _, err := e.Execute("build", map[string]any{"id": "o" + string(rune('a'+i))}); err != nil {
			t.Fatal(err)
		}
	}

	all := e.Log().Range(1, 0)
	if len(all) != 5 {
		t.Fatalf("应有 5 条记录，得到 %d", len(all))
	}
	for i, r := range all {
		if r.Seq != int64(i+1) {
			t.Fatalf("序号不连续: 位置 %d seq %d", i, r.Seq)
		}
		if r.Action != "build" || r.Params["kind"] != "widget" {
			t.Fatalf("记录内容错误: %+v", r)
		}
		kinds := map[ImpactKind]int{}
		for _, im := range r.Impacts {
			kinds[im.Kind]++
		}
		if kinds[ImpactObjectCreated] != 1 || kinds[ImpactObjectChanged] != 1 ||
			kinds[ImpactRelationAdded] != 1 {
			t.Fatalf("影响清单不完整: %v", kinds)
		}
	}

	mid := e.Log().Range(2, 4)
	if len(mid) != 3 || mid[0].Seq != 2 || mid[2].Seq != 4 {
		t.Fatalf("区间查询结果错误: %v", mid)
	}
	if e.Log().Range(3, 2) != nil {
		t.Fatal("逆序区间应返回空")
	}

	// 记录不可变：修改入参侧数据不影响已存记录。
	all[0].Params["kind"] = "mutated"
	if e.Log().Range(1, 1)[0].Params["kind"] != "widget" {
		t.Fatal("执行记录应对外不可变")
	}
}

func TestConcurrentCommitsStrictSeq(t *testing.T) {
	st := NewStore()
	e := NewEngine(st)
	e.Register(NewActionType("c", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		return tx.CreateObject(args["id"].(string), testType, nil)
	}))

	const n = 200
	var wg sync.WaitGroup
	seqs := make([]int64, n)
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			r, err := e.Execute("c", map[string]any{"id": "g" + strconv.Itoa(i)})
			if err != nil {
				errs <- err
				return
			}
			seqs[i] = r.Seq
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	seen := map[int64]bool{}
	for i, s := range seqs {
		if s < 1 || s > n {
			t.Fatalf("goroutine %d 序号越界: %d", i, s)
		}
		if seen[s] {
			t.Fatalf("序号重号: %d", s)
		}
		seen[s] = true
	}
	if len(seen) != n || e.Log().LastSeq() != n {
		t.Fatalf("序号未严格覆盖 1..%d，实际 %d 条", n, e.Log().LastSeq())
	}
	all := e.Log().Range(1, 0)
	for i, r := range all {
		if r.Seq != int64(i+1) {
			t.Fatal("日志顺序不是严格递增")
		}
	}
}
