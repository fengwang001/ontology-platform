// Command demo 逐条演示并判定一致性导出器的各项性质。
package main

import (
	"fmt"
	"os"

	"ontology/snapshot"
	"ontology/store"
)

type check struct {
	name string
	fn   func() (bool, string)
}

func main() {
	checks := []check{
		{"两个重叠快照都关闭后才释放保留值", checkOverlapRelease},
		{"保留值个数不超改写键数", checkRetainedBound},
	}
	fails := 0
	for _, c := range checks {
		ok, detail := c.fn()
		status := "OK"
		if !ok {
			status = "FAIL"
			fails++
		}
		fmt.Printf("%s %s %s\n", status, c.name, detail)
	}
	fmt.Printf("TOTAL %d/%d 通过\n", len(checks)-fails, len(checks))
	if fails > 0 {
		os.Exit(1)
	}
}

func checkOverlapRelease() (bool, string) {
	st := store.New()
	st.Put("k", []byte("v1"))
	s1, s2 := snapshot.Open(st), snapshot.Open(st)
	st.Put("k", []byte("v2"))
	if st.Retained() != 1 {
		return false, "改写后保留值应为 1"
	}
	s1.Close()
	if st.Retained() != 1 {
		return false, "关闭一个后保留值不应释放"
	}
	s2.Close()
	if st.Retained() != 0 {
		return false, "两个都关闭后保留值应归零"
	}
	return true, ""
}

func checkRetainedBound() (bool, string) {
	st := store.New()
	for i := 0; i < 100000; i++ {
		st.Put(fmt.Sprintf("k%06d", i), []byte("old"))
	}
	snap := snapshot.Open(st)
	defer snap.Close()
	for i := 0; i < 100; i++ {
		st.Put(fmt.Sprintf("k%06d", i), []byte("new"))
	}
	if st.Retained() > 100 {
		return false, fmt.Sprintf("保留值 %d 超过改写键数 100", st.Retained())
	}
	return true, fmt.Sprintf("(10万键改写100, 保留=%d)", st.Retained())
}
