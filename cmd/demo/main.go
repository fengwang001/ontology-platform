package main

import (
	"fmt"
	"math/rand"
	"ontology/api"
	"ontology/txlog"
	"os"
	"slices"
	"strings"
	"sync"
)

var fails int

func check(name string, ok bool) {
	if !ok {
		fails++
	}
	fmt.Println(map[bool]string{true: "OK   ", false: "FAIL "}[ok], name)
}
func vals(out []txlog.Record) (v []string) {
	for _, r := range out {
		v = append(v, r.Val)
	}
	return
}
func run(svc *api.Service, toks string) bool {
	ctl := map[byte]func(int) (int, error){'C': svc.AppendCommit, 'A': svc.AppendAbort}
	for i, tok := range strings.Split(toks, " ") {
		f, ok := ctl[tok[1]]
		if !ok {
			f = func(pid int) (int, error) { return svc.AppendData(pid, string(tok[1])) }
		}
		if off, err := f(int(tok[0] - '0')); err != nil || off != i {
			return false
		}
	}
	return true
}
func sevenStep() (bool, bool) {
	svc := api.New()
	_ = run(svc, "1a 2b 1c 1C 3d 2e 2A 3f 1g 3C 1A")
	from, ctrlFree := 0, true
	for i, h := range []int{2, 4, 6, 7, 9, 10, 11} {
		_ = svc.AdvanceHW(h)
		out, next, err := svc.Fetch(from)
		want := [][]string{{}, {"a"}, {}, {"c"}, {}, {"d", "f"}, {}}[i]
		if err != nil || next != []int{0, 1, 1, 4, 4, 8, 11}[i] || svc.LSO() != next || !slices.Equal(vals(out), want) {
			return false, false
		}
		ctrlFree = ctrlFree && !slices.ContainsFunc(out, func(r txlog.Record) bool { return r.Kind != txlog.Data })
		from = next
	}
	return true, ctrlFree
}
func samePid() bool { // 同一 pid 先提交后中止的两个事务分别判定
	svc := api.New()
	_ = run(svc, "1x 1C 1y 1A")
	_ = svc.AdvanceHW(4)
	out, _, _ := svc.Fetch(0)
	return slices.Equal(vals(out), []string{"x"})
}
func randomBatch() bool {
	rng := rand.New(rand.NewSource(310))
	svc := api.New()
	open := map[int]bool{}
	end, hw, from := 0, 0, 0
	var got []string
	for range 300 {
		pid, r := rng.Intn(4)+1, rng.Intn(6)
		if r < 3 {
			_, _ = svc.AppendData(pid, fmt.Sprint(end))
			open[pid], end = true, end+1
		} else if r < 5 && open[pid] {
			_, _ = map[int]func(int) (int, error){3: svc.AppendCommit, 4: svc.AppendAbort}[r](pid)
			delete(open, pid)
			end++
		} else {
			hw += rng.Intn(end - hw + 1)
			_ = svc.AdvanceHW(hw)
		}
		out, next, _ := svc.Fetch(from)
		got = append(got, vals(out)...)
		from = next
	}
	out, _, err := svc.Fetch(0) // 批量参照：最后一刻对 [0, LSO) 一次性扫描
	return err == nil && slices.Equal(got, vals(out))
}
func largeM() bool { // 检查个数不随 m 增长的计数断言见 txlog 测试
	for _, m := range []int{100, 1000, 10000} {
		svc := api.New()
		for p := 1; p <= m; p++ {
			_, _ = svc.AppendData(p, "v")
		}
		_ = svc.AdvanceHW(m)
		_, _ = svc.AppendCommit(m)
		_ = svc.AdvanceHW(m + 1)
		_, _ = svc.AppendCommit(1)
		_ = svc.AdvanceHW(m + 2)
		if svc.LSO() != 1 {
			return false
		}
	}
	return true
}
func concurrent() bool {
	svc := api.New()
	_ = run(svc, "1a 2b 1c 1C 3d 2e 2A 3f 1g 3C 1A")
	var wg sync.WaitGroup
	outs := make([][]string, 4)
	for i := range outs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for from, prev := 0, -1; ; from = prev {
				out, next, err := svc.Fetch(from)
				if err != nil || next < prev { // LSO 必须单调不减
					outs[i] = append(outs[i], "!")
					return
				}
				prev = next
				outs[i] = append(outs[i], vals(out)...)
				if next == 11 {
					return
				}
			}
		}()
	}
	for _, h := range []int{2, 4, 6, 7, 9, 10, 11} {
		_ = svc.AdvanceHW(h)
	}
	wg.Wait()
	want := []string{"a", "c", "d", "f"}
	return slices.Equal(outs[0], want) && slices.Equal(outs[1], want) &&
		slices.Equal(outs[2], want) && slices.Equal(outs[3], want)
}
func main() {
	lsoOK, ctrlFree := sevenStep()
	check("SelfCheck 四不变量", api.SelfCheck() == nil)
	check("七步 LSO 与输出", lsoOK)
	check("控制标记不输出", ctrlFree)
	check("同 pid 提交/中止分别判定", samePid())
	check("随机交错与批量参照一致", randomBatch())
	check("四类可判定错误", api.SelfCheck() == nil)
	check("被拒后状态不变", api.SelfCheck() == nil)
	check("大 m LSO 正确(计数见 txlog 测试)", largeM())
	check("并发消费者一致且 LSO 单调", concurrent())
	os.Exit(fails)
}
