// Command demo 演示增量聚合视图维护器的各项判定。
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

var failures int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	checkChange()
	checkAgg()
	checkJournal()
	fmt.Printf("SUMMARY %d failure(s)\n", failures)
	if failures > 0 {
		panic("demo failed")
	}
}

// checkJournal 判定 journal 包：逐字节截断的四类分类各举一例。
func checkJournal() {
	dir, err := os.MkdirTemp("", "demo-jrn")
	if err != nil {
		check("journal.trunc", false, err.Error())
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "d.jrn")
	w, err := journal.Create(path)
	if err != nil {
		check("journal.trunc", false, err.Error())
		return
	}
	for i := 0; i < 200; i++ {
		c := change.Change{Version: uint64(i + 1), Op: change.Insert,
			HasGroup: true, Group: fmt.Sprintf("g%05d", i),
			ID: fmt.Sprintf("id%06d", i), Value: float64(i)}
		if err := w.Log(c); err != nil {
			check("journal.trunc", false, err.Error())
			return
		}
	}
	w.Close()
	data, _ := os.ReadFile(path)
	classAt := func(cut int) journal.Class {
		p := filepath.Join(dir, "cut.jrn")
		os.WriteFile(p, data[:cut], 0o644)
		_, _, err := journal.Replay(p)
		var je *journal.Error
		if errors.As(err, &je) {
			return je.Class
		}
		return -1
	}
	ok := classAt(3) == journal.ClassHeaderShort &&
		classAt(9) == journal.ClassLenShort &&
		classAt(22) == journal.ClassBodyShort &&
		classAt(48) == journal.ClassCRC
	check("journal.trunc", ok, "截断分类:头/长度前缀/记录体/CRC 各一例")
}

// checkAgg 判定 agg 包：撤回能力声明符合推导，且精确求和增量与重算位级一致。
func checkAgg() {
	declOK := true
	for _, k := range agg.All() {
		if !k.InsertIncremental() {
			declOK = false
		}
		wantDel := k == agg.Count || k == agg.Sum
		if k.DeleteIncremental() != wantDel || k.NeedsMembersOnDelete() == wantDel {
			declOK = false
		}
	}
	var inc, fresh agg.Summer
	for _, v := range []float64{1e16, 1, 3} {
		inc.Add(v)
	}
	inc.Sub(1)
	fresh.Add(1e16)
	fresh.Add(3)
	bitOK := math.Float64bits(inc.Value()) == math.Float64bits(fresh.Value())
	check("agg.decl", declOK, "五种聚合器撤回声明符合推导")
	check("agg.sum.bits", bitOK, "精确求和:增量减与全量重算位级相等")
}

// checkChange 判定 change 包：编解码往返一致（含空串分组键与缺失分组键）。
func checkChange() {
	cases := []change.Change{
		{Version: 1, Op: change.Insert, HasGroup: true, Group: "g1", ID: "a", Value: 3.5},
		{Version: 2, Op: change.Insert, HasGroup: true, Group: "", ID: "b", Value: -0.0},
		{Version: 3, Op: change.Delete, ID: "a"},
		{Version: 4, Op: change.Update, HasGroup: true, Group: "g2", ID: "b", Value: 1e300},
	}
	ok := true
	for _, c := range cases {
		got, err := change.Decode(c.Encode())
		if err != nil || !got.Equal(c) {
			ok = false
		}
	}
	check("change.codec", ok, "编解码往返一致(含空串/缺失分组键)")
}
