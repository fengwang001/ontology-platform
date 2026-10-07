// 命令 server 是属性级并发写入判定的一个最小演示。
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	s := ontology.NewStore()
	s.RegisterObjectType(ontology.ObjectType{
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
	})
	if _, err := s.Create("Doc", "d1", map[string]string{"title": "a", "status": "draft", "hits": "0"}); err != nil {
		panic(err)
	}

	// 第一次并发写入：title，声明读取 status。
	r1, _ := s.Write(ontology.WriteRequest{ObjectID: "d1", Baseline: 1, Set: map[string]string{"title": "b"}})
	fmt.Printf("title write committed at v%d\n", r1.Version)

	// 第二次并发写入：status。显式写集合不相交，
	// 但落入 title 写入所声明的钩子读集合 {status}：被拒绝。
	_, err := s.Write(ontology.WriteRequest{ObjectID: "d1", Baseline: 1, Set: map[string]string{"status": "published"}})
	fmt.Printf("status write rejected: %v\n", err)

	// 第三次并发写入：hits。写集合与相关读集合均不相交，合并成功。
	r3, _ := s.Write(ontology.WriteRequest{ObjectID: "d1", Baseline: 1, Set: map[string]string{"hits": "1"}})
	fmt.Printf("hits write committed at v%d\n", r3.Version)

	// 判定日志重放核验。
	for _, rec := range s.Decisions() {
		fmt.Printf("#%d %s baseline=%d write=%v read=%v -> %s (%s)\n",
			rec.ID, rec.ObjectID, rec.Baseline, rec.WriteSet, rec.ReadSet, rec.Verdict, rec.Reason)
	}
}
