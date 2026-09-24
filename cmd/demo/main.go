package main

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"

	"ontology/api"
	"ontology/entry"
)

var failed bool

func ok(name string, cond bool) {
	mark := "OK"
	if !cond {
		mark = "FAIL"
		failed = true
	}
	fmt.Println(name, mark)
}

func fmtLog(l []entry.Entry) string {
	s := "["
	for i, e := range l {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%d/%d/%s", e.Term, e.Index, e.Cmd)
	}
	return s + "]"
}

// checkedOf 经反射读取 raft 的非导出计数器（不经过任何导出接口）。
func checkedOf(s *api.Server) int64 {
	v := reflect.ValueOf(s).Elem()
	return v.FieldByName("c").Elem().FieldByName("r").Index(int(v.FieldByName("id").Int())).FieldByName("checked").Int()
}

func main() {
	sv := api.New()
	step := func(n, lead int, w1, w2, w3 string, ci int) {
		got := fmtLog(api.Log(sv[0])) + " " + fmtLog(api.Log(sv[1])) + " " + fmtLog(api.Log(sv[2]))
		ok(fmt.Sprintf("step%d %s CI=%d", n, got, api.Committed(sv[lead])),
			got == w1+" "+w2+" "+w3 && api.Committed(sv[lead]) == ci)
	}
	sv[0].SetTerm(1) // 步 1
	api.Append(sv[0], "a")
	api.Replicate(sv[0], sv[1], 0)
	api.CommitIndex(sv[0])
	step(1, 0, "[1/1/a]", "[1/1/a]", "[]", 1)
	api.Append(sv[0], "b") // 步 2：S1 崩溃，未 CommitIndex
	api.Replicate(sv[0], sv[1], 1)
	step(2, 0, "[1/1/a 1/2/b]", "[1/1/a 1/2/b]", "[]", 1)
	sv[2].SetTerm(2) // 步 3、4：S3 为领导，S1/S2 宕机
	api.Append(sv[2], "x")
	step(3, 2, "[1/1/a 1/2/b]", "[1/1/a 1/2/b]", "[2/1/x]", 0)
	api.Append(sv[2], "y")
	step(4, 2, "[1/1/a 1/2/b]", "[1/1/a 1/2/b]", "[2/1/x 2/2/y]", 0)
	sv[0].SetTerm(3) // 步 5：S1 归队为领导，S3 宕机
	api.Replicate(sv[0], sv[1], 1)
	api.Append(sv[0], "z")
	api.Replicate(sv[0], sv[1], 2)
	api.CommitIndex(sv[0])
	step(5, 0, "[1/1/a 1/2/b 3/3/z]", "[1/1/a 1/2/b 3/3/z]", "[2/1/x 2/2/y]", 3)
	api.Replicate(sv[0], sv[2], 0) // 步 6：S3 携 term2 日志归来，被整段截断重放
	step(6, 0, "[1/1/a 1/2/b 3/3/z]", "[1/1/a 1/2/b 3/3/z]", "[1/1/a 1/2/b 3/3/z]", 3)

	l0 := api.Log(sv[0]) // 老任期 (1,2,"b") 由当前任期 (3,3,"z") 的提交间接提交
	ok("indirect: (1,2,b) committed via (3,3,z)", api.Committed(sv[0]) == 3 && l0[1].Term == 1 && l0[1].Cmd == "b")

	fv := api.New() // 三类可判定错误互不相同，被拒后状态不变
	fv[0].SetTerm(1)
	api.Append(fv[0], "a")
	fv[1].SetTerm(2)
	api.Append(fv[1], "x")
	before := api.Log(fv[1])
	e1 := api.Append(fv[0], "")
	e2 := api.Replicate(fv[0], fv[1], -1)
	e3 := api.Replicate(fv[0], fv[1], 1)
	ok("faults: 3 distinct errors, state intact", e1 == api.ErrEmptyCmd && e2 == api.ErrPrevIndexRange &&
		e3 == api.ErrPrevTermMismatch && e1 != e2 && e2 != e3 && slices.Equal(api.Log(fv[1]), before))

	iv := api.New() // 大 m 下增量提交只查新增 1 条
	iv[0].SetTerm(1)
	m := 10000
	for i := 0; i < m; i++ {
		api.Append(iv[0], "x")
	}
	api.Replicate(iv[0], iv[1], 0)
	ci1 := api.CommitIndex(iv[0])
	api.Append(iv[0], "y")
	api.Replicate(iv[0], iv[1], m)
	ci2 := api.CommitIndex(iv[0])
	ok("incremental: m=10000 checked==1", ci1 == m && ci2 == m+1 && checkedOf(iv[0]) == 1)

	const n = 8 // 并发只读已收敛集群，快照逐条一致
	snaps := make([][]entry.Entry, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			snaps[g] = api.Log(sv[0])
			_ = api.SelfCheck()
		}(g)
	}
	close(start)
	wg.Wait()
	same := true
	for g := 1; g < n; g++ {
		same = same && slices.Equal(snaps[0], snaps[g])
	}
	ok("concurrent read consistent + selfcheck", same && api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
