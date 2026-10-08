// Command server 运行一个端到端演示场景：创建对象类型、写入双时态
// 历史事实、执行属性定义迁移，并在指定记录时刻重新展开历史。
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	s := ontology.NewStore()

	must(s.CreateObjectType("Employee", map[string]ontology.PropertyDef{
		"name":   {Name: "name", Type: ontology.TypeString, Required: true},
		"salary": {Name: "salary", Type: ontology.TypeInt, Required: false},
	}, 0))
	must(s.RegisterObject("Employee", "emp-1"))

	must(s.WriteFact(ontology.Fact{
		ObjectID: "emp-1", ValidTime: 10, RecordTime: 100,
		Values: map[string]ontology.Value{
			"name":   ontology.StringValue("alice"),
			"salary": ontology.IntValue(100),
		},
	}))

	// 迁移：salary 放宽为 float，新增必填属性 department，自记录时刻 200 生效。
	must(s.Migrate(ontology.Migration{
		TypeID: "Employee", EffectiveFrom: 200,
		NewProps: map[string]ontology.PropertyDef{
			"name":       {Name: "name", Type: ontology.TypeString, Required: true},
			"salary":     {Name: "salary", Type: ontology.TypeFloat, Required: false},
			"department": {Name: "department", Type: ontology.TypeString, Required: true},
		},
	}))

	must(s.WriteFact(ontology.Fact{
		ObjectID: "emp-1", ValidTime: 10, RecordTime: 300,
		Values: map[string]ontology.Value{
			"name":       ontology.StringValue("alice"),
			"salary":     ontology.FloatValue(120.5),
			"department": ontology.StringValue("eng"),
		},
	}))

	// 在记录时刻 150 展开：只能看到第一条事实，且按旧版本解释，
	// department 标记为 not-applicable（当时尚不存在）。
	res, err := s.Expand(ontology.ExpandRequest{
		ObjectID: "emp-1", AsOfRecord: 150, ValidFrom: 0, ValidTo: 1000,
	})
	must(err)
	for _, fv := range res.Facts {
		fmt.Printf("valid=%d record=%d schemaVersion=%d\n", fv.ValidTime, fv.RecordTime, fv.SchemaVersionID)
		for name, ps := range fv.Props {
			fmt.Printf("  %-12s %s %+v\n", name, ps.Status, ps.Value)
		}
	}

	for _, r := range s.AuditLog() {
		fmt.Printf("audit #%d %s %s versions=%v\n", r.Seq, r.Op, r.Verdict, r.Versions)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
