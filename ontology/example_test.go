package ontology_test

import (
	"context"
	"fmt"

	"ontology/ontology"
)

func ExamplePlatform() {
	ctx := context.Background()
	p := ontology.New()

	_ = p.CreateType(ctx, "Employee", nil)
	_ = p.CreateType(ctx, "Manager", []string{"Employee"})
	_ = p.CreateSubject(ctx, "auditor")

	// Employee 上声明 DENY：salary 对 auditor 不可见。
	_, _ = p.PutRule(ctx, "Employee", "auditor", "salary", ontology.EffectDeny)

	first, _ := p.Export(ctx, ontology.ExportRequest{
		ExportID:   "report-Q1",
		TypeID:     "Manager",
		ObjectID:   "mgr-42",
		SubjectID:  "auditor",
		Attributes: []string{"name", "salary"},
		Values:     map[string]any{"name": "Ada", "salary": 999999},
	})
	fmt.Println("excluded:", first.Excluded[0].Attribute, "on", first.Excluded[0].DeclaringType)

	// 事后删除规则并重组继承链。
	_, _ = p.DeleteRule(ctx, "Employee", "auditor", "salary")
	_ = p.CreateType(ctx, "Contractor", nil)
	_ = p.SetParents(ctx, "Manager", []string{"Contractor"})

	// 历史导出的依据仍可解析为命中时刻的内容快照。
	traced, err := p.Trace(ctx, "report-Q1", "salary")
	fmt.Println("trace err:", err)
	fmt.Println("traced effect:", traced.Rule.Effect, "declared on:", traced.Rule.DeclaringType)

	// 审计“当时未被排除”的属性得到可区分的结果而非记录缺失。
	_, err = p.Trace(ctx, "report-Q1", "name")
	fmt.Println("name trace kind:", ontology.KindOf(err))

	// Output:
	// excluded: salary on Employee
	// trace err: <nil>
	// traced effect: DENY declared on: Employee
	// name trace kind: ATTRIBUTE_NOT_EXCLUDED
}
