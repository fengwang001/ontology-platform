package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"ontology/audit"
	"ontology/change"
	"ontology/journal"
	"ontology/view"
)

// 10 万条插入 + 5000 次删除，其中仅 3 次删到当前 Min。
func checkRecomputeCounts() (bool, string) {
	v := view.NewMem()
	version := uint64(0)
	next := func() uint64 { version++; return version }
	for g := 0; g < 100; g++ {
		name := fmt.Sprintf("g%03d", g)
		for i := 0; i < 1000; i++ {
			v.Apply(change.Change{Version: next(), Op: change.OpInsert,
				ID: uint64(g*1000 + i + 1), Group: change.Str(name),
				Value: float64(i)})
		}
	}
	del := func(g, i int) {
		v.Apply(change.Change{Version: next(), Op: change.OpDelete,
			ID: uint64(g*1000 + i + 1)})
	}
	for g := 0; g < 99; g++ {
		for i := 100; i < 150; i++ {
			del(g, i)
		}
	}
	for i := 100; i < 147; i++ {
		del(99, i)
	}
	del(0, 0)
	del(0, 1)
	del(0, 2)
	st := v.Stats()
	min, sum := st.PerAgg["min"], st.PerAgg["sum"]
	ok := min.Recomputes == 3 && sum.Recomputes == 0 &&
		st.PerAgg["count"].Recomputes == 0 && min.MaxAccess <= 1000
	return ok, fmt.Sprintf("min=%d sum=%d count=%d minMaxAccess=%d",
		min.Recomputes, sum.Recomputes, st.PerAgg["count"].Recomputes,
		min.MaxAccess)
}

// 随机变更流：增量视图与 audit 全量重算逐组位级相等。
func checkAuditEqual() (bool, string) {
	rng := rand.New(rand.NewSource(42))
	v := view.NewMem()
	var changes []change.Change
	var live []uint64
	nextID, version := uint64(0), uint64(0)
	for i := 0; i < 5000; i++ {
		version++
		var c change.Change
		switch r := rng.Intn(10); {
		case r < 5 || len(live) == 0:
			nextID++
			c = change.Change{Version: version, Op: change.OpInsert,
				ID: nextID, Group: change.Str(fmt.Sprintf("g%d", rng.Intn(20))),
				Value: rng.NormFloat64() * 100}
			live = append(live, nextID)
		case r < 8:
			j := rng.Intn(len(live))
			c = change.Change{Version: version, Op: change.OpDelete, ID: live[j]}
			live = append(live[:j], live[j+1:]...)
		default:
			c = change.Change{Version: version, Op: change.OpUpdate,
				ID: live[rng.Intn(len(live))],
				Group: change.Str(fmt.Sprintf("g%d", rng.Intn(20))),
				Value: rng.NormFloat64() * 100}
		}
		if err := v.Apply(c); err != nil {
			return false, "apply: " + err.Error()
		}
		changes = append(changes, c)
	}
	if err := audit.Check(v, changes); err != nil {
		return false, err.Error()
	}
	return true, fmt.Sprintf("changes=%d groups=%d",
		len(changes), len(v.Groups()))
}

// 删空组后：Groups() 不出现该组，Query 返回不存在。
func checkGroupGone() (bool, string) {
	v := view.NewMem()
	v.Apply(change.Change{Version: 1, Op: change.OpInsert, ID: 1,
		Group: change.Str("solo"), Value: 5})
	v.Apply(change.Change{Version: 2, Op: change.OpDelete, ID: 1})
	_, ok := v.Query("solo")
	gone := !ok && len(v.Groups()) == 0
	return gone, fmt.Sprintf("query_ok=%v groups=%d", ok, len(v.Groups()))
}

// 对 200 条变更的日志逐类各取一个截断点，验证四类可判定分类。
func checkTruncation(dir string) (bool, string) {
	path := filepath.Join(dir, "trunc.log")
	w, err := journal.Create(path)
	if err != nil {
		return false, err.Error()
	}
	for i := 0; i < 200; i++ {
		w.Append(change.Change{Version: uint64(i + 1), Op: change.OpInsert,
			ID: uint64(i + 1), Group: change.Str(fmt.Sprintf("g%04d", i%10)),
			Value: float64(i)})
	}
	w.Close()
	data, _ := os.ReadFile(path)
	cases := []struct {
		kind journal.Kind
		at   int64
	}{
		{journal.KindHeader, 3},
		{journal.KindLength, journal.HeaderLen + 1},
		{journal.KindBody, journal.HeaderLen + 6},
		{journal.KindCRC, int64(len(data)) - 1},
	}
	ok := true
	detail := ""
	for _, tc := range cases {
		tp := filepath.Join(dir, fmt.Sprintf("cut%d.log", tc.at))
		os.WriteFile(tp, data[:tc.at], 0o600)
		got, _, err := journal.Replay(tp)
		re, isRE := err.(*journal.ReplayError)
		if !isRE || re.Kind != tc.kind {
			ok = false
		}
		detail += fmt.Sprintf("[%s@%d→%d条] ", tc.kind, tc.at, len(got))
	}
	return ok, detail
}
