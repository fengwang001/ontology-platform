// Command demo 逐步判定「确定性重放 + LWW 压缩」的各项性质，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/lww"
	"ontology/seq"
)

var failed bool

func check(name string, ok bool) {
	mark := "OK"
	if !ok {
		mark = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	// seq：序号从 1 起连续，历史按 sn 升序。
	lg := seq.NewLog()
	lg.Append("k", 1, 10)
	lg.Append("k", 2, 20)
	lg.Append("x", 3, 30)
	h := lg.History("k")
	check("seq: sn 连续且历史按 sn 升序",
		h[0].Sn == 1 && h[1].Sn == 2 && lg.Len("k") == 2 && lg.Len("x") == 1)

	// lww：第三节七步推导，每步之后核对三键 winner（Ver, Val）。
	steps := []struct {
		key      string
		ver, val int64
	}{
		{"a", 10, 100}, {"b", 5, 50}, {"a", 10, 200}, {"c", 7, 70},
		{"a", 5, 50}, {"b", 12, 90}, {"c", 7, 77},
	}
	// want[i] 为第 i+1 步之后 a/b/c 的 winner（0 表示该键尚无变更）。
	want := [][3]int64{
		{100, 0, 0}, {100, 50, 0}, {200, 50, 0}, {200, 50, 70},
		{200, 50, 70}, {200, 90, 70}, {200, 90, 77},
	}
	e := lww.New(16)
	stepsOK := true
	for i, s := range steps {
		e.Apply(s.key, s.ver, s.val)
		for j, k := range []string{"a", "b", "c"} {
			w, ok := e.Winner(k)
			if (ok && w.Val != want[i][j]) || (!ok && want[i][j] != 0) {
				stepsOK = false
			}
		}
	}
	check("lww: 七步后三键 winner 与推导表一致", stepsOK)

	// lww：Replay 按 Key 字典序输出压缩结果。
	got := e.Replay()
	check("lww: Replay 输出 (a,200),(b,90),(c,77) 按 Key 升序",
		fmt.Sprint(got) == fmt.Sprint([]lww.Record{
			{Key: "a", Val: 200}, {Key: "b", Val: 90}, {Key: "c", Val: 77}}))

	// lww：大 m 下 Replay 检查的历史条数不随 m 增长。
	check("lww: Replay 检查条数不随 m 增长", lww.SelfCheck())

	// api：同一批变更喂进 api，与暴力参照一致，且 Replay 连调两次逐字段相同。
	a := api.New(16)
	batch := make([]api.Change, len(steps))
	for i, s := range steps {
		batch[i] = api.Change{Key: s.key, Ver: s.ver, Val: s.val}
	}
	if err := a.Feed(batch); err != nil {
		check("api: Feed 七步变更", false)
	}
	wantRecs := []api.Record{{Key: "a", Val: 200}, {Key: "b", Val: 90}, {Key: "c", Val: 77}}
	check("api: Replay 与暴力参照一致且连调两次相同",
		reflect.DeepEqual(a.Replay(), wantRecs) && reflect.DeepEqual(a.Replay(), a.Replay()))

	// api：History 按 sn 升序。
	ha := a.History("a")
	check("api: History 按 sn 升序", fmt.Sprint(ha) == fmt.Sprint([]api.Change{
		{Key: "a", Ver: 10, Val: 100, Sn: 1},
		{Key: "a", Ver: 10, Val: 200, Sn: 3},
		{Key: "a", Ver: 5, Val: 50, Sn: 5}}))

	// api：三类故障各有可判定且互不相同的哨兵错误。
	e1 := a.Feed([]api.Change{{Key: "", Ver: 1, Val: 1}})
	e2 := a.Feed([]api.Change{{Key: "a", Ver: -1, Val: 1}})
	over := make([]api.Change, 20)
	for i := range over {
		over[i] = api.Change{Key: "z", Ver: int64(i), Val: int64(i)}
	}
	e3 := a.Feed(over)
	check("api: 空 Key / 负 Ver / 历史超限三类错误可判定且互异",
		errors.Is(e1, api.ErrEmptyKey) && errors.Is(e2, api.ErrNegativeVer) &&
			errors.Is(e3, api.ErrHistoryLimit) &&
			e1 != e2 && e2 != e3 && e1 != e3)

	// api：被拒后状态不变，且系统可继续正常使用。
	okAfter := reflect.DeepEqual(a.Replay(), wantRecs) &&
		a.Feed([]api.Change{{Key: "d", Ver: 1, Val: 9}}) == nil &&
		len(a.Replay()) == 4
	check("api: 被拒后状态不变且可继续用", okAfter)

	// api：SelfCheck 通过；N 个 goroutine 并发只读结果逐字段相同。
	const n = 32
	outs := make([][]api.Record, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = a.SelfCheck()
			_ = a.History("a")
			outs[i] = a.Replay()
		}(i)
	}
	wg.Wait()
	same := a.SelfCheck()
	for _, o := range outs {
		same = same && reflect.DeepEqual(o, a.Replay())
	}
	check("api: SelfCheck 通过且并发只读一致", same)

	if failed {
		os.Exit(1)
	}
}
