// Package ontology 实现对象类型范围内的「动作事务审计溯源」子系统。
//
// 三个协作者：
//   - Executor：动作事务执行模块，保证审计记录与状态变更同生同灭；
//   - AuditStore：审计记录生成与存证模块，单调序号、只追加、哈希链防篡改；
//   - Replayer：按审计序列重放，独立重建任意序号 / 区间的状态。
//
// 最小用法：
//
//	store := ontology.NewAuditStore()
//	_ = store.Seed("A", "init")            // 声明一个已有实例
//	exec, _ := ontology.NewExecutor(store)
//	rec, err := exec.ExecuteAction("act1",
//	    map[string]string{"A": "v1"}, false) // 提交；true 表示整体回退
//	st := ontology.NewReplayer(store).StateAt(rec.Seq)
//	_ = st["A"]                              // "v1"
//
//	// 追加订正（原记录原样保留，追溯覆盖历史重建）
//	_, _ = exec.Correct("fix1", rec.Seq, map[string]string{"A": "v1-fixed"})
//
//	// 按区间重放：回退 / 订正仅为「尝试事件」，不作为状态来源
//	evs, _ := ontology.NewReplayer(store).ReplayRange(0, store.Len())
//
// 详见同目录 DESIGN.md。
package ontology
