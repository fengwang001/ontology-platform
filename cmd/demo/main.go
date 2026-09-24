// Command demo 演示只追加日志的段索引与范围回放器，并逐条判定验收项。
package main

import (
	"bytes"
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

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Printf("OK   %s\n", name)
	} else {
		failed++
		fmt.Printf("FAIL %s\n", name)
	}
}

func buildLog(dir string, maxEvents, every, total int) {
	w, err := segment.NewWriter(dir, maxEvents, every)
	ck(err)
	for i := 0; i < total; i++ {
		_, err := w.Append(bytes.Repeat([]byte{byte(i)}, 15))
		ck(err)
	}
	ck(w.Close())
}

func ck(err error) {
	if err != nil {
		panic(err)
	}
}

func seqsOK(evs []event.Event, from uint64) bool {
	for i, e := range evs {
		if e.Seq != from+uint64(i) {
			return false
		}
	}
	return true
}

func main() {
	root, err := os.MkdirTemp("", "segdemo")
	ck(err)
	defer os.RemoveAll(root)
	dir := func(name string) string { return filepath.Join(root, name) }

	// 1. 三种 from 位置定位正确
	d1 := dir("one")
	buildLog(d1, 500, 128, 1000)
	r1 := replay.New(d1)
	evA, repA, _ := r1.Replay(256, 260) // 恰为锚点
	evB, repB, _ := r1.Replay(300, 310) // 两锚点之间
	ck(os.Remove(filepath.Join(d1, segment.SegmentName(0))))
	evC, _, _ := replay.New(d1).Replay(100, 510) // 小于最小序号，钳位
	check("locate: on-anchor / between / below-min",
		repA.Skipped == 0 && seqsOK(evA, 256) &&
			repB.Skipped == 300-256 && seqsOK(evB, 300) &&
			len(evC) == 11 && seqsOK(evC, 500))

	// 2. 跳过事件数严格小于锚点间隔 N=128
	d2 := dir("two")
	buildLog(d2, 100000, 128, 100000)
	r2 := replay.New(d2)
	rng := rand.New(rand.NewSource(7))
	skipOK := true
	for i := 0; i < 200; i++ {
		from := uint64(rng.Intn(99000))
		evs, rep, err := r2.Replay(from, from+49)
		if err != nil || len(evs) != 50 || rep.Skipped >= 128 || !seqsOK(evs, from) {
			skipOK = false
		}
	}
	check("skip bound: skipped < N for 200 random from", skipOK)

	// 3. 删索引后重建与原索引逐字节相同
	seg0 := filepath.Join(d2, segment.SegmentName(0))
	idxPath := segment.IndexPath(seg0)
	before, _ := os.ReadFile(idxPath)
	ck(os.Remove(idxPath))
	ck(repair.RebuildIndex(seg0, 128))
	after, _ := os.ReadFile(idxPath)
	check("index rebuild byte-identical", bytes.Equal(before, after))

	// 4. 跨三段回放不重不漏
	d4 := dir("four")
	buildLog(d4, 4, 2, 12)
	evs4, _, err4 := replay.New(d4).Replay(0, 11)
	check("cross-3-segment replay exact", err4 == nil && len(evs4) == 12 && seqsOK(evs4, 0))

	// 5. 四类截断分类各一例
	d5 := dir("five")
	buildLog(d5, 500, 4, 500)
	seg5 := filepath.Join(d5, segment.SegmentName(0))
	data, _ := os.ReadFile(seg5)
	tmp := filepath.Join(d5, "tmp.osl")
	classOK := true
	for _, tc := range []struct {
		p    int
		want error
	}{
		{10, segment.ErrHeaderIncomplete},
		{26 + 2, segment.ErrLengthPrefixIncomplete},
		{26 + 10, segment.ErrBodyIncomplete},
		{26 + 29, segment.ErrCRCMismatch},
	} {
		ck(os.WriteFile(tmp, data[:tc.p], 0o644))
		if !errors.Is(repair.Classify(tmp), tc.want) {
			classOK = false
		}
	}
	check("four truncation classes", classOK)

	// 6. repair 改正段头条数
	ck(os.WriteFile(seg5, data[:26+300*31+10], 0o644))
	rep6, err6 := repair.RepairSegment(seg5, 4)
	check("repair fixes header count",
		err6 == nil && rep6.OldCount == 500 && rep6.NewCount == 300 && repair.Classify(seg5) == nil)

	// 7. 索引偏移被篡改：回退全段扫描仍正确且有标注
	d7 := dir("seven")
	buildLog(d7, 100, 4, 20)
	seg7 := filepath.Join(d7, segment.SegmentName(0))
	idx, _ := sparse.ReadFile(segment.IndexPath(seg7))
	idx.Anchors[2].Offset += 3
	ck(sparse.WriteFile(segment.IndexPath(seg7), idx))
	evs7, rep7, err7 := replay.New(d7).Replay(9, 14)
	check("tampered index falls back, correct+flagged",
		err7 == nil && rep7.IndexInvalid && seqsOK(evs7, 9) && len(evs7) == 6)

	// 8. 序号缺口被检出
	d8 := dir("eight")
	buildLog(d8, 4, 2, 12)
	seg81 := filepath.Join(d8, segment.SegmentName(1))
	f, _ := os.OpenFile(seg81, os.O_RDWR, 0)
	f.WriteAt(segment.EncodeHeader(segment.Header{FirstSeq: 5, Count: 4}), 0)
	f.Close()
	check("sequence gap detected", errors.Is(repair.CheckContinuity(d8), repair.ErrSeqGap))

	fmt.Printf("TOTAL %d/%d passed\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}
