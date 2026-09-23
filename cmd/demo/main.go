// Command demo 逐条演示并判定 LSM 合并读取器的核心语义，不读参数不联网。
package main

import (
	"encoding/binary"
	"fmt"
	"ontology/compact"
	"ontology/iter"
	"ontology/level"
	"ontology/segment"
	"ontology/verify"
	"os"
	"path/filepath"
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

func main() {
	// 1. 同键多版本：层小者胜，同层（L0）序号大者胜。
	s := openStore()
	ingest(s, 1, [3]string{"k", "L1old", ""})
	ingest(s, 0, [3]string{"k", "L0v1", ""})
	ingest(s, 0, [3]string{"k", "L0v2", ""})
	v, st := get(s, "k")
	check("multi-version winner (lower level, higher seq)", v == "L0v2" && st == segment.StateValue)
	s.Close()

	// 2. 删除标记 ≠ 从未写过，且不复活旧值。
	s = openStore()
	ingest(s, 1, [3]string{"gone", "L1old", ""})
	ingest(s, 0, [3]string{"gone", "", "del"})
	_, stDel := get(s, "gone")
	_, stAbs := get(s, "never")
	check("tombstone vs never-written distinct", stDel == segment.StateDeleted && stAbs == segment.StateAbsent)
	s.Close()

	// 3+4. 删除标记丢弃条件：部分合并保留，覆盖全层丢弃。
	setup := func() *level.Store {
		s := openStore()
		ingest(s, 2, [3]string{"k", "L2old", ""})
		ingest(s, 1, [3]string{"a", "1", ""})
		ingest(s, 0, [3]string{"k", "", "del"}, [3]string{"z", "26", ""})
		return s
	}
	s = setup()
	m1, err := compact.Compact(s, []int{0, 1}, 1)
	_, st = get(s, "k")
	check("partial compact keeps tombstone", err == nil && st == segment.StateDeleted && m1.Count == 3)
	s.Close()
	s = setup()
	m2, err := compact.Compact(s, []int{0, 1, 2}, 2)
	_, st = get(s, "k")
	check("full compact drops tombstone", err == nil && st == segment.StateAbsent && m2.Count == 2)
	s.Close()

	// 5. 点查比较数上界 4*ceil(log2(n))。
	p := writeSegN(100000)
	r, err := segment.Open(p)
	if err != nil {
		panic(err)
	}
	_, _, err = r.Get([]byte("key00050000"))
	cmp := r.Compares()
	r.Close()
	check(fmt.Sprintf("point lookup compares %d <= 68", cmp), err == nil && cmp <= 68)

	// 6. 归并比较数上界 4*M*ceil(log2(S+1))。
	var srcs []*iter.Source
	for i := 0; i < 5; i++ {
		r, err := segment.Open(writeSegN(400, i))
		if err != nil {
			panic(err)
		}
		defer r.Close()
		srcs = append(srcs, iter.NewSource(r, i, 0))
	}
	mg := iter.NewMerger(srcs, false)
	m := 0
	for _, ok := mg.Next(); ok; _, ok = mg.Next() {
		m++
	}
	bound := int64(4 * m * 3)
	check(fmt.Sprintf("merge compares %d <= %d, resident %d <= 5", mg.Compares(), bound, mg.MaxResident()),
		m == 2000 && mg.Compares() <= bound && mg.MaxResident() <= 5)

	// 7. 四类截断分类各一例。
	seg := writeSegN(500)
	fi, _ := os.Stat(seg)
	full, _ := os.ReadFile(seg)
	ioff := int(binary.LittleEndian.Uint64(full[12:20]))
	iend := ioff + int(binary.LittleEndian.Uint64(full[20:28]))
	ok7 := truncClass(seg, full, 10, segment.ErrHeaderIncomplete) &&
		truncClass(seg, full, 36+100, segment.ErrEntryTruncated) &&
		truncClass(seg, full, ioff+10, segment.ErrIndexIncomplete) &&
		truncClass(seg, full, iend+2, segment.ErrCRCMismatch)
	os.WriteFile(seg, full, 0o644)
	check(fmt.Sprintf("truncation classes x4 (size=%d)", fi.Size()), ok7)

	// 8. 合并中途崩溃：半截新段被清理，读取与合并前一致。
	s = openStore()
	ingest(s, 1, [3]string{"b", "2", ""})
	ingest(s, 0, [3]string{"a", "1", ""})
	vb, _ := get(s, "a")
	m3, _ := compact.Compact(s, []int{0, 1}, 1)
	bs, _ := os.ReadFile(m3.Path)
	s.Close()
	s = openStore()
	ingest(s, 1, [3]string{"b", "2", ""})
	ingest(s, 0, [3]string{"a", "1", ""})
	dir := filepath.Dir(s.Snapshot()[0][0].Path)
	os.WriteFile(filepath.Join(dir, "L1-000099.seg.tmp"), bs[:len(bs)/2], 0o644)
	s.Close()
	s, err = level.OpenStore(dir)
	va, sta := get(s, "a")
	check("crash mid-compact recovers", err == nil && va == vb && sta == segment.StateValue)
	s.Close()

	// 9. 层级不变量：L1 两段重叠被检出。
	s = openStore()
	ingest(s, 1, [3]string{"a", "1", ""}, [3]string{"m", "13", ""})
	ingest(s, 1, [3]string{"k", "11", ""}, [3]string{"z", "26", ""})
	ov := verify.CheckLevels(s.Snapshot())
	check(fmt.Sprintf("L1 overlap detected %v", ov != nil && len(ov) == 1), len(ov) == 1 && ov[0].Lo == "k" && ov[0].Hi == "m")
	s.Close()

	fmt.Printf("TOTAL %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
