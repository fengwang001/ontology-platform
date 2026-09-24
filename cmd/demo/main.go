// Command demo 演示批量导入的幂等与断点续传能力，逐条打印判定结果。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ontology/batch"
	"ontology/importer"
	"ontology/progress"
	"ontology/reconcile"
	"ontology/store"
)

func makeBatch(id string, n int) *batch.Batch {
	b := &batch.Batch{ID: id}
	for i := 0; i < n; i++ {
		b.Records = append(b.Records, batch.Record{
			Key:   fmt.Sprintf("k%06d", i),
			Value: []byte(fmt.Sprintf("v%d", i)),
		})
	}
	return b
}

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
	}
	fmt.Printf("%s %s %s\n", verdict, name, detail)
	if ok {
		passed++
	}
}

func main() {
	dir, err := os.MkdirTemp("", "ontology-demo")
	if err != nil {
		fmt.Println("FAIL tempdir", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	b := &batch.Batch{ID: "b0", Records: []batch.Record{{Key: "k", Value: []byte("v")}}}
	rt, err := batch.Decode(b.Encode())
	check("batch-codec", err == nil && rt.ID == b.ID && bytes.Equal(rt.Records[0].Value, []byte("v")),
		"清单编码解码往返一致")
	dup := &batch.Batch{ID: "b1", Records: []batch.Record{{Key: "x"}, {Key: "x"}}}
	var derr *batch.DupKeyError
	check("batch-dupkey", errors.As(dup.Validate(), &derr) && derr.First == 0 && derr.Second == 1,
		"批内重复键整批拒绝并报告两处位置")
	st := store.New()
	ex1, _ := st.Put("k", []byte("v"))
	ex2, _ := st.Put("k", []byte("v"))
	_, cerr := st.Put("k", []byte("other"))
	var conf *store.ConflictError
	check("store-idempotent", !ex1 && ex2 && errors.As(cerr, &conf),
		"同键同值幂等、同键异值拒绝")
	st2 := store.New()
	st2.FailOnPut(1)
	_, ferr := st2.Put("z", []byte("w"))
	check("store-failinject", errors.Is(ferr, store.ErrInjected) && !st.Has("z"),
		"第 k 次写入可注入失败且不落数据")
	pf, _, _ := progress.Open(filepath.Join(dir, "d.progress"), "d")
	for i := 0; i < 3; i++ {
		pf.Flush(batch.Interval{Start: 2 * i, End: 2*i + 2})
	}
	raw, _ := os.ReadFile(pf.Path())
	seen := map[progress.Class]bool{}
	for cut := 1; cut < len(raw); cut++ {
		_, class := progress.Recover(raw[:cut])
		seen[class] = true
	}
	check("progress-truncate", seen[progress.ClassHeaderIncomplete] &&
		seen[progress.ClassIntervalIncomplete] && seen[progress.ClassCRCMismatch],
		"逐字节截断可分类为头部不完整/区间不完整/CRC 不匹配")

	big := makeBatch("big", 100000)
	refStore := store.New()
	importer.New(refStore, filepath.Join(dir, "ref"), 1000).Import(big)
	stA := store.New()
	stA.FailOnPut(70000)
	dirA := filepath.Join(dir, "a")
	importer.New(stA, dirA, 1000).Import(big)
	imA := importer.New(stA, dirA, 1000)
	imA.Import(big)
	check("resume-rewrite", imA.Rewrites() <= 1000 && bytes.Equal(stA.Snapshot(), refStore.Snapshot()),
		fmt.Sprintf("第 7 万条中断后续传重写 %d 条(<=N=1000)且终态逐字节相同", imA.Rewrites()))

	res, _ := importer.New(stA, dirA, 1000).Import(big)
	check("reimport-identical", res.AlreadyDone && bytes.Equal(stA.Snapshot(), refStore.Snapshot()),
		"重复导入返回已完成且存储逐字节不变")

	dirB := filepath.Join(dir, "b")
	stB1 := store.New()
	stB1.FailOnPut(50001)
	importer.New(stB1, dirB, 1000).Import(big)
	stB2 := store.New()
	for i := 0; i < 30000; i++ {
		stB2.Put(big.Records[i].Key, big.Records[i].Value)
	}
	imB := importer.New(stB2, dirB, 1000)
	_, errB := imB.Import(big)
	check("store-truth-wins", errB == nil && bytes.Equal(stB2.Snapshot(), refStore.Snapshot()),
		"进度声称 50000 而存储仅 30000 时以存储为准续传")

	dirC := filepath.Join(dir, "c")
	now := time.Now()
	imC1 := importer.New(store.New(), dirC, 10)
	imC1.Now = func() time.Time { return now }
	imC1.LockTTL = time.Minute
	imC1.Acquire("L") // 模拟崩溃的在途持有者，锁不释放
	imC2 := importer.New(store.New(), dirC, 10)
	imC2.Now = func() time.Time { return now.Add(30 * time.Second) }
	_, errActive := imC2.Import(makeBatch("L", 5))
	imC2.Now = func() time.Time { return now.Add(2 * time.Minute) }
	_, errExpired := imC2.Import(makeBatch("L", 5))
	check("lock-takeover", errors.Is(errActive, importer.ErrInProgress) && errExpired == nil,
		"持有者在途时同批次被拒，过期后安全接管")

	seg := makeBatch("seg", 10)
	stS := store.New()
	stS.FailOnPut(5)
	importer.New(stS, filepath.Join(dir, "s"), 3).Import(seg)
	stS.Put("extra", []byte("x"))
	rep := reconcile.Reconcile(stS, seg, false)
	threeSeg := len(rep.Written) == 1 && rep.Written[0] == (batch.Interval{Start: 0, End: 4}) &&
		len(rep.Gaps) == 1 && rep.Gaps[0] == (batch.Interval{Start: 4, End: 10}) &&
		len(rep.Extra) == 1 && rep.Extra[0] == "extra"
	check("reconcile-segments", threeSeg,
		fmt.Sprintf("对账三段结论: 已写入%v 缺口%v 多余%v", rep.Written, rep.Gaps, rep.Extra))
	check("inflight-not-committed", rep.Committed == 0 && rep.InFlight == 4,
		fmt.Sprintf("在途 %d 条不计入已提交(%d)", rep.InFlight, rep.Committed))
	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
