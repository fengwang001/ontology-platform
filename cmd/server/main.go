// 演示程序：构造服务，执行写入、删除、复活与双时态回溯查询，
// 并演示 WAL 落盘与重放还原。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"ontology/service"
	"ontology/store"
)

func show(svc *service.Service, label string, sysQ, bizQ int64) {
	r := svc.Query("Employee", "alice", sysQ, bizQ)
	if r.Version != nil {
		fmt.Printf("%-28s sysQ=%-4d bizQ=%-3d -> %-18s v%d (sys=%d biz=%d) payload=%q\n",
			label, sysQ, bizQ, r.Status, r.Version.Seq, r.Version.Sys, r.Version.BizStart, r.Version.Payload)
	} else {
		fmt.Printf("%-28s sysQ=%-4d bizQ=%-3d -> %s\n", label, sysQ, bizQ, r.Status)
	}
}

func main() {
	dir, err := os.MkdirTemp("", "ontology-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	wal, err := store.NewFileWAL(filepath.Join(dir, "wal.jsonl"))
	if err != nil {
		log.Fatal(err)
	}
	st := store.New(store.RealClock{}, store.WithWAL(wal))
	svc := service.New(st)

	// 写入 -> 删除 -> 复活。
	must := func(_ store.Version, e *service.Error) {
		if e != nil {
			log.Fatal(e)
		}
	}
	v1, e1 := svc.Write("Employee", "alice", "v0", 100, `{"level":7}`)
	must(v1, e1)
	v2, e2 := svc.Delete("Employee", "alice", "v1", 200)
	must(v2, e2)
	v3, e3 := svc.Write("Employee", "alice", "v2", 300, `{"level":9}`) // 复活
	must(v3, e3)
	fmt.Printf("已提交: v1(sys=%d,biz=100) v2=删除(sys=%d,biz=200) v3=复活(sys=%d,biz=300)\n\n",
		v1.Sys, v2.Sys, v3.Sys)

	show(svc, "删除前区间", v3.Sys, 150)
	show(svc, "删除中区间", v3.Sys, 250)
	show(svc, "复活后区间", v3.Sys, 350)
	show(svc, "回溯到删除前系统时间", v1.Sys, 350)
	show(svc, "业务时间早于任何记录", v3.Sys, 50)
	show(svc, "系统时间早于首次提交", v1.Sys-1, 150)

	// 乐观并发冲突演示。
	if _, err := svc.Write("Employee", "alice", "v2", 400, "x"); err != nil {
		fmt.Printf("\n过期凭证 v2 写入被拒绝: %v\n", err)
	}

	// WAL 重放还原。
	if err := wal.Close(); err != nil {
		log.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "wal.jsonl"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nWAL 内容:\n%s", data)
}
