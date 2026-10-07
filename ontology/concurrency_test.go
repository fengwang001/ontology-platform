package ontology

import (
	"errors"
	"sort"
	"sync"
	"testing"
)

// naiveModel 是一个独立的朴素串行参照实现：
// 无锁、无缓存，每次查询都基于当前字段定义从头重算。
type naiveModel struct {
	decl   BindingDecl
	fields map[FieldRef]FieldDef
}

func newNaiveModel(decl BindingDecl, fields map[FieldRef]FieldDef) *naiveModel {
	return &naiveModel{decl: decl, fields: fields}
}

func (n *naiveModel) updateField(ref FieldRef, def FieldDef) {
	n.fields[ref] = def
}

// query 复现 Registry.QueryBinding 的判定顺序，返回 (ok, kind)。
func (n *naiveModel) query(dir Direction) (bool, IncompatKind) {
	if !n.decl.Direction.Allows(dir) {
		return false, -1
	}
	left, lok := n.fields[n.decl.Left]
	right, rok := n.fields[n.decl.Right]
	var lp, rp *FieldDef
	if lok {
		lp = &left
	}
	if rok {
		rp = &right
	}
	res := CheckBinding(n.decl, lp, rp)
	if res.Kind == IncompatFieldDeleted {
		return false, IncompatFieldDeleted
	}
	if !res.Compatible(n.decl.Direction) {
		return false, res.Kind
	}
	return true, Compatible
}

type recordedOp struct {
	seq     uint64
	isQuery bool
	// update
	ref FieldRef
	def FieldDef
	// query result
	dir  Direction
	ok   bool
	kind IncompatKind
}

// 并发字段变更与绑定查询交织：每个查询结论都必须等价于
// 按全局序号串行重放后朴素实现给出的结论。
func TestConcurrentUpdatesAndQueriesMatchNaiveSerial(t *testing.T) {
	r := NewRegistry()
	enumAB := []Value{StrValue("a"), StrValue("b")}
	if err := r.RegisterObjectType("A", []FieldDef{{ID: "fa", Type: TString, Enum: enumAB}}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterObjectType("B", []FieldDef{{ID: "fb", Type: TString, Enum: enumAB}}); err != nil {
		t.Fatal(err)
	}
	decl := twoWayDecl("lt", FieldRef{"A", "fa"}, FieldRef{"B", "fb"})
	if err := r.DeclareLinkType(decl); err != nil {
		t.Fatal(err)
	}

	variants := map[FieldRef][]FieldDef{
		{"A", "fa"}: {
			{ID: "fa", Type: TString, Enum: enumAB},
			{ID: "fa", Type: TString, Enum: []Value{StrValue("a")}},
			{ID: "fa", Type: TString, Enum: []Value{StrValue("a"), StrValue("b"), StrValue("c")}},
		},
		{"B", "fb"}: {
			{ID: "fb", Type: TString, Enum: enumAB},
			{ID: "fb", Type: TString, Enum: []Value{StrValue("b")}},
			{ID: "fb", Type: TString, Enum: []Value{StrValue("a"), StrValue("b"), StrValue("c"), StrValue("d")}},
		},
	}
	refs := []FieldRef{{"A", "fa"}, {"B", "fb"}}

	var mu sync.Mutex
	var ops []recordedOp
	record := func(op recordedOp) {
		mu.Lock()
		ops = append(ops, op)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				ref := refs[(worker+i)%len(refs)]
				def := variants[ref][(worker+i)%len(variants[ref])]
				seq, err := r.UpdateField(ref.ObjectType, def)
				if err != nil {
					t.Errorf("update failed: %v", err)
					return
				}
				record(recordedOp{seq: seq, ref: ref, def: def})
			}
		}(w)
	}
	dirs := []Direction{LeftToRight, RightToLeft}
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				dir := dirs[(worker+i)%2]
				res, err := r.QueryBinding("lt", dir)
				op := recordedOp{isQuery: true, dir: dir}
				switch {
				case err == nil:
					op.seq, op.ok, op.kind = res.Seq, true, Compatible
				case errors.Is(err, ErrBindingInvalid):
					op.seq, op.kind = res.Seq, IncompatFieldDeleted
				default:
					var incErr *IncompatibleError
					if !errors.As(err, &incErr) {
						t.Errorf("unexpected query error: %v", err)
						return
					}
					op.seq, op.kind = res.Seq, incErr.Kind
				}
				record(op)
			}
		}(w)
	}
	wg.Wait()

	// 按全局序号串行重放到朴素实现上，逐条核对查询结论。
	sort.Slice(ops, func(i, j int) bool { return ops[i].seq < ops[j].seq })
	naive := newNaiveModel(decl, map[FieldRef]FieldDef{
		{"A", "fa"}: {ID: "fa", Type: TString, Enum: enumAB},
		{"B", "fb"}: {ID: "fb", Type: TString, Enum: enumAB},
	})
	queries := 0
	for _, op := range ops {
		if !op.isQuery {
			naive.updateField(op.ref, op.def)
			continue
		}
		queries++
		wantOK, wantKind := naive.query(op.dir)
		if op.ok != wantOK || op.kind != wantKind {
			t.Fatalf("seq %d: concurrent result (ok=%v kind=%v) != naive serial (ok=%v kind=%v)",
				op.seq, op.ok, op.kind, wantOK, wantKind)
		}
	}
	if queries == 0 {
		t.Fatal("no queries recorded")
	}

	// 最终绑定兼容性结论也必须一致。
	final, ok := r.Compatibility("lt")
	if !ok {
		t.Fatal("link type missing")
	}
	_, wantKind := naive.query(LeftToRight)
	if final.Kind != wantKind && !(wantKind == Compatible && final.Kind == Compatible) {
		t.Fatalf("final compatibility %v != naive serial %v", final.Kind, wantKind)
	}
}
