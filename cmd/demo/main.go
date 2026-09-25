// Command demo 逐条打印 chunked 快照导出的自检结果。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/export"
	"ontology/snap"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func keys(es []snap.Entry) string {
	s := ""
	for _, e := range es {
		s += fmt.Sprintf("%s=%d ", e.Key, e.Val)
	}
	return s
}

func main() {
	// 第三节五步表：a..f，chunkSize=2，快照后 Put(d,99)。
	db, _ := api.New(2)
	for i, k := range []string{"a", "b", "c", "d", "e", "f"} {
		_ = db.Put(k, int64(i+1))
	}
	db.Snapshot()
	e1, c1, d1, _ := db.Next("")
	_ = db.Put("d", 99)
	e2, c2, d2, _ := db.Next(c1)
	e3, c3, d3, _ := db.Next(c2)
	_, _, _, err5 := db.Next(c3)
	check("五步: [a=1 b=2]->b,F [c=3 d=4]->d,F [e=5 f=6]->f,T 再Next=ErrFinished",
		keys(e1) == "a=1 b=2 " && c1 == "b" && !d1 && keys(e2) == "c=3 d=4 " && c2 == "d" && !d2 &&
			keys(e3) == "e=5 f=6 " && c3 == "f" && d3 && errors.Is(err5, api.ErrFinished))

	// 三块拼接 == 快照时刻全量（点时刻一致：d=4 而非 99）。
	all := append(append(append([]snap.Entry{}, e1...), e2...), e3...)
	check("拼接==全量且 d=4（快照时刻值）", keys(all) == "a=1 b=2 c=3 d=4 e=5 f=6 ")

	// 三类陷阱的具体错值（用错误实现现场重算）。
	s := snap.Capture(map[string]int64{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6})
	ge := 0 // 陷阱甲：>= 会多导位点键 b，续传后 b 出现两次
	for i := 0; i < s.Len(); i++ {
		if s.At(i).Key >= "b" {
			ge = i
			break
		}
	}
	liveD := db.View()[3].Val // 陷阱乙：活视图读到 d=99
	shortDone := 2 < 2        // 陷阱丙：第三块满块(2==2)，短块规则判 done=false
	check("陷阱: >=多导b="+s.At(ge).Key+" 活视图d=99 短块规则done=false",
		s.At(ge).Key == "b" && liveD == 99 && !shortDone)

	// 四类可判定错误互不相同 + 被拒后状态不变。
	_, errA := api.New(0)
	errB := db.Put("", 1)
	db2, _ := api.New(2)
	_, _, _, errC := db2.Next("")
	errs := []error{errA, errB, errC, api.ErrFinished}
	distinct := errs[0] == api.ErrBadChunk && errs[1] == api.ErrEmptyKey && errs[2] == api.ErrNoSnapshot &&
		errs[0] != errs[1] && errs[1] != errs[2] && errs[2] != errs[3] && errs[0] != errs[3]
	check("四类哨兵: ErrBadChunk/ErrEmptyKey/ErrNoSnapshot/ErrFinished 互不相同", distinct)
	check("被拒后状态不变且可继续用", db.Put("", 7) == api.ErrEmptyKey && len(db.View()) == 6 && db2.Put("x", 1) == nil)

	// 大 n 定位代价 + 并发导出点时刻一致。
	check("大 n 定位代价不随 n 线性增长", export.CheckProbes())
	cdb, _ := api.New(8)
	for i := 0; i < 64; i++ {
		_ = cdb.Put(fmt.Sprintf("k%03d", i), int64(i))
	}
	cdb.Snapshot()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < 64; i++ {
				_ = cdb.Put(fmt.Sprintf("w%02d-%03d", w, i), int64(i))
			}
		}(w)
	}
	var got []snap.Entry
	cur := ""
	close(start)
	for {
		en, nc, dn, err := cdb.Next(cur)
		if err != nil {
			break
		}
		got = append(got, en...)
		cur = nc
		if dn {
			break
		}
	}
	wg.Wait()
	ok := len(got) == 64
	for i, e := range got {
		if e.Key != fmt.Sprintf("k%03d", i) || e.Val != int64(i) {
			ok = false
		}
	}
	check("并发导出==快照时刻全量（无撕裂）", ok)
	check("SelfCheck 通过", cdb.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
