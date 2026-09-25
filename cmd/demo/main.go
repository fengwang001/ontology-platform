package main

import (
	"fmt"
	"os"

	"ontology/snapshot"
	"ontology/store"
)

var fails int

func judge(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
		return
	}
	fails++
	fmt.Println("FAIL " + name)
}

func main() {
	st := store.New()
	for i := 0; i < 100; i++ {
		st.Put(fmt.Sprintf("k%03d", i), []byte(fmt.Sprintf("old-%d", i)))
	}
	snap := snapshot.Take(st)

	// 判定1：导出到第 50 键时改写第 80 键，第 80 键仍是快照旧值。
	keys, _ := snap.Keys(snapshot.Ascending)
	for i, k := range keys {
		if i == 50 {
			st.Put("k079", []byte("new-value"))
		}
		if i == 79 {
			v, _, _ := snap.Get(k)
			judge("改写键仍是快照旧值", string(v) == "old-79")
		} else if i != 79 {
			snap.Get(k)
		}
	}

	// 判定4：保留值个数不超过改写键数（这里只改写了 1 个键）。
	judge("保留值个数不超改写键数", snap.Retained() == 1 && snap.Retained() <= 1)
	// 判定5：读取次数精确等于键数（每个键只读一次，Keys 不计入）。
	judge("读取次数等于键数", snap.Reads() == 100)

	// 判定2：两个重叠快照都关闭后才释放。
	st2 := store.New()
	for i := 0; i < 10; i++ {
		st2.Put(fmt.Sprintf("k%03d", i), []byte("old"))
	}
	s1 := snapshot.Take(st2)
	for i := 0; i < 5; i++ {
		st2.Put(fmt.Sprintf("k%03d", i), []byte("new"))
	}
	s2 := snapshot.Take(st2)
	s2.Close()
	afterOne := s1.Retained() == 5
	s1.Close()
	afterBoth := st2.Retained(0) == 0
	judge("重叠快照都关闭后才释放", afterOne && afterBoth)

	snap.Close()

	fmt.Printf("TOTAL fails=%d\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
