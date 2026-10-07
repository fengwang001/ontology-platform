package main

import (
	"fmt"
	"log"

	ontology "ontology/ontology"
)

// 演示：权限固化导出 + 规则变更后的历史审计追溯。
func main() {
	p := ontology.NewPlatform()
	p.AddPrincipal("alice")
	must(p.AddType("Base", "", []string{"id", "name", "secret"}))
	must(p.AddType("Leaf", "Base", []string{"token"}))

	ruleID, err := p.UpsertRule("Base", "alice", "secret", ontology.EffectDeny, "confidential")
	must(err)
	fmt.Printf("rule created: %s\n", ruleID)

	exp, err := p.Export("Leaf", "alice")
	must(err)
	fmt.Printf("export %s: included=%v exclusions=%v\n", exp.ID, exp.Included, exp.Exclusions)

	// 规则之后被删除并被新规则替换——历史导出不受影响。
	must(p.DeleteRule("Base", "alice", "secret"))
	if _, err := p.UpsertRule("Base", "alice", "secret", ontology.EffectAllow, "downgraded"); err != nil {
		log.Fatal(err)
	}

	rec, err := p.AuditTrace(exp.ID, "secret")
	must(err)
	fmt.Printf("audit %s/secret: rule=%s effect=%s content=%q declaredOn=%s\n",
		rec.ExportID, rec.RuleID, rec.Rule.Effect, rec.Rule.Content, rec.DeclaredOn)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
