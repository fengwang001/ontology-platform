package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"ontology/api"
	"ontology/resv"
	"ontology/rnd"
)

var fails int

func check(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		fails++
	}
	fmt.Println(mark, name)
}

func render(ss []api.Sample) string {
	parts := make([]string, len(ss))
	for i, x := range ss {
		parts[i] = fmt.Sprintf("%s:%.4f", x.ID, x.Key)
	}
	return strings.Join(parts, ",")
}

func main() {
	// rnd: 按序取数、用尽判定、合法性校验
	src, _ := rnd.New([]float64{0.3, 0.49})
	u1, _ := src.Next()
	u2, _ := src.Next()
	_, e3 := src.Next()
	_, bad := rnd.New([]float64{0.5, 1.0})
	check("rnd: ordered draw, exhaustion, validation", u1 == 0.3 && u2 == 0.49 &&
		errors.Is(e3, rnd.ErrExhausted) && src.Consumed() == 2 && errors.Is(bad, rnd.ErrBadValue))

	// resv: 键计算、容量、替换判定、键相等先到者保留
	src2, _ := rnd.New([]float64{0.3, 0.49, 0.81, 0.5, 0.49})
	p := resv.NewPool(2)
	p.Consider(src2, "a", 1)   // 0.3000
	p.Consider(src2, "b", 2)   // 0.7000
	p.Consider(src2, "c", 0.5) // 0.6561 替换 a
	p.Consider(src2, "d", 2)   // 0.7071 替换 c
	p.Consider(src2, "e", 2)   // 0.7000 与池底 b 相等，不替换
	it := p.Items()
	check("resv: replace lowest rank, tie keeps first arrival", p.Len() == 2 && it[0].ID == "d" && it[1].ID == "b")

	// api: 第三节八步轨迹；第 4 步 W=0 被拒且不消耗随机数
	us := []float64{0.30, 0.49, 0.81, 0.0625, 0.72, 0.64, 0.9, 0.25}
	ws := []float64{1, 2, 0.5, 0, 4, 1, 2, 0.5}
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	want := []string{"a:0.3000|1", "b:0.7000,a:0.3000|2", "b:0.7000,c:0.6561|3",
		"b:0.7000,c:0.6561|3", "b:0.7000,c:0.6561|4", "f:0.7200,b:0.7000|5",
		"g:0.8000,f:0.7200|6", "h:0.8100,g:0.8000|7"}
	sm, _ := api.New(2, us)
	ok := true
	for i, id := range ids {
		err := sm.Offer([]api.Item{{ID: id, W: ws[i]}})
		ok = ok && (id != "d" && err == nil || errors.Is(err, api.ErrZeroWeight)) &&
			fmt.Sprintf("%s|%d", render(sm.Sample()), sm.Consumed()) == want[i]
	}
	check("api: 8-step trace, W=0 rejected without consuming", ok)

	// api: 与批量排序参照一致
	usR := []float64{0.3, 0.49, 0.81, 0.0625, 0.72, 0.64}
	itR := []api.Item{{ID: "a", W: 1}, {ID: "b", W: 2}, {ID: "c", W: 0.5}, {ID: "e", W: 4}, {ID: "f", W: 1}, {ID: "g", W: 2}}
	sr, _ := api.New(2, usR)
	ok = sr.Offer(itR) == nil
	keys, idx := make([]float64, len(itR)), make([]int, len(itR))
	for i, x := range itR {
		keys[i], idx[i] = math.Pow(usR[i], 1/x.W), i
	}
	sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]] > keys[idx[b]] })
	got := sr.Sample()
	ok = ok && len(got) == 2 && got[0].ID == itR[idx[0]].ID && got[1].ID == itR[idx[1]].ID &&
		got[0].Key == keys[idx[0]] && got[1].Key == keys[idx[1]]
	check("api: sample equals batch-sorted reference", ok)

	// api: 键相等时先到者保留
	st, _ := api.New(1, []float64{0.5, 0.5})
	st.Offer([]api.Item{{ID: "first", W: 1}, {ID: "second", W: 1}})
	check("api: equal keys keep first arrival", st.Sample()[0].ID == "first")

	// api: 四类可判定错误且互不相同
	s4, _ := api.New(2, []float64{0.5, 0.5, 0.5})
	ok = errors.Is(s4.Offer([]api.Item{{ID: "z", W: 0}}), api.ErrZeroWeight) &&
		errors.Is(s4.Offer([]api.Item{{ID: "n", W: math.NaN()}}), api.ErrBadWeight) &&
		errors.Is(s4.Offer([]api.Item{{ID: "n", W: -1}}), api.ErrBadWeight) &&
		s4.Offer([]api.Item{{ID: "v", W: 1}}) == nil &&
		errors.Is(s4.Offer([]api.Item{{ID: "v", W: 1}}), api.ErrBadInput) &&
		errors.Is(s4.Offer([]api.Item{{ID: "x", W: 1}, {ID: "y", W: 1}, {ID: "w", W: 1}}), api.ErrExhausted)
	_, errK := api.New(0, nil)
	sents := map[error]bool{api.ErrZeroWeight: true, api.ErrBadWeight: true, api.ErrExhausted: true, api.ErrBadInput: true}
	check("api: four distinct decidable errors", ok && errors.Is(errK, api.ErrBadInput) && len(sents) == 4)

	// api: 被拒后蓄水池与游标不变
	s5, _ := api.New(2, []float64{0.3, 0.49, 0.81})
	s5.Offer([]api.Item{{ID: "a", W: 1}})
	before := fmt.Sprintf("%s|%d", render(s5.Sample()), s5.Consumed())
	s5.Offer([]api.Item{{ID: "b", W: 1}, {ID: "bad", W: 0}})                // 第二行 W=0
	s5.Offer([]api.Item{{ID: "c", W: 1}, {ID: "d", W: 1}, {ID: "e", W: 1}}) // 随机源用尽
	check("api: rejected batch leaves pool & cursor untouched", fmt.Sprintf("%s|%d", render(s5.Sample()), s5.Consumed()) == before)

	// resv: 大 m 下访问个数不随 m 线性增长（白盒测试断言上界）
	check("resv: visits bounded by 2*ceil(log2 m)+4", exec.Command("go", "test", "-count=1", "-run", "TestVisitBounds", "./resv/").Run() == nil)

	// api: 并发 Offer 后随机数守恒
	usC := make([]float64, 64)
	for i := range usC {
		usC[i] = 0.05 + 0.9*float64(i)/63
	}
	sc, _ := api.New(5, usC)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				sc.Offer([]api.Item{{ID: fmt.Sprintf("g%dx%d", g, i), W: 1}})
			}
		}(g)
	}
	wg.Wait()
	seen := map[string]bool{}
	ok = sc.Consumed() == 64 && len(sc.Sample()) == 5
	for _, x := range sc.Sample() {
		ok = ok && !seen[x.ID]
		seen[x.ID] = true
	}
	check("api: concurrent Offer conserves randoms", ok)

	// api: 内置自检核验四条不变量
	ss, _ := api.New(1, []float64{0.5})
	check("api: SelfCheck verifies all invariants", ss.SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}
