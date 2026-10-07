package main

import (
	"fmt"

	"ontology/ontology"
)

// 演示：对象整体删除与属性历史独立删除两套机制的交织。
func main() {
	svc := ontology.NewService()
	id := ontology.ObjectID("person-1")
	must(svc.CreateObject(id))
	r1, err := svc.AddHistory(id, "status", "active", 100)
	must(err)
	_, err = svc.AddHistory(id, "status", "suspended", 200)
	must(err)

	// 单独删除 r1，再整体删除对象；整体删除期间单独删除被拒绝。
	must(svc.DeleteRecord(id, r1))
	must(svc.DeleteObject(id))
	fmt.Println("delete record while object deleted:", svc.DeleteRecord(id, r1))

	// 复活：r1 的单独删除状态保留，不被隐式恢复。
	must(svc.RestoreObject(id))
	fmt.Println("query@150 after restore:", svc.QueryVisibleValue(id, "status", 150))
	fmt.Println("query@250 after restore:", svc.QueryVisibleValue(id, "status", 250))

	// 操作日志：输入、输出与三层条件取值。
	for _, e := range svc.Log() {
		fmt.Printf("seq=%d op=%s obj=%s rec=%s err=%v cond=%+v\n",
			e.Seq, e.Op, e.ObjectID, e.RecordID, e.Err, e.Cond)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
