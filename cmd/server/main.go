// 属性索引一致性子系统演示：写入、查询、批量回滚、崩溃恢复。
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	log := ontology.NewBufferLogger()
	faults := ontology.NewFaultInjector()
	exact := ontology.NewIndex("status-exact", ontology.ExactKey)
	prefix := ontology.NewIndex("status-prefix", ontology.PrefixKey(1))
	s := ontology.NewStore(ontology.NewMemoryWAL(), faults, log)
	s.RegisterProperty("status", exact, prefix)
	s.AddInstance("obj-1")
	s.AddInstance("obj-2")
	s.AddInstance("obj-3")
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(s.Write("obj-1", "status", ontology.Of("active")))
	must(s.Write("obj-2", "status", ontology.Of("active")))
	must(s.Write("obj-3", "status", ontology.Of(""))) // 显式默认值，区别于 Absent
	hits, _ := s.QueryByValue("status", ontology.Of("active"))
	fmt.Printf("status=active -> %v\n", hits)
	hits, _ = s.QueryAbsent("status")
	fmt.Printf("status=ABSENT -> %v\n", hits)
	// 演示批量写入整体回滚：obj-2 的索引维护被注入失败。
	faults.FailIndexFor("status-exact", "obj-2")
	errs := s.BatchWrite("status", []ontology.InstanceWrite{
		{InstanceID: "obj-1", Value: ontology.Of("paused")},
		{InstanceID: "obj-2", Value: ontology.Of("paused")},
	})
	fmt.Printf("batch errors: %v\n", errs)
	faults.Reset()
	// 演示崩溃与恢复：在"属性值已改、索引未改"切分点崩溃。
	faults.CrashWhen(func(p ontology.CutPoint, _ uint64, _ string) bool {
		return p == ontology.CutAfterValue
	})
	func() {
		defer func() { recover() }()
		_ = s.Write("obj-1", "status", ontology.Of("archived"))
	}()
	fmt.Println("crashed at cut point: after-value")
	for _, d := range s.Recover() {
		fmt.Printf("recover: txn=%d kind=%s decision=%s reason=%s\n",
			d.TxnID, d.Kind, d.Decision, d.Reason)
	}
	v, _ := s.Get("obj-1", "status")
	fmt.Printf("obj-1 status after recovery: %+v (rolled back)\n", v)
	fmt.Println("---- operation log ----")
	fmt.Print(log.String())
}
