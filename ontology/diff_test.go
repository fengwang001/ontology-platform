package ontology

import (
	"math/rand"
	"sort"
	"testing"
)

// TestRandomDifferentialAgainstNaive 随机生成操作序列，对每个读/写判定
// 比较优化实现与独立朴素重放模型；二者必须在错误类别、应用与否、
// 写入/忽略/移除字段集合、部分视图标记上完全一致。
func TestRandomDifferentialAgainstNaive(t *testing.T) {
	seeds := []int64{1, 2, 3, 7, 11, 42, 99, 1670, 2026}
	for _, seed := range seeds {
		t.Run("", func(t *testing.T) {
			runDiffScenario(t, seed)
		})
	}
}

func runDiffScenario(t *testing.T, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	g := NewGateway()
	nm := newNaiveModel(g)

	policy := PolicyIgnoreField
	if rng.Intn(2) == 0 {
		policy = PolicyRejectWhole
	}
	mustType(t, g, "T", []Attribute{{ID: "id0", Name: "n0", Kind: KindString}}, policy)

	subjects := []string{"s1", "s2", "s3", "admin"}
	// 存活标识符池与“曾经存在”的名字池（用于引用旧名字）。
	liveIDs := []string{"id0"}
	nextAttr := 1
	objects := []string{}

	pickSubject := func() string { return subjects[rng.Intn(len(subjects))] }
	pickLive := func() string { return liveIDs[rng.Intn(len(liveIDs))] }

	compareWrite := func(req WriteRequest, exists bool, action string) {
		t.Helper()
		nd := nm.decideWrite(req, exists, action)
		res, gerr := func() (*WriteResult, error) {
			if action == "create_object" {
				return g.CreateObject(req)
			}
			return g.Write(req)
		}()
		gk := ErrorKindOf(gerr)
		if gk != nd.errKind {
			t.Fatalf("seed=%d write err kind mismatch: gateway=%v naive=%v req=%+v",
				seed, gk, nd.errKind, req)
		}
		if nd.errKind != "" {
			return
		}
		sort.Strings(res.WrittenFields)
		sort.Strings(res.IgnoredFields)
		sort.Strings(nd.written)
		sort.Strings(nd.ignored)
		if res.Applied != nd.writeApplied ||
			!sameStrings(res.WrittenFields, nd.written) ||
			!sameStrings(res.IgnoredFields, nd.ignored) {
			t.Fatalf("seed=%d write result mismatch:\n gateway=%+v\n naive=%+v\n req=%+v",
				seed, res, nd, req)
		}
		if len(res.Denies) != len(nd.denyKinds) {
			t.Fatalf("seed=%d deny count mismatch: %+v vs %+v", seed, res.Denies, nd.denyKinds)
		}
		for _, d := range res.Denies {
			if nd.denyKinds[d.Ref] != d.Kind {
				t.Fatalf("seed=%d deny kind mismatch ref=%s: gateway=%s naive=%s",
					seed, d.Ref, d.Kind, nd.denyKinds[d.Ref])
			}
		}
	}

	compareRead := func(objectID, subject string) {
		t.Helper()
		nd := nm.decideRead(objectID, "T", subject)
		rr, err := g.Read(objectID, "T", subject)
		if ErrorKindOf(err) != nd.errKind {
			t.Fatalf("seed=%d read err kind mismatch: gateway=%v naive=%v obj=%s",
				seed, ErrorKindOf(err), nd.errKind, objectID)
		}
		if nd.errKind != "" {
			return
		}
		var visible []string
		for id := range rr.Object.Fields {
			visible = append(visible, id)
		}
		sort.Strings(visible)
		if !sameStrings(visible, nd.visible) ||
			!sameStrings(rr.RemovedAttrs, nd.removed) ||
			rr.PartialView != nd.partial {
			t.Fatalf("seed=%d read projection mismatch:\n gateway visible=%v removed=%v partial=%v\n naive visible=%v removed=%v partial=%v",
				seed, visible, rr.RemovedAttrs, rr.PartialView,
				nd.visible, nd.removed, nd.partial)
		}
	}

	for iter := 0; iter < 500; iter++ {
		latest := currentVer(t, g, "T")
		switch rng.Intn(8) {
		case 0, 1: // 演进
			var ch AttrChange
			if len(liveIDs) == 0 {
				id := "id" + itoa(nextAttr)
				nextAttr++
				ch = AttrChange{Kind: ChangeAdd, AttrID: id,
					NewName: "n" + itoa(nextAttr), NewAttr: KindInt}
			} else {
				target := pickLive()
				mode := rng.Intn(4)
				if len(liveIDs) == 1 && mode == 3 {
					mode = 0 // 保留至少一个存活属性
				}
				switch mode {
				case 0:
					id := "id" + itoa(nextAttr)
					nextAttr++
					ch = AttrChange{Kind: ChangeAdd, AttrID: id,
						NewName: "n" + itoa(nextAttr), NewAttr: KindInt}
				case 1:
					ch = AttrChange{Kind: ChangeRename, AttrID: target,
						NewName: "rn" + itoa(iter)}
				case 2:
					ch = AttrChange{Kind: ChangeTighten, AttrID: target, NewAttr: KindString}
				case 3:
					ch = AttrChange{Kind: ChangeDeprecate, AttrID: target}
				}
			}
			if _, err := g.Evolve("T", []AttrChange{ch}, ""); err != nil {
				t.Fatalf("seed=%d evolve %+v: %v", seed, ch, err)
			}
			if ch.Kind == ChangeAdd {
				liveIDs = append(liveIDs, ch.AttrID)
			}
			if ch.Kind == ChangeDeprecate {
				var kept []string
				for _, id := range liveIDs {
					if id != ch.AttrID {
						kept = append(kept, id)
					}
				}
				liveIDs = kept
			}
		case 2, 3: // 授权
			attr := pickLive()
			if rng.Intn(6) == 0 {
				attr = "*"
			}
			from := 1 + rng.Intn(latest+1)
			to := 0
			if rng.Intn(2) == 0 {
				to = from + rng.Intn(3)
			}
			op := OpRead
			if rng.Intn(2) == 0 {
				op = OpWrite
			}
			if rng.Intn(4) == 0 {
				_ = g.Revoke("T", pickSubject(), attr, op, from, to)
			} else {
				_ = g.Grant("T", pickSubject(), attr, op, from, to)
			}
		case 4: // 创建对象（随机引用 ID 或当前名字，偶发垃圾引用）
			latest = currentVer(t, g, "T")
			if len(liveIDs) == 0 {
				break
			}
			n := 1 + rng.Intn(2)
			fields := map[string]any{}
			st := g.store.getType("T")
			for i := 0; i < n; i++ {
				id := pickLive()
				ref := id
				if a := st.byID[id]; a != nil && rng.Intn(2) == 0 {
					ref = a.Name
				}
				if rng.Intn(10) == 0 {
					ref = "ghost" + itoa(iter) + itoa(i)
				}
				fields[ref] = iter
			}
			id := "obj" + itoa(len(objects))
			req := WriteRequest{ObjectID: id, TypeID: "T", Subject: pickSubject(),
				SchemaVer: latest, Fields: fields}
			if rng.Intn(12) == 0 {
				req.SchemaVer = latest - 1 // 故意过期
				if req.SchemaVer < 1 {
					req.SchemaVer = latest + 1
				}
			}
			compareWrite(req, false, "create_object")
			if g.store.getObject(id) != nil {
				objects = append(objects, id)
			}
		case 5, 6: // 写已存在对象
			if len(objects) == 0 || len(liveIDs) == 0 {
				break
			}
			oid := objects[rng.Intn(len(objects))]
			id := pickLive()
			ref := id
			st := g.store.getType("T")
			if a := st.byID[id]; a != nil && rng.Intn(2) == 0 {
				ref = a.Name
			}
			if rng.Intn(10) == 0 {
				ref = "ghost" + itoa(iter)
			}
			req := WriteRequest{ObjectID: oid, TypeID: "T", Subject: pickSubject(),
				SchemaVer: latest, Fields: map[string]any{ref: iter}}
			if rng.Intn(6) == 0 {
				req.SchemaVer = latest - 1
				if req.SchemaVer < 1 {
					req.SchemaVer = latest + 1
				}
			}
			compareWrite(req, true, "write")
		case 7: // 读
			if len(objects) == 0 {
				break
			}
			compareRead(objects[rng.Intn(len(objects))], pickSubject())
		}
	}

	// 日志必须打印每次判定的输入、输出与依据：抽查最近的读/写日志条目。
	var sawRead, sawWrite bool
	for _, e := range g.DecisionLog() {
		if e.Input == nil || e.Output == nil || e.Reason == "" {
			t.Fatalf("seed=%d incomplete decision log entry: %+v", seed, e)
		}
		if e.Action == "read" {
			sawRead = true
		}
		if e.Action == "write" || e.Action == "create_object" {
			sawWrite = true
		}
	}
	if !sawRead || !sawWrite {
		t.Fatalf("seed=%d decision log missing read/write evidence", seed)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
