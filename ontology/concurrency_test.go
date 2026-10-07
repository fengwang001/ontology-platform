package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// TestConcurrentCommitsAndWritesMatchNaive 验证并发提交的字段变更与
// 并发写入交织时，Engine 的最终状态等价于按全序日志串行重放的结果。
func TestConcurrentCommitsAndWritesMatchNaive(t *testing.T) {
	e := NewEngine()
	ot := NewObjectType("E")
	ot.Fields["n"] = &FieldDef{Name: "n", Type: IntType, Constraint: NumRange(0, 10000), Nullable: true}
	e.RegisterObjectType(ot)

	const committers, writers, rounds = 4, 6, 40
	var wg sync.WaitGroup

	// 变更提交方：基于当前定义构造收紧/放宽/新增字段的打包提交。
	for g := 0; g < committers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < rounds; i++ {
				defs := e.ObjectTypeDef("E")
				cur := defs["n"]
				old := cur // 拷贝当前定义作为 CAS 基线
				lo := float64(rng.Intn(500))
				hi := 500 + float64(rng.Intn(9501))
				nw := old
				nw.Constraint = NumRange(lo, hi)
				batch := []FieldChange{{ObjectType: "E", Field: "n", Old: &old, New: &nw}}
				if rng.Intn(3) == 0 {
					// 打包里再加一个带默认的新增字段。
					fname := fmt.Sprintf("extra%d", rng.Intn(3))
					if _, ok := defs[fname]; !ok {
						batch = append(batch, FieldChange{ObjectType: "E", Field: fname,
							New: &FieldDef{Name: fname, Type: IntType, HasDefault: true, Default: IntValue(0)}})
					}
				}
				e.Commit(batch)
			}
		}(g)
	}

	// 写入方：随机写入/覆盖实例，取值可能落在收紧后的约束之外（应被拒绝）。
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(1000 + int64(g)))
			for i := 0; i < rounds; i++ {
				in := Instance{
					ID:     fmt.Sprintf("w%d-%d", g, rng.Intn(20)),
					Type:   "E",
					Values: map[string]Value{"n": IntValue(int64(rng.Intn(10001)))},
				}
				_ = e.Write(in) // 拒绝也是合法结果，由日志记录
			}
		}(g)
	}
	wg.Wait()

	// 用全序日志在独立的朴素串行实现上重放。
	n := NewNaive()
	not := NewObjectType("E")
	not.Fields["n"] = &FieldDef{Name: "n", Type: IntType, Constraint: NumRange(0, 10000), Nullable: true}
	n.RegisterObjectType(not)

	log := e.Log()
	commitOps, writeOps := 0, 0
	for _, entry := range log {
		switch entry.Op {
		case OpCommit:
			commitOps++
			res := n.Commit(entry.Batch)
			if res.Accepted != entry.Accepted {
				t.Fatalf("seq %d: naive accepted=%v, engine accepted=%v", entry.Seq, res.Accepted, entry.Accepted)
			}
			if res.Categories != entry.Categories {
				t.Fatalf("seq %d: naive categories=%v, engine=%v", entry.Seq, res.Categories, entry.Categories)
			}
		case OpWrite:
			writeOps++
			err := n.Write(*entry.Write)
			if (err == nil) != entry.Accepted {
				t.Fatalf("seq %d: naive write accepted=%v, engine=%v", entry.Seq, err == nil, entry.Accepted)
			}
		}
	}
	if commitOps == 0 || writeOps == 0 {
		t.Fatalf("log must contain both kinds of ops: commits=%d writes=%d", commitOps, writeOps)
	}

	// 最终接受的字段定义必须一致。
	if !reflect.DeepEqual(e.ObjectTypeDef("E"), n.ObjectTypeDef("E")) {
		t.Fatalf("field defs diverge:\nengine=%+v\nnaive=%+v", e.ObjectTypeDef("E"), n.ObjectTypeDef("E"))
	}
	// 最终写入结果必须一致。
	el, nl := e.LiveInstances("E"), n.LiveInstances("E")
	sort.Slice(el, func(i, j int) bool { return el[i].ID < el[j].ID })
	sort.Slice(nl, func(i, j int) bool { return nl[i].ID < nl[j].ID })
	if !reflect.DeepEqual(el, nl) {
		t.Fatalf("live instances diverge: engine=%d naive=%d", len(el), len(nl))
	}

	// 每次提交都有完整的判定记录：变更内容、检查依据与结论。
	decisions := e.Decisions()
	if len(decisions) == 0 {
		t.Fatal("no decisions recorded")
	}
	totalItems := 0
	for _, entry := range log {
		if entry.Op == OpCommit {
			totalItems += len(entry.Batch)
		}
	}
	if len(decisions) != totalItems {
		t.Fatalf("decisions %d != committed items %d", len(decisions), totalItems)
	}
	for _, d := range decisions {
		if d.Seq == 0 || d.Reason == "" {
			t.Fatalf("decision missing seq/reason: %+v", d)
		}
	}
	t.Logf("ops: %d commits (%d items) + %d writes, all decisions recorded", commitOps, totalItems, writeOps)
}
