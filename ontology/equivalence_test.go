package ontology_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/ontology"
	"ontology/ontology/naive"
)

// registerTypes 在两个实现上注册同一组类型与钩子。
func registerTypes(s *ontology.Store, m *naive.Model) {
	docType := ontology.ObjectType{
		Name: "Doc",
		Hooks: []ontology.ValidationHook{{
			Name: "title-needs-status",
			DeclareReads: func(writeSet map[string]struct{}) ([]string, []ontology.RemoteRead) {
				if _, ok := writeSet["title"]; ok {
					return []string{"status"}, nil
				}
				return nil, nil
			},
		}},
	}
	empType := ontology.ObjectType{
		Name: "Employee",
		Hooks: []ontology.ValidationHook{{
			Name:        "needs-company-name",
			RemoteReads: []ontology.RemoteRead{{Link: "worksAt", TargetType: "Company", Prop: "name"}},
		}},
	}
	compType := ontology.ObjectType{Name: "Company"}
	for _, ot := range []ontology.ObjectType{docType, empType, compType} {
		s.RegisterObjectType(ot)
		m.RegisterObjectType(ot)
	}
}

func kindOf(err error) string {
	if err == nil {
		return "ok"
	}
	if we, ok := err.(*ontology.WriteError); ok {
		return we.Kind.String()
	}
	return "other:" + err.Error()
}

// 随机串行操作流：同一操作序列逐条施加到两个实现，
// 每一步的裁定类别必须完全一致。
func TestSerialRandomizedMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	s := ontology.NewStore()
	m := naive.New()
	registerTypes(s, m)

	type objRef struct{ typeName, id string }
	var objs []objRef
	for i := 0; i < 4; i++ {
		for _, typ := range []string{"Doc", "Employee", "Company"} {
			id := fmt.Sprintf("%s-%d", typ, i)
			init := map[string]string{"seed": fmt.Sprint(i)}
			if _, err := s.Create(typ, id, init); err != nil {
				t.Fatalf("store create: %v", err)
			}
			if _, err := m.Create(typ, id, init); err != nil {
				t.Fatalf("naive create: %v", err)
			}
			objs = append(objs, objRef{typ, id})
		}
	}

	props := []string{"title", "status", "hits", "name", "level"}
	for step := 0; step < 2000; step++ {
		o := objs[rng.Intn(len(objs))]
		var baseline uint64
		cur, _ := s.Version(o.id)
		switch rng.Intn(4) {
		case 0:
			baseline = cur // 最新基线
		case 1:
			if cur > 0 {
				baseline = cur - 1 // 直接父版本，触发合并判定
			}
		case 2:
			baseline = uint64(rng.Intn(int(cur) + 1)) // 可能落后
		default:
			baseline = cur
		}
		set := map[string]string{
			props[rng.Intn(len(props))]: fmt.Sprintf("s%d", step),
		}
		if rng.Intn(3) == 0 {
			set[props[rng.Intn(len(props))]] = "x"
		}
		req := ontology.WriteRequest{ObjectID: o.id, Baseline: baseline, Set: set}

		if rng.Intn(50) == 0 {
			s.Delete(o.id)
			m.Delete(o.id)
		}

		_, sErr := s.Write(req)
		_, mErr := m.Write(req)
		if kindOf(sErr) != kindOf(mErr) {
			t.Fatalf("step %d: store=%v naive=%v (req=%+v)", step, sErr, mErr, req)
		}
	}

	// 最终状态逐对象一致。
	for _, o := range objs {
		v, _ := s.Version(o.id)
		if mv := m.Version(o.id); mv != v {
			t.Fatalf("%s: store version %d != naive version %d", o.id, v, mv)
		}
		_, sp, err := s.ReadAt(o.id, v)
		if err != nil {
			t.Fatalf("read %s@%d: %v", o.id, v, err)
		}
		mp := m.Props(o.id, v)
		if fmt.Sprint(sp) != fmt.Sprint(mp) {
			t.Fatalf("%s: store props %v != naive props %v", o.id, sp, mp)
		}
	}
}

