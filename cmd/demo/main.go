// Command demo 演练等宽直方图的边界归属与计数语义。
// 直接 `go run ./cmd/demo` 即可，不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"math"

	"ontology"
)

var failures int

func check(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		failures++
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func main() {
	h, _ := ontology.NewHistogram(0, 1, 3)

	// 1. 若干边界样本的桶号。
	samples := []struct {
		x    float64
		want int
		desc string
	}{
		{0, 0, "x==lo -> bucket 0"},
		{math.Copysign(0, -1), 0, "x==-0.0 -> bucket 0"},
		{1.0 / 3.0, 1, "x==1/3 -> bucket 1"},
		{2.0 / 3.0, 2, "x==2/3 -> bucket 2"},
	}
	ok := true
	for _, s := range samples {
		hh, _ := ontology.NewHistogram(0, 1, 3)
		_ = hh.Add(s.x)
		b := hh.Buckets()
		var got int
		for i, c := range b {
			if c != 1 {
				if c != 0 {
					ok = false
				}
				continue
			}
			got = i
		}
		if hh.Overflow() != 0 || hh.Underflow() != 0 || got != s.want {
			ok = false
		}
	}
	_ = h.Add(0)
	_ = h.Add(1.0 / 3.0)
	_ = h.Add(2.0 / 3.0)
	check("边界样本桶号", ok, fmt.Sprintf("buckets=%v", h.Buckets()))

	// 2. hi 本身计入上溢而不是最后一桶。
	hh, _ := ontology.NewHistogram(0, 1, 3)
	_ = hh.Add(1)
	check("hi 计入上溢", hh.Overflow() == 1 && hh.Buckets()[2] == 0,
		fmt.Sprintf("over=%d last=%d", hh.Overflow(), hh.Buckets()[2]))

	// 3. 天真算法 vs 校正算法：运行时找一个真实失配边界。
	n := 9
	lo, hi := 0.0, 1.0
	w := (hi - lo) / float64(n)
	badK, naive, fixed := -1, 0, 0
	for k := 1; k < n; k++ {
		x := lo + float64(k)*w
		hh, _ := ontology.NewHistogram(lo, hi, n)
		_ = hh.Add(x)
		ni := ontology.NaiveIndex(lo, w, n, x)
		var fi int
		for i, c := range hh.Buckets() {
			if c == 1 {
				fi = i
			}
		}
		if ni != fi {
			badK, naive, fixed = k, ni, fi
			break
		}
	}
	check("天真算法vs校正", badK > 0 && naive != fixed,
		fmt.Sprintf("(0,1,%d) 边界k=%d naive=%d corrected=%d", n, badK, naive, fixed))

	// 4. 计数恒等式。
	_ = h.Add(1)
	_ = h.Add(-1)
	_ = h.Add(math.NaN())
	s := h.Snapshot()
	check("计数恒等式", s.IdentityHolds(),
		fmt.Sprintf("buckets=%v under=%d over=%d skip=%d added=%d",
			s.Counts, s.Under, s.Over, s.Skip, s.Added))

	// 5. NaN 跳过与 ±Inf 溢出。
	sp, _ := ontology.NewHistogram(0, 1, 2)
	nanErr := errors.Is(sp.Add(math.NaN()), ontology.ErrNaN)
	_ = sp.Add(math.Inf(-1))
	_ = sp.Add(math.Inf(1))
	check("NaN跳过/±Inf溢出", nanErr && sp.Skipped() == 1 &&
		sp.Underflow() == 1 && sp.Overflow() == 1,
		fmt.Sprintf("skip=%d under=%d over=%d", sp.Skipped(), sp.Underflow(), sp.Overflow()))

	// 6. 异参数 Merge 报错。
	a, _ := ontology.NewHistogram(0, 1, 3)
	b, _ := ontology.NewHistogram(0, 1, 4)
	_, err := a.Merge(b)
	check("异参数Merge报错", errors.Is(err, ontology.ErrMismatchedSpec),
		fmt.Sprintf("%v", err))

	// 7. Merge 不改源。
	m1, _ := ontology.NewHistogram(0, 1, 3)
	m2, _ := ontology.NewHistogram(0, 1, 3)
	_ = m1.Add(0.1)
	_ = m2.Add(0.9)
	m, _ := m1.Merge(m2)
	after := m2.Buckets()
	leftUnchanged := m1.Buckets()[0] == 1 && m1.Buckets()[2] == 0
	check("Merge不改源", after[2] == 1 && after[0] == 0 && leftUnchanged &&
		m.Buckets()[0] == 1 && m.Buckets()[2] == 1,
		fmt.Sprintf("left=%v right=%v merged=%v", m1.Buckets(), after, m.Buckets()))

	tag := "OK"
	if failures != 0 {
		tag = "FAIL"
	}
	fmt.Printf("%s 总计 %d 个失败\n", tag, failures)
}
