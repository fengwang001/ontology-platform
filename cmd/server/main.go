// server 演示本体平台批量更新的崩溃原子性：
// 正常生效一批 → 模拟提交后崩溃 → 重启恢复 → 观察恢复终态。
package main

import (
	"fmt"
	"os"

	"ontology/ontology"
)

func main() {
	disk := ontology.NewSimDisk()
	audit := ontology.NewAuditLogger(os.Stdout)

	eng, err := ontology.NewEngine(disk, audit, nil)
	must(err)

	// 初始化两个本体实例。
	must(eng.Write("server-1", map[string]string{"status": "idle"}))
	must(eng.Write("server-2", map[string]string{"status": "idle"}))

	// 批次一：正常生效。
	muts := []ontology.Mutation{
		{Instance: "server-1", Props: map[string]string{"status": "deploying", "version": "2.0"}},
		{Instance: "server-2", Props: map[string]string{"status": "deploying", "version": "2.0"}},
	}
	id, status, err := eng.RunBatch(muts, ontology.BatchOptions{})
	must(err)
	fmt.Printf("batch %d -> %s\n", id, status)

	// 批次二：在提交记录落盘后的检查点阶段模拟进程中断。
	crashEng, err := ontology.NewEngine(disk, audit, func(p ontology.StagePoint) bool {
		return p.Phase == ontology.PhaseCheckpoint
	})
	must(err)
	muts2 := []ontology.Mutation{
		{Instance: "server-1", Props: map[string]string{"status": "running"}},
		{Instance: "server-2", Props: map[string]string{"status": "running"}},
	}
	id2, _, err := crashEng.RunBatch(muts2, ontology.BatchOptions{})
	fmt.Printf("batch %d interrupted: %v\n", id2, err)

	// 重启并恢复：提交记录已落盘，必须恢复为整批已生效。
	eng2, err := ontology.NewEngine(disk, audit, nil)
	must(err)
	rep, err := eng2.Recover()
	must(err)
	fmt.Printf("recover batch %d: commitRecordFound=%v classification=%s recordsScanned=%d\n",
		rep.Batch, rep.CommitRecordFound, rep.Classification, rep.JournalRecords)

	for _, in := range eng2.Snapshot() {
		fmt.Printf("instance %s version=%d props=%v\n", in.ID, in.Version, in.Props)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
