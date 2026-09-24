package main

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/api"
	"ontology/resv"
)

var fails int

func mark(name string, ok bool) {
	if !ok {
		fails++
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}
func near(a, b float64) bool           { return math.Abs(a-b) < 1e-12 }
func it(id string, w float64) api.Item { return api.Item{ID: id, W: w} }
func newSamp(k int, us []float64) *api.Sampler {
	s, _ := api.New(k, us)
	return s
}

// walkthrough 复现第三节八步：逐步核对池（排名 ID 序拼成串）与 Consumed。
func walkthrough() bool {
	s, _ := api.New(2, []float64{.30, .49, .81, .0625, .72, .64, .9, .25})
	ids := "abcdefgh"
	ws := []float64{1, 2, .5, 0, 4, 1, 2, .5}
	pools := []string{"a", "ba", "bc", "bc", "bc", "fb", "gf", "hg"}
	cons := []int{1, 2, 3, 3, 4, 5, 6, 7}
	for i := 0; i < 8; i++ {
		err := s.Offer([]api.Item{it(string(ids[i]), ws[i])})
		if (err == nil) == (ws[i] == 0) { // 仅 d(W=0) 必须报错
			return false
		}
		g := s.Sample()
		if s.Consumed() != cons[i] || len(g) != len(pools[i]) {
			return false
		}
		for j := range g {
			if g[j].ID != string(pools[i][j]) {
				return false
			}
		}
	}
	g := s.Sample()
	return near(g[0].Key, .81) && near(g[1].Key, .8)
}

func main() {
	mark("eight-step walkthrough pools & Consumed", walkthrough())
	// 第4步 W==0 被拒不耗随机数，后续 e 仍取“本该给 d”的下一个 u=.0625。
	z := newSamp(2, []float64{.30, .49, .81, .0625, .72})
	for _, x := range []api.Item{it("a", 1), it("b", 2), it("c", .5)} {
		_ = z.Offer([]api.Item{x})
	}
	e0 := z.Offer([]api.Item{it("d", 0)})
	_ = z.Offer([]api.Item{it("e", 4)})
	mark("step4 W==0 rejected, no random consumed", errors.Is(e0, api.ErrZeroWeight) && z.Consumed() == 4)
	// 与批量排序参照一致由 SelfCheck 内置重排比对钉住。
	r := newSamp(3, []float64{.31, .42, .83, .07, .77, .66, .91})
	for _, x := range []api.Item{it("a", 1), it("b", 2), it("c", .5), it("e", 4), it("f", 1), it("g", 2), it("h", .5)} {
		_ = r.Offer([]api.Item{x})
	}
	mark("sample equals batch-sort reference", r.SelfCheck() == nil)
	// 键相等先到者保留：x 键 .5^1=.5；y 键 .25^(1/2)=.5，晚到不替换。
	t := newSamp(1, []float64{.5, .25})
	_ = t.Offer([]api.Item{it("x", 1), it("y", 2)})
	mark("equal key keeps earlier arrival", t.Sample()[0].ID == "x")
	// 四类可判定、互不相同的哨兵错误。
	_, eK := api.New(0, nil)
	_, eU := api.New(1, []float64{2})
	eZ := newSamp(1, []float64{.1}).Offer([]api.Item{it("z", 0)})
	eB := newSamp(1, []float64{.1}).Offer([]api.Item{it("n", math.NaN())})
	q3 := newSamp(1, []float64{.1})
	_ = q3.Offer([]api.Item{it("q", 1)})
	eX := q3.Offer([]api.Item{it("r", 1)})
	ss := []error{api.ErrInvalidInput, api.ErrZeroWeight, api.ErrBadWeight, api.ErrExhausted}
	distinct := errors.Is(eK, ss[0]) && errors.Is(eU, ss[0]) && errors.Is(eZ, ss[1]) &&
		errors.Is(eB, ss[2]) && errors.Is(eX, ss[3]) && ss[0] != ss[1] && ss[0] != ss[2] &&
		ss[0] != ss[3] && ss[1] != ss[2] && ss[1] != ss[3] && ss[2] != ss[3]
	mark("four distinct decidable error kinds", distinct)
	// 被拒整批不留痕：第二条空 ID，池与游标不变，之后仍可用。
	p := newSamp(2, []float64{.4, .6, .8})
	before := p.Consumed()
	_ = p.Offer([]api.Item{it("ok", 1), it("", 1)})
	intact := p.Consumed() == before && len(p.Sample()) == 0
	_ = p.Offer([]api.Item{it("ok", 1)})
	mark("rejected batch leaves state & cursor intact", intact && p.Consumed() == before+1 && p.SelfCheck() == nil)
	mark("heap visits bounded independent of m", resv.ComplexityOK())
	mark("concurrent Offer conserves randoms bijectively", concurrent())
	if fails != 0 {
		fmt.Println("RESULT: FAIL")
		return
	}
	fmt.Println("RESULT: OK")
}

func concurrent() bool {
	const n, per = 8, 20
	tot := n * per
	us := make([]float64, tot)
	for i := range us {
		us[i] = float64(i+1) / float64(tot+1)
	}
	s, _ := api.New(tot, us)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			b := make([]api.Item, per)
			for j := range b {
				b[j] = it(fmt.Sprintf("g%d-%d", g, j), 1)
			}
			_ = s.Offer(b)
		}(g)
	}
	wg.Wait()
	if s.Consumed() != tot || len(s.Sample()) != tot || s.SelfCheck() != nil {
		return false
	}
	uset, uk, ui := map[float64]bool{}, map[float64]bool{}, map[string]bool{}
	for _, u := range us {
		uset[u] = true
	}
	for _, e := range s.Sample() {
		if uk[e.Key] || ui[e.ID] || !uset[e.Key] {
			return false
		}
		uk[e.Key], ui[e.ID] = true, true
	}
	return len(uk) == tot
}
