// demo：逐项验证过滤索引增量维护的正确性，全部 OK 才以 0 退出。
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/pindex"
	"ontology/ptab"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("%s: OK\n", name)
	} else {
		fmt.Printf("%s: FAIL\n", name)
		failed = true
	}
}

func main() {
	// pindex：谓词边界 + 索引增删查升序
	predOK := pindex.Hit(50) && pindex.Hit(80) && !pindex.Hit(49) && !pindex.Hit(0)
	ix := pindex.New()
	ix.Add(10, 3)
	ix.Add(10, 1)
	ix.Add(10, 2)
	ix.Add(20, 9)
	ix.Remove(10, 1)
	idxOK := reflect.DeepEqual(ix.Lookup(10), []int{2, 3}) &&
		reflect.DeepEqual(ix.Keys(), []int{10, 20}) && len(ix.Lookup(99)) == 0
	check("pindex predicate & sorted add/remove/lookup", predOK && idxOK)

	// ptab：命中迁移（加入/移除/迁移）与校验失败不留痕
	pix := pindex.New()
	tab := ptab.New(pix)
	_ = tab.Put(1, 10, 80) // 无→命中：加入
	_ = tab.Put(1, 10, 49) // 命中→不中：移除
	_ = tab.Put(1, 20, 50) // 不中→命中：加入（50 左闭）
	_ = tab.Put(1, 30, 60) // 命中→命中、Key 变：迁移
	migOK := reflect.DeepEqual(pix.Lookup(30), []int{1}) &&
		len(pix.Lookup(10)) == 0 && len(pix.Lookup(20)) == 0 && len(tab.Snapshot()) == 1
	err1, err2, err3 := tab.Put(-1, 1, 1), tab.Put(2, 1, 101), tab.Delete(99)
	errOK := errors.Is(err1, ptab.ErrBadID) && errors.Is(err2, ptab.ErrBadScore) &&
		errors.Is(err3, ptab.ErrNotFound) && err1 != err2 && err2 != err3 && err1 != err3 &&
		len(tab.Snapshot()) == 1 // 三次被拒后状态不变
	check("ptab hit-migration & rejected ops leave no trace", migOK && errOK)

	// api：第三节六步，逐步核对索引内容（含第 4、5 步判定）
	a := api.New()
	ops := [][3]int{{1, 10, 80}, {2, 10, 30}, {3, 20, 60}, {1, 10, 49}, {2, 10, 50}, {3, 10, 70}}
	want10 := [][]int{{1}, {1}, {1}, {}, {2}, {2, 3}}
	want20 := [][]int{{}, {}, {3}, {3}, {3}, {}}
	traceOK, step4OK, step5OK := true, false, false
	for i, op := range ops {
		if a.Put(op[0], op[1], op[2]) != nil ||
			!reflect.DeepEqual(a.Lookup(10), want10[i]) ||
			!reflect.DeepEqual(a.Lookup(20), want20[i]) {
			traceOK = false
			break
		}
		if i == 3 {
			step4OK = len(a.Lookup(10)) == 0 // 命中翻不命中：旧条目已移除
		}
		if i == 4 {
			step5OK = reflect.DeepEqual(a.Lookup(10), []int{2}) // Score=50 左闭命中
		}
	}
	check("six-step trace: per-step index content", traceOK)
	check("step4 hit->miss removal & step5 score==50 hit", step4OK && step5OK)
	check("final Lookup(10)==[2 3]", reflect.DeepEqual(a.Lookup(10), []int{2, 3}))

	// SelfCheck：当前状态与全量重建一致 + 内置序列核验四条不变量
	check("SelfCheck: index equals full rebuild", a.SelfCheck() == nil && api.New().SelfCheck() == nil)

	// 大 m 下 Lookup 检查行数不随 m 增长：计数器是非导出字段，公开接口读不到，
	// 只能由 pindex 同包测试钉住，这里以子进程跑该测试拿判定。
	out, err := exec.Command("go", "test", "-count=1", "-run", "TestLookupExaminedRowsBound", "./pindex/").CombinedOutput()
	check("large-m lookup examined-rows independent of m", err == nil && strings.Contains(string(out), "ok"))

	// 并发只读：N 个 goroutine 同时 Lookup/Snapshot，结果逐字段相同
	for i := 0; i < 50; i++ {
		_ = a.Put(1000+i, i%7, i%101)
	}
	wantIDs, wantSnap := a.Lookup(3), a.Snapshot()
	start := make(chan struct{})
	var wg sync.WaitGroup
	concOK := true
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < 50; r++ {
				if !reflect.DeepEqual(a.Lookup(3), wantIDs) || !reflect.DeepEqual(a.Snapshot(), wantSnap) {
					concOK = false
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent read-only Lookup/Snapshot identical", concOK)

	if failed {
		os.Exit(1)
	}
}
