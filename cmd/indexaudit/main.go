// 命令行 demo：演示基线/增量划分、失败后不可用、独立复核发现注入
// 不一致，以及单条目复核的 O(1) 历史读取取证。判定日志以 JSON 行
// 打印到 stderr，人类可读结论打印到 stdout。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/ontology"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func main() {
	dlog := ontology.NewDecisionLog()
	dlog.SetSink(os.Stderr)

	store := ontology.NewStore()
	mgr := ontology.NewIndexManager(store, dlog)
	ver := ontology.NewVerifier(store, dlog)

	mgr.Declare("Employee", "by_city", "city")

	// 基线范围的既有状态。
	mgr.Write("Employee", "e1", map[string]ontology.PropertyValue{"city": "BJ"})
	mgr.Write("Employee", "e2", map[string]ontology.PropertyValue{"city": "SH"})

	fmt.Println("== 1) rebuild baseline only ==")
	audit, err := mgr.Rebuild("Employee", "by_city")
	must(err)
	fmt.Printf("baselineEndLSN=%d completionLSN=%d entries=%d digest=%s\n",
		audit.BaselineEndLSN, audit.CompletionLSN, len(audit.Entries), audit.Digest[:12])

	fmt.Println("== 2) delta writes after rebuild are applied in write order ==")
	mgr.Write("Employee", "e3", map[string]ontology.PropertyValue{"city": "BJ"})
	mgr.Write("Employee", "e1", map[string]ontology.PropertyValue{"city": "SZ"})
	res, err := mgr.Query("Employee", "by_city", "BJ")
	must(err)
	fmt.Printf("query city=BJ -> %v (inconsistency declared=%v)\n",
		res.ObjectIDs, res.InconsistencyDeclared)

	fmt.Println("== 3) independent verify on clean state ==")
	rep := ver.VerifyAudit(audit, store)
	fmt.Printf("consistent=%v entries=%d historyReads=%d\n",
		rep.Consistent, rep.EntriesChecked, rep.HistoryRecordsRead)

	fmt.Println("== 4) tamper one entry; verify must find it without any rebuild logs ==")
	_, changed := mgr.TamperEntryForTest("Employee", "by_city", "e3", "XX")
	if !changed {
		must(errors.New("tamper failed"))
	}
	rep2, err := ver.VerifyManager(mgr, "Employee", "by_city", true)
	must(err)
	fmt.Printf("consistent=%v\n", rep2.Consistent)
	for _, mm := range rep2.Mismatches {
		fmt.Println("  mismatch:", mm.String())
	}

	res2, err := mgr.Query("Employee", "by_city", "XX")
	must(err)
	fmt.Printf("query after verify-flag: ids=%v declared=%v reason=%q\n",
		res2.ObjectIDs, res2.InconsistencyDeclared, res2.InconsistencyReason)

	fmt.Println("== 5) failed rebuild => unavailable, distinct from not-declared ==")
	mgr.FailNextRebuild(true)
	_, err = mgr.Rebuild("Employee", "by_city")
	fmt.Println("rebuild error:", err)
	_, qerr := mgr.Query("Employee", "by_city", "BJ")
	fmt.Println("query error:", qerr)

	_, nerr := mgr.Query("Employee", "no_such_index", "BJ")
	fmt.Println("undeclared query error:", nerr)

	fmt.Println("== 6) constant-history verification proof ==")
	// 让 e2 的历史变得很长，然后只复核它一个条目；读取记录数保持常数。
	for i := 0; i < 1000; i++ {
		city := []string{"BJ", "SH", "GZ", "SZ"}[i%4]
		mgr.Write("Employee", "e2", map[string]ontology.PropertyValue{"city": city})
	}
	audit3, err := mgr.Rebuild("Employee", "by_city")
	must(err)
	store.ResetHistoryInspections()
	mm, err := ver.VerifyEntry(audit3, store, "e2")
	must(err)
	fmt.Printf("VerifyEntry(e2) mismatch=%v historyRecordsRead=%d (object history length=%d)\n",
		mm != nil, store.HistoryInspections(), store.HistoryLength("e2", "city"))
}
