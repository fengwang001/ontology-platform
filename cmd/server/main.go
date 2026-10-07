// server 演示对象类型版本迁移与运行中实例双写回填子系统的完整流程:
// 声明迁移 -> 双版本读写 -> 异步回填与并发流量交错 -> 追加对应关系 -> 全部回填。
package main

import (
	"context"
	"fmt"
	"time"

	"ontology/ontology/backfill"
	"ontology/ontology/migration"
	"ontology/ontology/router"
)

func main() {
	svc := router.NewService(migration.NewDeclaration())

	fmt.Println("== 1. 声明迁移: name 保留, nick 废弃, age 新增默认 18 ==")
	must(svc.AmendDeclaration([]migration.Mapping{
		{Property: "name", Action: migration.ActionRetain},
		{Property: "nick", Action: migration.ActionDeprecate},
		{Property: "age", Action: migration.ActionAddDefault, Default: 18},
	}))

	fmt.Println("== 2. 创建存量实例(旧版本结构) ==")
	must(svc.Create("alice", router.VersionOld, map[string]any{"name": "alice", "nick": "al"}))
	must(svc.Create("bob", router.VersionOld, map[string]any{"name": "bob", "nick": "b"}))

	fmt.Println("== 3. 回填开始前, 新旧版本读同一实例 ==")
	printRead(svc, "alice", router.VersionOld)
	printRead(svc, "alice", router.VersionNew) // 即时现算, 不等待回填

	fmt.Println("== 4. 旧版本写入未回填实例(自动转换为新版本写入) ==")
	must(svc.Write("bob", router.VersionOld, map[string]any{"name": "bob-the-builder"}))
	printRead(svc, "bob", router.VersionNew)

	fmt.Println("== 5. 异步回填与并发读写交错 ==")
	must(svc.Create("carol", router.VersionOld, map[string]any{"name": "carol"}))
	must(svc.Create("dave", router.VersionOld, map[string]any{"name": "dave"}))
	worker := backfill.NewWorker(svc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { worker.Run(ctx, 10*time.Millisecond); close(done) }()
	for i := 0; i < 5; i++ {
		must(svc.Write("carol", router.VersionNew, map[string]any{"name": fmt.Sprintf("carol-%d", i)}))
	}
	<-done
	fmt.Println("回填完成, 待回填实例:", svc.PendingIDs())

	fmt.Println("== 6. 全部实例的新版本视图 ==")
	for _, id := range svc.InstanceIDs() {
		printRead(svc, id, router.VersionNew)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func printRead(svc *router.Service, id string, v router.Version) {
	view, err := svc.Read(id, v)
	if err != nil {
		fmt.Printf("  read(%s, %s) -> %v\n", id, v, err)
		return
	}
	fmt.Printf("  read(%s, %s) -> %v\n", id, v, view)
}
