package api_test

import (
	"errors"
	"math/rand"
	"strconv"
	"sync"
	"testing"

	"ontology/api"
	"ontology/sla"
)

type view struct {
	c, v, inf, mn, mx int64
	avg               float64
	br                bool
}

func rd(m *api.Monitor) view {
	return view{m.Count(), m.Violations(), m.InFlight(), m.MinLatency(), m.MaxLatency(), m.AvgLatency(), m.Breached()}
}
func be(m *api.Monitor, i int, lat int64) {
	id := "r" + strconv.Itoa(i)
	_ = m.Begin(id, 0)
	_ = m.End(id, lat)
}
func TestBatchRecompute(t *testing.T) {
	for _, n := range []int{2, 50, 500, 3000} {
		m, _ := api.New(10, 4, 3)
		rng := rand.New(rand.NewSource(int64(n)))
		var c, v, sm, mn, mx int64
		mn = 1 << 62
		for i := 0; i < n; i++ {
			lat := int64(rng.Intn(25))
			be(m, i, lat)
			c, sm = c+1, sm+lat
			v += int64(sla.Classify(lat, 10))
			if lat < mn {
				mn = lat
			}
			if lat > mx {
				mx = lat
			}
		}
		g := rd(m)
		if g.c != c || g.v != v || g.mn != mn || g.mx != mx || g.inf != 0 || g.avg != float64(sm)/float64(c) {
			t.Fatalf("n=%d %+v want c=%d v=%d mn=%d mx=%d", n, g, c, v, mn, mx)
		}
	}
}
func TestThresholdBoundary(t *testing.T) {
	bs := []int64{0, 0, 5}
	es := []int64{10, 11, 5}
	vs := []bool{false, true, false}
	for i := range bs {
		m, _ := api.New(10, 4, 3)
		_ = m.Begin("r", bs[i])
		if m.Count() != 0 || m.InFlight() != 1 {
			t.Fatal("在途请求被计入统计")
		}
		_ = m.End("r", es[i])
		if (m.Violations() == 1) != vs[i] || m.Count() != 1 || m.InFlight() != 0 {
			t.Fatalf("case %d 判定错误", i)
		}
	}
}
func TestSlidingWindowBreach(t *testing.T) {
	ls := [][]int64{{10, 15, 5, 11, 12, 4, 20}, {11, 11}, {11, 11, 11, 4, 4}}
	ws := [][]bool{{false, false, false, false, true, false, true}, {false, false}, {false, false, true, true, false}}
	for g := range ls {
		m, _ := api.New(10, 4, 3)
		for i, lat := range ls[g] {
			be(m, i, lat)
			if m.Breached() != ws[g][i] {
				t.Fatalf("组%d 步%d got=%v want=%v", g, i, m.Breached(), ws[g][i])
			}
		}
	}
}
func TestSentinelErrorsDistinct(t *testing.T) {
	es := []error{api.ErrInvalidParam, api.ErrDuplicateBegin, api.ErrUnknown, sla.ErrNegative}
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 4; j++ {
			if es[i] == es[j] {
				t.Fatal("哨兵错误不互异")
			}
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	m, _ := api.New(10, 4, 3)
	_ = m.Begin("A", 0)
	_ = m.End("A", 11)
	_ = m.Begin("B", 0)
	s0 := rd(m)
	for _, c := range []struct {
		do  func() error
		exp error
	}{
		{func() error { return m.Begin("B", 9) }, api.ErrDuplicateBegin},
		{func() error { return m.End("ghost", 2) }, api.ErrUnknown},
		{func() error { return m.End("A", 5) }, api.ErrUnknown},
	} {
		if e := c.do(); !errors.Is(e, c.exp) || rd(m) != s0 {
			t.Fatalf("拒绝判定/留痕错误 exp=%v e=%v", c.exp, e)
		}
	}
	_ = m.Begin("C", 100)
	s1 := rd(m)
	if e := m.End("C", 99); !errors.Is(e, sla.ErrNegative) || rd(m) != s1 {
		t.Fatalf("负延迟 e=%v 或留痕", e)
	}
	_ = m.End("C", 105)
	if rd(m).c != s1.c+1 {
		t.Fatal("负延迟后该请求不能继续正常使用")
	}
	for _, c := range [][3]int{{-1, 4, 3}, {10, 0, 3}, {10, 4, 0}, {10, 4, 5}} {
		g, e := api.New(int64(c[0]), c[1], c[2])
		if !errors.Is(e, api.ErrInvalidParam) || g != nil {
			t.Fatalf("非法参数未返回 ErrInvalidParam %v e=%v", c, e)
		}
	}
}
func TestConcurrentReadersConsistent(t *testing.T) {
	m, _ := api.New(10, 4, 3)
	for i := 0; i < 60; i++ {
		_ = m.Begin("r"+strconv.Itoa(i), 0)
		if i%5 != 0 {
			_ = m.End("r"+strconv.Itoa(i), int64(4+i%12))
		}
	}
	res := make([]view, 64)
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func(i int) { defer wg.Done(); res[i] = rd(m) }(i)
	}
	wg.Wait()
	for i := 1; i < len(res); i++ {
		if res[i] != res[0] {
			t.Fatalf("并发只读不一致 res[%d]=%+v", i, res[i])
		}
	}
}
func TestSelfCheck(t *testing.T) {
	if m, _ := api.New(1, 1, 1); m.SelfCheck() != nil {
		t.Fatal("SelfCheck 四条不变量核验失败")
	}
}
