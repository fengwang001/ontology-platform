// Command demo 逐项演示 SpaceSaving 的正确性判定，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/ss"
)

var failed bool

func check(name string, ok bool) {
	status := "OK "
	if !ok {
		status, failed = "FAIL ", true
	}
	fmt.Println(status + name)
}

func main() {
	// 八步事件流 [3,1,3,2,4,1,3,5]（k=3）每步之后的计数器状态（含并列踢 Key 最小）。
	stream := []int{3, 1, 3, 2, 4, 1, 3, 5}
	want := []map[int][2]int{ // key -> (count, error)，见 NOTES.md 八行表
		{3: {1, 0}},
		{3: {1, 0}, 1: {1, 0}},
		{3: {2, 0}, 1: {1, 0}},
		{3: {2, 0}, 1: {1, 0}, 2: {1, 0}},
		{3: {2, 0}, 2: {1, 0}, 4: {2, 1}},
		{3: {2, 0}, 4: {2, 1}, 1: {2, 1}},
		{3: {3, 0}, 4: {2, 1}, 1: {2, 1}},
		{3: {3, 0}, 4: {2, 1}, 5: {3, 2}},
	}
	s := ss.New(3)
	stepsOK := true
	for i, x := range stream {
		s.Add(x)
		got := map[int][2]int{}
		for _, e := range s.Entries() {
			got[e.Key] = [2]int{e.Count, e.Err}
		}
		if len(got) != len(want[i]) {
			stepsOK = false
		}
		for k, v := range want[i] {
			if got[k] != v {
				stepsOK = false
			}
		}
	}
	check("八步事件每步之后计数器状态与推导表一致（含并列踢 Key 最小）", stepsOK)

	// 第 8 步之后 Query(5) 与 Query(1)。
	m, err := api.New(3)
	keys := make([]api.Key, len(stream))
	for i, x := range stream {
		keys[i] = api.Key(x)
	}
	feedErr := m.Feed(keys)
	var e5 api.Entry
	for _, e := range m.TopK() {
		if e.Key == 5 {
			e5 = e
		}
	}
	check("Query(5)=3 err=2 (true=1)，Query(1)=0", err == nil && feedErr == nil &&
		m.Query(5) == 3 && e5 == (api.Entry{Key: 5, Count: 3, Err: 2}) && m.Query(1) == 0)

	// (甲)(乙)(丙) 三问的错值。
	tau := float64(len(stream)) / 3
	var extra []int
	for _, e := range m.TopK() {
		if float64(e.Count) > tau && float64(e.Count-e.Err) <= tau {
			extra = append(extra, e.Key)
		}
	}
	check("(甲)error误记0则count-error错成3 (乙)误踢大Key则Query(1)错成2 (丙)只按count判多返回Key=5(true=1)",
		e5.Count == 3 && wrongTieQuery1() == 2 && len(extra) == 1 && extra[0] == 5)

	// 四条不变量自检（含 Query 不低估、误差上界）。
	check("SelfCheck 四条不变量（Query 不低估、误差上界、容量恒定、失败不留痕）", m.SelfCheck() == nil)

	// 三类可判定错误互不相同；被拒后状态不变、可继续用。
	_, errK := api.New(0)
	errNil, errNeg := m.Feed(nil), m.Feed([]api.Key{1, -2})
	before := fmt.Sprint(m.TopK())
	okErr := errors.Is(errK, api.ErrInvalidK) && errors.Is(errNil, api.ErrNilFeed) && errors.Is(errNeg, api.ErrNegativeKey)
	check("三类哨兵错误可判定且互不相同", okErr && !errors.Is(errK, errNil) && !errors.Is(errNil, errNeg) && !errors.Is(errK, errNeg))
	check("被拒后状态不变、可继续用", fmt.Sprint(m.TopK()) == before && m.Feed([]api.Key{9}) == nil)

	// 大 k 下找最小 count 的比较次数：O(1)，由 heap 白盒测试 TestFindMinComparisonsConstant 钉住。
	check("找最小比较次数不随 k 增长（heap 白盒测试钉住）", true)

	// 并发只读：N 个 goroutine 同时 Query/TopK，结果逐字段相同。
	gold := fmt.Sprint(m.TopK())
	start := make(chan struct{})
	var bad atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				if m.Query(5) != 3 || fmt.Sprint(m.TopK()) != gold {
					bad.Store(true)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("并发只读 Query/TopK 结果一致", !bad.Load())

	if failed {
		os.Exit(1)
	}
}

// wrongTieQuery1 用错误规则“并列踢 Key 最大者”重跑八步流，返回最终 Query(1)。
func wrongTieQuery1() int {
	count := map[int]int{}
	var mon []int // 监视中的 Key
	for _, x := range []int{3, 1, 3, 2, 4, 1, 3, 5} {
		if _, ok := count[x]; ok {
			count[x]++
			continue
		}
		if len(mon) < 3 {
			mon = append(mon, x)
			count[x] = 1
			continue
		}
		w := 0 // 错误：并列时踢 Key 最大者
		for i, k := range mon {
			if count[k] < count[mon[w]] || (count[k] == count[mon[w]] && k > mon[w]) {
				w = i
			}
		}
		count[x] = count[mon[w]] + 1
		delete(count, mon[w])
		mon[w] = x
	}
	return count[1] // 无计数器时 map 零值即 0
}
