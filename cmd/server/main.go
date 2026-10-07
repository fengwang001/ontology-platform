// cmd/server 是双时态链接审计子系统的最小演示入口：
// 构造链接历史、调整基数约束版本，然后执行一次跨版本的历史审计
// 并以 JSON 输出审计报告与判定日志。
package main

import (
	"encoding/json"
	"fmt"
	"log"

	"ontology/ontology"
)

func main() {
	s := ontology.NewStore()
	s.RegisterObjectType("Person")
	s.RegisterObjectType("Company")
	typeReady := s.Now()
	if err := s.RegisterLinkType(ontology.LinkTypeDef{
		ID: "worksAt", LeftType: "Person", RightType: "Company",
	}); err != nil {
		log.Fatal(err)
	}

	must := func(err error) {
		if err != nil {
			log.Fatal(err)
		}
	}
	must(s.CreateLink("worksAt", "p1", "c1", 0))
	must(s.CreateLink("worksAt", "p1", "c2", 0))
	// 版本 2：每人至多任职一家公司 —— p1 自该版本起历史上曾违反。
	if _, err := s.AdjustCardinality("worksAt",
		ontology.Cardinality{Min: 0, Max: 1}, ontology.Cardinality{Min: 0, Max: -1}); err != nil {
		log.Fatal(err)
	}
	must(s.RevokeLink("worksAt", "p1", "c2", 100))

	rep, err := s.Audit(ontology.AuditRequest{
		LinkType: "worksAt", RecordFrom: typeReady, RecordTo: s.Now(),
		ValidAt: 0, ExpectedVersion: 2,
	})
	if err != nil {
		log.Fatal(err)
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))

	logs, _ := json.MarshalIndent(s.DecisionLog(), "", "  ")
	fmt.Println(string(logs))
}
