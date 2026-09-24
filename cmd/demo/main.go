// Command demo 演示只追加日志的段索引与范围回放器，并逐条判定核心性质。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"ontology/event"
	"ontology/repair"
	"ontology/replay"
	"ontology/segment"
	"ontology/sparse"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func writeSeg(dir string, first, n uint64, payload string) string {
	p := filepath.Join(dir, fmt.Sprintf("%020d.seg", first))
	w, err := segment.Create(p, first)
	must(err)
	for i := uint64(0); i < n; i++ {
		must(w.Append(event.Event{Seq: first + i, Payload: []byte(payload)}))
	}
	must(w.Close())
	return p
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func replaySeqs(dir string, from, to uint64) ([]uint64, replay.Stats) {
	var seqs []uint64
	st, err := replay.Replay(dir, from, to, func(e event.Event) error {
		seqs = append(seqs, e.Seq)
		return nil
	})
	must(err)
	return seqs, st
}

func contiguousFrom(seqs []uint64, first uint64) bool {
	for i, s := range seqs {
		if s != first+uint64(i) {
			return false
		}
	}
	return true
}

func main() {
	dir, err := os.MkdirTemp("", "ontology-demo")
	must(err)
	const N = 128
	for k := uint64(0); k < 3; k++ { // 三段：[1000,1400) [1400,1800) [1800,2200)
		seg := writeSeg(dir, 1000+400*k, 400, "x")
		must(sparse.Build(seg, seg+".idx", N))
	}
	// 三种 from 位置
	s, st := replaySeqs(dir, 1128, 1131) // 恰等于锚点
	check("locate from==anchor", contiguousFrom(s, 1128) && len(s) == 4 && st.Skipped() == 0)
	s, st = replaySeqs(dir, 1130, 1131) // 两锚点之间
	check("locate from between anchors", contiguousFrom(s, 1130) && len(s) == 2 && st.Skipped() == 2)
	s, st = replaySeqs(dir, 0, 1001) // 小于首锚点：钳到最小序号
	check("locate from below first anchor", contiguousFrom(s, 1000) && len(s) == 2 && st.Skipped() == 0)
	// 跳过事件数严格小于锚点间隔（200 个随机 from）
	rng := rand.New(rand.NewSource(7))
	boundOK := true
	for i := 0; i < 200; i++ {
		from := 1000 + uint64(rng.Intn(1199))
		_, st := replaySeqs(dir, from, from+10)
		if st.Skipped() >= N {
			boundOK = false
		}
	}
	check("skipped < anchor interval (200 random)", boundOK)
	// 删索引重建逐字节相同
	seg1 := filepath.Join(dir, fmt.Sprintf("%020d.seg", 1000))
	orig, err := os.ReadFile(seg1 + ".idx")
	must(err)
	must(os.Remove(seg1 + ".idx"))
	must(repair.RebuildIndex(seg1, N))
	rebuilt, err := os.ReadFile(seg1 + ".idx")
	must(err)
	check("index rebuild byte-identical", string(orig) == string(rebuilt))
	// 跨三段回放不重不漏
	s, _ = replaySeqs(dir, 1350, 1850)
	check("replay across 3 segments", len(s) == 501 && contiguousFrom(s, 1350))
	// 四类截断分类各一例
	tdir, err := os.MkdirTemp("", "ontology-demo-trunc")
	must(err)
	tseg := writeSeg(tdir, 0, 500, "abcd") // 记录定长 24B
	data, err := os.ReadFile(tseg)
	must(err)
	cuts := []struct {
		name string
		cut  int64
		want error
	}{
		{"truncate header", 10, segment.ErrHeaderIncomplete},
		{"truncate length prefix", 25, segment.ErrLengthIncomplete},
		{"truncate event body", 30, segment.ErrBodyIncomplete},
		{"truncate crc", int64(len(data)) - 2, segment.ErrCRCMismatch},
	}
	for _, c := range cuts {
		p := filepath.Join(tdir, "cut.seg")
		must(os.WriteFile(p, data[:c.cut], 0o644))
		check("classify "+c.name, errors.Is(repair.Classify(p), c.want))
	}
	// repair 改正段头条数
	must(os.WriteFile(tseg, data[:24+251*24+7], 0o644))
	rep, err := repair.RepairSegment(tseg)
	must(err)
	check("repair fixes header count", rep.Before == 500 && rep.After == 251 && repair.Classify(tseg) == nil)
	// 索引偏移被篡改：回退全段扫描仍正确且有标注
	idx, err := sparse.Load(seg1 + ".idx")
	must(err)
	idx.Anchors[2].Offset += 3
	must(os.WriteFile(seg1+".idx", idx.Encode(), 0o644))
	s, st = replaySeqs(dir, 1300, 1310)
	check("corrupt index falls back", st.IndexInvalid() && contiguousFrom(s, 1300) && len(s) == 11)
	// 序号缺口检出
	gdir, err := os.MkdirTemp("", "ontology-demo-gap")
	must(err)
	writeSeg(gdir, 0, 100, "x")
	writeSeg(gdir, 101, 100, "x") // 缺序号 100
	var ge *repair.GapError
	gapErr := repair.CheckContinuity(gdir)
	check("sequence gap detected", errors.Is(gapErr, repair.ErrSeqGap) &&
		errors.As(gapErr, &ge) && ge.Lo == 100 && ge.Hi == 100)
	// 汇总
	for _, d := range []string{dir, tdir, gdir} {
		os.RemoveAll(d)
	}
	if failures > 0 {
		fmt.Printf("TOTAL FAIL (%d failures)\n", failures)
		os.Exit(1)
	} else {
		fmt.Println("TOTAL OK")
	}
}
