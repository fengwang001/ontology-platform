// Command demo 逐条演示增量聚合视图维护器的验收项，全部 OK 时退出码为 0。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

var passed, total int

func check(name string, ok bool) {
	total++
	status := "OK"
	if ok {
		passed++
	} else {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("change codec roundtrip", checkChangeCodec())
	check("agg retractability declarations", checkAggFamily())
	check("journal truncation classes", checkTruncation())
	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	if passed != total {
		panic("demo checks failed")
	}
}

func checkTruncation() bool {
	path := filepath.Join(os.TempDir(), "ontology-demo-trunc.log")
	defer os.Remove(path)
	j, err := journal.Create(path)
	if err != nil {
		return false
	}
	g := "g1"
	for i := 0; i < 10; i++ {
		c := change.Change{Version: uint64(i + 1), Op: change.OpInsert, ID: uint64(i + 1), Group: &g, Value: float64(i)}
		if err := j.Append(c.Encode()); err != nil {
			return false
		}
	}
	j.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	recSize := 30 + 8 // payload 30B + len(4) + crc(4)
	s := journal.HeaderSize + 3*recSize // 第 3 条记录起点
	cases := []struct {
		cut   int
		want  journal.Class
		wantN int
	}{
		{5, journal.ClassHeader, 0},
		{s + 2, journal.ClassLength, 3},
		{s + 10, journal.ClassRecord, 3},
		{s + 36, journal.ClassCRC, 3},
	}
	for _, tc := range cases {
		recs, cls, err := journal.ReplayBytes(data[:tc.cut])
		if cls != tc.want || err == nil || len(recs) != tc.wantN {
			return false
		}
	}
	return true
}

func checkAggFamily() bool {
	want := map[string]bool{"Count": false, "Sum": false, "Min": true, "Max": true, "DistinctCount": true}
	fam := agg.Family()
	if len(fam) != len(want) {
		return false
	}
	for _, a := range fam {
		need, ok := want[a.Name()]
		if !ok || a.NeedsMembers() != need {
			return false
		}
		a.Add(5)
		a.Add(9)
		// 删除 9：Count/Sum 恒可增量；Min(当前极值5) 可增量；Max(当前极值9)
		// 与 DistinctCount 必须要求重算。
		wantRetract := map[string]bool{"Count": true, "Sum": true, "Min": true, "Max": false, "DistinctCount": false}
		if a.Retract(9) != wantRetract[a.Name()] {
			return false
		}
	}
	return true
}

func checkChangeCodec() bool {
	g := "g1"
	cases := []change.Change{
		{Version: 7, Op: change.OpInsert, ID: 42, Group: &g, Value: -3.5},
		{Version: 8, Op: change.OpDelete, ID: 42},
		{Version: 9, Op: change.OpUpdate, ID: 1, Group: new(string), Value: 0},
	}
	for _, c := range cases {
		got, err := change.Decode(c.Encode())
		if err != nil || got.Version != c.Version || got.Op != c.Op || got.ID != c.ID || got.Value != c.Value {
			return false
		}
		if (got.Group == nil) != (c.Group == nil) || (got.Group != nil && *got.Group != *c.Group) {
			return false
		}
	}
	return true
}
