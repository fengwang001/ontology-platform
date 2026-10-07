// 演示快照导出协同组件：持续写入的同时开启一次导出，
// 打印边界、快照与增量流。
package main

import (
	"fmt"
	"log"

	"ontology/snapshot"
)

func main() {
	j := snapshot.NewJournal()

	// 边界之前被接受的写入：两个对象 + 一条链接（同一动作事务）。
	j.AppendTransaction("txn-1", []snapshot.WriteSpec{
		{Kind: snapshot.KindObjectUpsert, Object: &snapshot.Object{ID: "alice", Type: "Person"}},
		{Kind: snapshot.KindObjectUpsert, Object: &snapshot.Object{ID: "acme", Type: "Company"}},
		{Kind: snapshot.KindLinkUpsert, Link: &snapshot.Link{ID: "alice-works-at-acme", Type: "WorksAt", Src: "alice", Dst: "acme"}},
		{Kind: snapshot.KindActionRecord, Action: &snapshot.ActionRecord{ID: "act-1", Action: "OnboardEmployee"}},
	})

	coord := snapshot.NewCoordinator(j)
	sess, err := coord.BeginExport(snapshot.Auto(), snapshot.Options{})
	if err != nil {
		log.Fatalf("begin export: %v", err)
	}

	// 导出期间持续到达的写入，将作为增量输出。
	j.AppendTransaction("txn-2", []snapshot.WriteSpec{
		{Kind: snapshot.KindObjectUpsert, Object: &snapshot.Object{ID: "bob", Type: "Person"}},
		{Kind: snapshot.KindLinkUpsert, Link: &snapshot.Link{ID: "bob-works-at-acme", Type: "WorksAt", Src: "bob", Dst: "acme"}},
		{Kind: snapshot.KindActionRecord, Action: &snapshot.ActionRecord{ID: "act-2", Action: "OnboardEmployee"}},
	})

	state, err := sess.Snapshot()
	if err != nil {
		log.Fatalf("snapshot: %v", err)
	}
	fmt.Printf("boundary: LSN %d\n", sess.Boundary())
	fmt.Printf("snapshot: %d objects, %d links, %d actions\n",
		len(state.Objects), len(state.Links), len(state.Actions))

	incrs, err := sess.Drain()
	if err != nil {
		log.Fatalf("drain: %v", err)
	}
	for _, incr := range incrs {
		fmt.Printf("increment: txn %s, LSN [%d, %d], %d writes\n",
			incr.Txn, incr.FromLSN, incr.ToLSN, len(incr.Writes))
		for _, w := range incr.Writes {
			state.Apply(w)
		}
	}
	fmt.Printf("stitched: %d objects, %d links, %d actions\n",
		len(state.Objects), len(state.Links), len(state.Actions))
}
