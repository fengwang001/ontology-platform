// Command indexdemo 演示属性索引增量维护子系统的关键能力：
// 乱序事件、重复投递、索引依据字段原子切换、E2 回滚与四类错误。
package main

import (
	"fmt"

	ontologyindex "ontology/ontology"
)

func main() {
	s := ontologyindex.NewSchema()
	s.AddType(&ontologyindex.ObjectType{
		Name: "Person",
		Versions: []ontologyindex.TypeVersion{
			{EffectiveAt: 1, Fields: []ontologyindex.FieldVersion{
				{Physical: "ssn", PropertyID: "ssn"},
				{Physical: "name", PropertyID: "name"},
			}},
			{EffectiveAt: 100, Fields: []ontologyindex.FieldVersion{
				{Physical: "ssn", PropertyID: "ssn", Deprecated: true, ReplacedBy: "tax_id"},
				{Physical: "tax_id", PropertyID: "tax_id"},
				{Physical: "name", PropertyID: "name", Deprecated: true},
			}},
		},
	})

	aud := &ontologyindex.MemoryAuditor{}
	eng := ontologyindex.NewEngine(s, aud)
	must(eng.CreateIndex("persons_by_tax", "Person", "ssn", ontologyindex.ConstraintUnique))

	ingest := func(id, obj, prop string, v string, ts int64) {
		err := eng.Ingest(ontologyindex.ChangeEvent{
			EventID: id, ObjectType: "Person", ObjectID: obj,
			PropertyID: prop, NewValue: ontologyindex.StringValue(v),
			EffectiveAt: ontologyindex.LogicalClock(ts),
		})
		fmt.Printf("ingest %-4s -> %v\n", id, err)
	}

	// 1) 乱序 + 重复到达：最终只认逻辑顺序。
	ingest("e3", "p1", "ssn", "SSN-3", 30)
	ingest("e1", "p1", "ssn", "SSN-1", 10)
	ingest("e1", "p1", "ssn", "SSN-1", 10) // 重复
	ingest("e2", "p1", "ssn", "SSN-2", 20)
	show(eng, "persons_by_tax", "SSN-3")

	// 2) 切换依据字段 ssn -> tax_id，切换点 ts=100。
	must(eng.BeginSwitch("persons_by_tax", 100))
	_, err := eng.Lookup("persons_by_tax", ontologyindex.StringValue("SSN-3"))
	fmt.Printf("切换中查询 -> %T %v\n", err, err) // E4
	ingest("n1", "p1", "tax_id", "TAX-1", 120)
	must(eng.CommitSwitch("persons_by_tax"))
	show(eng, "persons_by_tax", "TAX-1")

	// 3) E1：废弃且无替代。
	must(eng.CreateIndex("persons_by_name", "Person", "name", ontologyindex.ConstraintDuplicate))
	err = eng.Ingest(ontologyindex.ChangeEvent{EventID: "z", ObjectType: "Person", ObjectID: "p9",
		PropertyID: "name", NewValue: ontologyindex.StringValue("Z"), EffectiveAt: 100})
	fmt.Printf("废弃字段写入 -> %v\n", err)

	fmt.Printf("审计记录条数=%d\n", len(aud.Snapshot()))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func show(eng *ontologyindex.Engine, idx, v string) {
	got, err := eng.Lookup(idx, ontologyindex.StringValue(v))
	fmt.Printf("lookup %-8s -> %v (err=%v)\n", v, got, err)
}