// 随机并发写入：真实实现对同一实例的并发写入被串行化后，
// 按判定日志给出的串行顺序重放到朴素模型，最终版本序列必须完全一致。
func TestConcurrentWritesEquivalentToNaiveSerial(t *testing.T) {
	s := ontology.NewStore()
	m := naive.New()
	registerTypes(s, m)

	const objects = 6
	var ids []string
	for i := 0; i < objects; i++ {
		typ := []string{"Doc", "Employee", "Company"}[i%3]
		id := fmt.Sprintf("%s-%d", typ, i)
		init := map[string]string{"seed": fmt.Sprint(i)}
		if _, err := s.Create(typ, id, init); err != nil {
			t.Fatalf("store create: %v", err)
		}
		if _, err := m.Create(typ, id, init); err != nil {
			t.Fatalf("naive create: %v", err)
		}
		ids = append(ids, id)
	}

	// 并发阶段：每个 goroutine 反复读取当前版本并以之为基线写入，
	// 部分写入刻意使用旧基线以制造合并与冲突。
	const goroutines = 8
	const opsPerG = 60
	props := []string{"title", "status", "hits", "name", "level"}
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			lrng := rand.New(rand.NewSource(int64(g*1000 + 1)))
			for i := 0; i < opsPerG; i++ {
				id := ids[lrng.Intn(len(ids))]
				cur, err := s.Version(id)
				if err != nil {
					continue
				}
				baseline := cur
				if lrng.Intn(2) == 0 && cur > 0 {
					baseline = cur - 1
				}
				set := map[string]string{
					props[lrng.Intn(len(props))]: fmt.Sprintf("g%d-i%d", g, i),
				}
				_, _ = s.Write(ontology.WriteRequest{ObjectID: id, Baseline: baseline, Set: set})
			}
		}(g)
	}
	wg.Wait()

	// 重放：判定日志的 ID 顺序即真实实现实际执行的串行顺序。
	recs := s.Decisions()
	replayed := 0
	for _, rec := range recs {
		if rec.Baseline == 0 && rec.CurrentVersion == 0 {
			continue // 创建阶段的提交已在两侧分别完成
		}
		ver, err := m.Write(ontology.WriteRequest{
			ObjectID: rec.ObjectID,
			Baseline: rec.Baseline,
			Set:      rec.WriteValues,
		})
		replayed++
		switch rec.Verdict {
		case ontology.VerdictCommitted:
			if err != nil {
				t.Fatalf("record %d: store committed v%d, naive rejected: %v", rec.ID, rec.NewVersion, err)
			}
			if ver != rec.NewVersion {
				t.Fatalf("record %d: store v%d, naive v%d", rec.ID, rec.NewVersion, ver)
			}
		case ontology.VerdictStaleBaseline:
			if !ontology.IsKind(err, ontology.ErrKindStaleBaseline) {
				t.Fatalf("record %d: store stale, naive err=%v", rec.ID, err)
			}
		case ontology.VerdictDeleted:
			if !ontology.IsKind(err, ontology.ErrKindDeleted) {
				t.Fatalf("record %d: store deleted, naive err=%v", rec.ID, err)
			}
		case ontology.VerdictPropertyConflict:
			if !ontology.IsKind(err, ontology.ErrKindPropertyConflict) {
				t.Fatalf("record %d: store property-conflict, naive err=%v", rec.ID, err)
			}
		case ontology.VerdictValidation:
			if !ontology.IsKind(err, ontology.ErrKindValidation) {
				t.Fatalf("record %d: store validation, naive err=%v", rec.ID, err)
			}
		}
	}
	if replayed == 0 {
		t.Fatal("no write decisions replayed")
	}

	// 最终版本序列一致：每个实例的版本号与最终状态完全相同。
	for _, id := range ids {
		v, _ := s.Version(id)
		if mv := m.Version(id); mv != v {
			t.Fatalf("%s: store version %d != naive version %d", id, v, mv)
		}
		_, sp, err := s.ReadAt(id, v)
		if err != nil {
			t.Fatalf("read %s@%d: %v", id, v, err)
		}
		if mp := m.Props(id, v); fmt.Sprint(sp) != fmt.Sprint(mp) {
			t.Fatalf("%s: store props %v != naive props %v", id, sp, mp)
		}
	}
	t.Logf("replayed %d concurrent write decisions against naive model", replayed)
}
