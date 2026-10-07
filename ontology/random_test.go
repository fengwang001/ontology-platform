package ontology

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// 随机操作序列下，Store.Traverse 与独立朴素模型逐条对照；
// 每次对照的输入、历史基准与结论都记录到审计日志以便事后核查。
func TestRandomOpSequenceAgainstNaive(t *testing.T) {
	auditDir := os.Getenv("ONTOLOGY_AUDIT_DIR")
	for _, seed := range []int64{1, 7, 42, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomSequence(t, seed, auditDir)
		})
	}
}

type auditRecord struct {
	Seq       int      `json:"seq"`
	AsOf      Version  `json:"asOf"`
	Start     string   `json:"start"`
	MaxDepth  int      `json:"maxDepth"`
	MaxVisit  int      `json:"maxVisited"`
	StoreErr  string   `json:"storeErr"`
	NaiveErr  string   `json:"naiveErr"`
	StoreNode []string `json:"storeNodes"`
	NaiveNode []string `json:"naiveNodes"`
	StoreEdge []string `json:"storeEdges"`
	NaiveEdge []string `json:"naiveEdges"`
	Match     bool     `json:"match"`
}

func runRandomSequence(t *testing.T, seed int64, auditDir string) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	d := newDual()

	// 固定两类对象、两类链接。
	if _, err := d.defineObjectType("T1", []PropertyDef{
		{ID: "t1s", Name: "s", Type: TypeString},
		{ID: "t1i", Name: "i", Type: TypeInt},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.defineObjectType("T2", []PropertyDef{
		{ID: "t2b", Name: "b", Type: TypeBool},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.defineLinkType("L1", "T1", "T1", ManyToMany); err != nil {
		t.Fatal(err)
	}
	if _, err := d.defineLinkType("L2", "T1", "T2", ManyToMany); err != nil {
		t.Fatal(err)
	}

	// 测试侧影子状态（用于生成合法操作）。
	type objInfo struct{ typeID string }
	objects := map[string]objInfo{}
	links := map[LinkID]bool{}
	defs := map[string][]PropertyDef{
		"T1": {{ID: "t1s", Name: "s", Type: TypeString}, {ID: "t1i", Name: "i", Type: TypeInt}},
		"T2": {{ID: "t2b", Name: "b", Type: TypeBool}},
	}
	propSeq := 0
	objSeq := 0

	randValue := func(vt ValueType) Value {
		switch vt {
		case TypeString:
			return fmt.Sprintf("v%d", rng.Intn(1000))
		case TypeInt:
			return int64(rng.Intn(1000))
		case TypeBool:
			return rng.Intn(2) == 0
		default:
			return float64(rng.Intn(1000))
		}
	}
	aliveOfType := func(typeID string) []string {
		var out []string
		for id, oi := range objects {
			if oi.typeID == typeID {
				out = append(out, id)
			}
		}
		return out
	}

	var auditFile *os.File
	if auditDir != "" {
		if err := os.MkdirAll(auditDir, 0o755); err == nil {
			auditFile, _ = os.Create(filepath.Join(auditDir, fmt.Sprintf("audit-seed%d.jsonl", seed)))
		}
		if auditFile != nil {
			defer auditFile.Close()
		}
	}

	const ops = 400
	checks := 0
	for step := 0; step < ops; step++ {
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19: // put object 20%
			objSeq++
			typeID := []string{"T1", "T2"}[rng.Intn(2)]
			id := fmt.Sprintf("obj%d", objSeq)
			props := map[string]Value{}
			for _, pd := range defs[typeID] {
				if rng.Intn(2) == 0 {
					props[pd.Name] = randValue(pd.Type)
				}
			}
			if _, err := d.putObject(typeID, id, props); err == nil {
				objects[id] = objInfo{typeID: typeID}
			}
		case 20, 21, 22, 23, 24, 25, 26, 27, 28, 29: // set property 10%
			var ids []string
			for id := range objects {
				ids = append(ids, id)
			}
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.Intn(len(ids))]
			pds := defs[objects[id].typeID]
			pd := pds[rng.Intn(len(pds))]
			d.setProperty(id, pd.Name, randValue(pd.Type))
		case 30, 31, 32: // delete object 3%
			var ids []string
			for id := range objects {
				ids = append(ids, id)
			}
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.Intn(len(ids))]
			if _, err := d.deleteObject(id); err == nil {
				delete(objects, id)
			}
		case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49: // add link 10%
			lt := []string{"L1", "L2"}[rng.Intn(2)]
			fromTy, toTy := "T1", "T1"
			if lt == "L2" {
				toTy = "T2"
			}
			froms, tos := aliveOfType(fromTy), aliveOfType(toTy)
			if len(froms) == 0 || len(tos) == 0 {
				continue
			}
			from, to := froms[rng.Intn(len(froms))], tos[rng.Intn(len(tos))]
			id := LinkID{Type: lt, From: from, To: to}
			if links[id] {
				continue
			}
			if _, err := d.addLink(lt, from, to); err == nil {
				links[id] = true
			} else if err != ErrCardinalityViolate && err != ErrLinkExists {
				t.Fatalf("unexpected addLink error: %v", err)
			}
		case 50, 51, 52, 53, 54: // remove link 5%
			var ls []LinkID
			for id := range links {
				ls = append(ls, id)
			}
			if len(ls) == 0 {
				continue
			}
			id := ls[rng.Intn(len(ls))]
			if _, err := d.removeLink(id.Type, id.From, id.To); err == nil {
				delete(links, id)
			}
		case 60, 61, 62, 63, 64, 65: // migrate object type 6%
			typeID := []string{"T1", "T2"}[rng.Intn(2)]
			pds := defs[typeID]
			switch rng.Intn(3) {
			case 0: // 新增属性
				propSeq++
				np := PropertyDef{ID: fmt.Sprintf("p%d", propSeq), Name: fmt.Sprintf("n%d", propSeq),
					Type: []ValueType{TypeString, TypeInt, TypeBool}[rng.Intn(3)]}
				if _, err := d.migrateObjectType(typeID, []PropertyDef{np}, nil); err == nil {
					defs[typeID] = append(append([]PropertyDef(nil), pds...), np)
				}
			case 1: // 重命名属性（保持 ID）
				propSeq++
				idx := rng.Intn(len(pds))
				rp := pds[idx]
				rp.Name = fmt.Sprintf("n%d", propSeq)
				if _, err := d.migrateObjectType(typeID, []PropertyDef{rp}, nil); err == nil {
					next := append([]PropertyDef(nil), pds...)
					next[idx] = rp
					defs[typeID] = next
				}
			case 2: // 删除属性（至少保留一个）
				if len(pds) <= 1 {
					continue
				}
				idx := rng.Intn(len(pds))
				if _, err := d.migrateObjectType(typeID, nil, []string{pds[idx].ID}); err == nil {
					next := append([]PropertyDef(nil), pds[:idx]...)
					defs[typeID] = append(next, pds[idx+1:]...)
				}
			}
		case 70, 71, 72: // adjust cardinality 3%
			lt := []string{"L1", "L2"}[rng.Intn(2)]
			card := []Cardinality{OneToOne, OneToMany, ManyToOne, ManyToMany}[rng.Intn(4)]
			d.adjustCardinality(lt, card)
		case 80, 81: // compact 2%
			cur := d.s.CurrentVersion()
			if cur > d.s.Horizon()+2 {
				keep := d.s.Horizon() + Version(rng.Int63n(int64(cur-d.s.Horizon())))
				d.compact(keep)
			}
		case 90, 91: // declare gap 2%
			var ids []string
			for id := range objects {
				ids = append(ids, id)
			}
			if len(ids) == 0 {
				continue
			}
			id := ids[rng.Intn(len(ids))]
			cur := d.s.CurrentVersion()
			from := Version(rng.Int63n(int64(cur) + 1))
			d.declareGap("object-props", id, from, from+2)
		}

		// 每个操作后随机挑一个历史时刻做对照遍历。
		cur := d.s.CurrentVersion()
		asOf := Version(rng.Int63n(int64(cur) + 2))
		start := fmt.Sprintf("obj%d", rng.Intn(objSeq+2))
		req := TraverseRequest{
			Start:      start,
			AsOf:       asOf,
			MaxDepth:   rng.Intn(9),
			MaxVisited: rng.Intn(40),
		}
		gotRes, gotErr := d.s.Traverse(req)
		wantRes, wantErr := d.n.Traverse(req)

		rec := auditRecord{Seq: step, AsOf: asOf, Start: start, MaxDepth: req.MaxDepth, MaxVisit: req.MaxVisited}
		if gotErr != nil {
			rec.StoreErr = gotErr.(*TraverseError).Kind.String()
		}
		if wantErr != nil {
			rec.NaiveErr = wantErr.(*TraverseError).Kind.String()
		}
		match := rec.StoreErr == rec.NaiveErr
		if match && gotErr == nil {
			gn, ge := normalize(gotRes)
			wne, wee := normalize(wantRes)
			rec.StoreNode, rec.StoreEdge = gn, ge
			rec.NaiveNode, rec.NaiveEdge = wne, wee
			match = reflect.DeepEqual(gn, wne) && reflect.DeepEqual(ge, wee)
		}
		rec.Match = match
		if auditFile != nil {
			line, _ := json.Marshal(rec)
			auditFile.Write(append(line, '\n'))
		}
		if !match {
			t.Fatalf("step=%d req=%+v storeErr=%v naiveErr=%v", step, req, gotErr, wantErr)
		}
		// 判定日志完整性：每次判定都必须锚定请求的历史时刻。
		if gotRes != nil {
			for i, dec := range gotRes.Decisions {
				if dec.AsOf != asOf {
					t.Fatalf("decision %d has AsOf=%d, want %d", i, dec.AsOf, asOf)
				}
				if dec.Seq != i {
					t.Fatalf("decision seq broken at %d", i)
				}
			}
		}
		checks++
	}
	t.Logf("seed=%d: %d traversals compared, all match", seed, checks)
}
