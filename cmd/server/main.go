// server 演示本体平台的两种并发控制：普通乐观更新与动作独占占用。
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	s := ontology.NewStore()
	s.RegisterType(ontology.ObjectType{
		Name: "Ticket",
		Properties: []ontology.PropertySpec{
			{Name: "title", Required: true},
			{Name: "state"},
		},
	})
	if err := s.CreateInstance("T1", "Ticket", map[string]ontology.PropertyValue{
		"title": "demo", "state": "open",
	}); err != nil {
		panic(err)
	}

	// 普通乐观更新。
	v, err := s.Update("caller-1", "T1", 1, ontology.Mutation{Property: "title", Value: "demo-2"})
	fmt.Printf("optimistic update -> version=%d err=%v\n", v, err)

	// 动作申请独占占用并执行生命周期状态机转换。
	occ, err := s.Acquire("action-close", "T1")
	if err != nil {
		panic(err)
	}
	if err := occ.Apply(ontology.Mutation{Property: "state", Value: "closed"}); err != nil {
		panic(err)
	}

	// 占用期间的普通更新被明确拒绝，版本号不变。
	if _, err := s.Update("caller-2", "T1", 2,
		ontology.Mutation{Property: "title", Value: "blocked"}); err != nil {
		fmt.Printf("update during occupancy rejected: %v\n", err)
	}

	v, err = occ.Commit()
	fmt.Printf("action commit -> version=%d err=%v\n", v, err)

	// 释放后普通更新按最新版本恢复判定。
	v, err = s.Update("caller-2", "T1", v,
		ontology.Mutation{Property: "title", Value: "after-release"})
	fmt.Printf("update after release -> version=%d err=%v\n", v, err)

	snap, _ := s.Get("T1")
	fmt.Printf("final snapshot: %+v\n", snap)

	fmt.Println("--- audit log ---")
	for _, rec := range s.Audit().Records() {
		fmt.Printf("#%d %-9s %-4s actor=%-12s code=%-18s version=%d basis=%s\n",
			rec.Seq, rec.Kind, rec.Instance, rec.Actor, rec.Code, rec.NewVersion, rec.Basis)
	}
}
