// 布隆过滤器演示：go run ./cmd/demo
// 不读命令行参数，不联网，退出码为 0。
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand"

	"ontology"
)

const (
	n        = 10000
	p        = 0.01
	queries  = 100000
)

var fails int

func elem(domain string, i uint64) []byte {
	b := make([]byte, 0, len(domain)+8)
	b = append(b, domain...)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], i)
	return append(b, num[:]...)
}

func step(name string, ok bool, detail string) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Printf("%s %s: %s\n", tag, name, detail)
}

func main() {
	// 1. 由 (n, p) 推出 m 与 k。
	f, err := ontology.New(n, p)
	step("params m/k", err == nil && f.M() == 95851 && f.K() == 7,
		fmt.Sprintf("m=%d k=%d (n=%d p=%.2f)", f.M(), f.K(), n, p))

	// 2. 插入一万个元素，零假阴性。
	for i := uint64(0); i < n; i++ {
		f.Add(elem("in", i))
	}
	fn := 0
	for i := uint64(0); i < n; i++ {
	if !f.MayContain(elem("in", i)) {
			fn++
		}
	}
	step("zero false negatives", fn == 0, fmt.Sprintf("false negatives=%d / %d", fn, n))

	// 3. 十万次未插入查询，实测假阳性率与 2p 对比。
	fp := 0
	for i := uint64(0); i < queries; i++ {
		if f.MayContain(elem("out", i)) {
			fp++
		}
	}
	rate := float64(fp) / queries
	step("measured FPR <= 2p", rate >= 0 && rate <= 2*p,
		fmt.Sprintf("measured=%.4f target p=%.2f upper 2p=%.2f", rate, p, 2*p))

	// 4. 打乱顺序两次插入，位数组逐字节相同。
	g, _ := ontology.New(n, p)
	for _, idx := range rand.New(rand.NewSource(1)).Perm(n) {
		g.Add(elem("in", uint64(idx)))
	}
	step("shuffled order identical", bytes.Equal(f.Bytes(), g.Bytes()),
		fmt.Sprintf("%d-byte snapshot equal", len(f.Bytes())))

	// 5. 同一元素重复插入一百万次，位数组与只插一次逐字节相同。
	once, _ := ontology.New(n, p)
	many, _ := ontology.New(n, p)
	e := elem("dup", 1)
	once.Add(e)
	for i := 0; i < 1_000_000; i++ {
		many.Add(e)
	}
	step("1e6 repeated adds identical", bytes.Equal(once.Bytes(), many.Bytes()),
		"bit array unchanged after 1000000 repeated adds")

	// 6. 异参数 Merge 返回可判定错误并给出两侧 (m, k)。
	other, _ := ontology.New(2*n, p)
	mergeErr := f.Merge(other)
	step("mismatch merge rejected", errors.Is(mergeErr, ontology.ErrParamMismatch),
		fmt.Sprintf("%v", mergeErr))

	// 7. 同参数 Merge 结果与合并插入逐字节相同，且不改源。
	a, _ := ontology.New(n, p)
	b, _ := ontology.New(n, p)
	combined, _ := ontology.New(n, p)
	for i := uint64(0); i < n/2; i++ {
		a.Add(elem("a", i))
		combined.Add(elem("a", i))
	}
	for i := uint64(0); i < n/2; i++ {
		b.Add(elem("b", i))
		combined.Add(elem("b", i))
	}
	bSnap := b.Bytes()
	merErr := a.Merge(b)
	step("merge equals combined insert",
		merErr == nil && bytes.Equal(a.Bytes(), combined.Bytes()) && bytes.Equal(b.Bytes(), bSnap),
		"merged == combined-insert, source filter untouched")

	// 8. EstimateCount 与真实值对比（相对误差 < 10%）。
	est := a.EstimateCount()
	rel := math.Abs(est-n) / n
	step("estimate count within 10%", rel <= 0.10,
		fmt.Sprintf("estimate=%.0f true=%d rel_err=%.2f%%", est, n, rel*100))

	// 9. 三类非法构造参数。
	_, eN := ontology.New(0, p)
	_, ePlo := ontology.New(n, 0)
	_, ePhi := ontology.New(n, 1)
	step("invalid params rejected",
		errors.Is(eN, ontology.ErrInvalidN) && errors.Is(ePlo, ontology.ErrInvalidP) && errors.Is(ePhi, ontology.ErrInvalidP),
		fmt.Sprintf("n<=0: %v; p<=0: %v; p>=1: %v", eN, ePlo, ePhi))

	if fails == 0 {
		fmt.Println("SUMMARY: all 9 checks OK")
	} else {
		fmt.Printf("SUMMARY: %d/9 checks FAIL\n", fails)
	}
}
