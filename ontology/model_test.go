package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naiveState 是独立实现的朴素批量导入模型：权限用线性扫描、
// 状态用普通嵌套 map，与守门器实现不共享任何代码路径。
type naiveState struct {
	types    map[string][]string // typeName -> requiredProps
	objects  map[string]map[string]map[string]string
	subjects map[string]bool
	perms    []PermissionEntry
}

func (n *naiveState) writableAt(snapshot []PermissionEntry, subject, typ, prop string) bool {
	granted := false
	for _, e := range snapshot {
		if e.Subject == subject && e.ObjectType == typ && e.Property == prop {
			granted = e.Writable
		}
	}
	return granted
}

func naiveImport(n *naiveState, req BatchRequest) BatchResult {
	if req.Mode != ModeAtomic && req.Mode != ModeLenient {
		return BatchResult{Rejected: true, RejectReason: "bad mode"}
	}
	if !n.subjects[req.Subject] {
		return BatchResult{Rejected: true, RejectReason: "no subject"}
	}
	snapshot := append([]PermissionEntry(nil), n.perms...)

	res := BatchResult{}
	for idx, rec := range req.Records {
		required, typeOK := n.types[rec.ObjectType]
		if !typeOK {
			res.Results = append(res.Results, RecordResult{Index: idx, Status: StatusFailed, FailCategory: FailTypeNotFound})
			continue
		}
		bucket := n.objects[rec.ObjectType]
		_, exists := bucket[rec.ObjectID]
		if (rec.Semantic == SemanticCreate && exists) ||
			(rec.Semantic == SemanticUpdate && !exists) ||
			(rec.Semantic != SemanticCreate && rec.Semantic != SemanticUpdate) {
			res.Results = append(res.Results, RecordResult{Index: idx, Status: StatusFailed, FailCategory: FailSemanticMismatch})
			continue
		}

		var allowed, denied []string
		for field := range rec.Fields {
			if n.writableAt(snapshot, req.Subject, rec.ObjectType, field) {
				allowed = append(allowed, field)
			} else {
				denied = append(denied, field)
			}
		}
		sort.Strings(denied)

		if req.Mode == ModeAtomic && len(denied) > 0 {
			res.Results = append(res.Results, RecordResult{Index: idx, Status: StatusFailed, FailCategory: FailPermissionDenied})
			continue
		}

		// 合并生效属性：更新语义以旧值为底，创建语义从空开始。
		effective := map[string]string{}
		if rec.Semantic == SemanticUpdate {
			for k, v := range bucket[rec.ObjectID] {
				effective[k] = v
			}
		}
		for _, f := range allowed {
			effective[f] = rec.Fields[f]
		}
		missing := false
		for _, prop := range required {
			if _, ok := effective[prop]; !ok {
				missing = true
			}
		}
		if missing {
			res.Results = append(res.Results, RecordResult{Index: idx, Status: StatusFailed, FailCategory: FailRequiredConstraint})
			continue
		}

		if bucket == nil {
			bucket = map[string]map[string]string{}
			n.objects[rec.ObjectType] = bucket
		}
		bucket[rec.ObjectID] = effective
		if len(denied) > 0 {
			res.Results = append(res.Results, RecordResult{Index: idx, Status: StatusPartial, Skipped: denied})
		} else {
			res.Results = append(res.Results, RecordResult{Index: idx, Status: StatusSuccess})
		}
	}
	return res
}

// randomScenario 生成一份随机场景：类型、主体、权限、初始对象与批量请求。
func randomScenario(r *rand.Rand) (*Store, *naiveState, BatchRequest) {
	propPool := []string{"p0", "p1", "p2", "p3", "p4", "p5"}
	typeNames := []string{"T0", "T1", "T2"}
	subjects := []string{"s0", "s1", "s2"}

	store := NewStore()
	naive := &naiveState{
		types:    map[string][]string{},
		objects:  map[string]map[string]map[string]string{},
		subjects: map[string]bool{},
	}

	for _, name := range typeNames {
		var required []string
		for _, p := range propPool {
			if r.Intn(3) == 0 {
				required = append(required, p)
			}
		}
		store.AddObjectType(ObjectType{Name: name, RequiredProps: required})
		naive.types[name] = required
		naive.objects[name] = map[string]map[string]string{}
	}
	for _, s := range subjects {
		store.AddSubject(s)
		naive.subjects[s] = true
	}

	// 随机权限条目（含重复键，后写覆盖先写）。
	for i := 0; i < r.Intn(60); i++ {
		e := PermissionEntry{
			Subject:    subjects[r.Intn(len(subjects))],
			ObjectType: typeNames[r.Intn(len(typeNames))],
			Property:   propPool[r.Intn(len(propPool))],
			Writable:   r.Intn(2) == 0,
		}
		store.SetPermission(e)
		naive.perms = append(naive.perms, e)
	}

	// 随机初始对象。
	for i := 0; i < r.Intn(8); i++ {
		typ := typeNames[r.Intn(len(typeNames))]
		id := fmt.Sprintf("obj-%d", r.Intn(6))
		props := map[string]string{}
		for _, p := range propPool {
			if r.Intn(2) == 0 {
				props[p] = fmt.Sprintf("v%d", r.Intn(100))
			}
		}
		store.PutObject(Object{TypeName: typ, ID: id, Props: props})
		naive.objects[typ][id] = props
	}

	// 随机批量请求。
	mode := ModeAtomic
	if r.Intn(2) == 0 {
		mode = ModeLenient
	}
	req := BatchRequest{Mode: mode, Subject: subjects[r.Intn(len(subjects))]}
	for i := 0; i < 1+r.Intn(20); i++ {
		semantic := SemanticCreate
		if r.Intn(2) == 0 {
			semantic = SemanticUpdate
		}
		fields := map[string]string{}
		for _, p := range propPool {
			if r.Intn(2) == 0 {
				fields[p] = fmt.Sprintf("w%d", r.Intn(100))
			}
		}
		req.Records = append(req.Records, Record{
			ObjectType: typeNames[r.Intn(len(typeNames))],
			ObjectID:   fmt.Sprintf("obj-%d", r.Intn(8)),
			Semantic:   semantic,
			Fields:     fields,
		})
	}
	return store, naive, req
}

// 与朴素模型对照：随机生成的记录与权限组合下，
// 逐条结果与最终状态必须完全一致。
func TestAgainstNaiveModel(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		store, naive, req := randomScenario(r)

		got := NewBatchImporter(store, nil).Execute(req)
		want := naiveImport(naive, req)

		if got.Rejected != want.Rejected {
			t.Fatalf("seed=%d: Rejected %v != %v", seed, got.Rejected, want.Rejected)
		}
		if len(got.Results) != len(want.Results) {
			t.Fatalf("seed=%d: 结果条数 %d != %d", seed, len(got.Results), len(want.Results))
		}
		for i := range got.Results {
			g, w := got.Results[i], want.Results[i]
			if g.Status != w.Status || g.FailCategory != w.FailCategory ||
				!reflect.DeepEqual(g.Skipped, w.Skipped) {
				t.Fatalf("seed=%d 记录 %d: got %+v want %+v", seed, i, g, w)
			}
		}
		if !reflect.DeepEqual(store.SnapshotObjects(), naive.objects) {
			t.Fatalf("seed=%d: 最终状态不一致\ngot  %v\nwant %v", seed, store.SnapshotObjects(), naive.objects)
		}
	}
}
