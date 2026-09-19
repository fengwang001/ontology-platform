// Command demo 实际演练流式 Top-K 选择器的每一条关键语义，
// 不读命令行参数、不联网。每步打印一行 OK/FAIL，末尾打印总计。
package main

import (
	"errors"
	"fmt"
	"math"

	"ontology"
)

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}

	// 1. Desc 与 Asc 下并列都按 ID 升序。
	desc, _ := ontology.New(3, ontology.Desc)
	for _, e := range []struct {
		id string
		v  float64
	}{{"a", 3}, {"b", 1}, {"c", 3}, {"d", 3}, {"e", 2}} {
		desc.Push(e.id, e.v)
	}
	check("desc ties ordered by ID asc: "+fmt.Sprint(idList(desc)), equalIDs(desc, "a", "c", "d"))

	asc, _ := ontology.New(3, ontology.Asc)
	for _, e := range []struct {
		id string
		v  float64
	}{{"a", 1}, {"z", 1}, {"m", 1}, {"q", 9}} {
		asc.Push(e.id, e.v)
	}
	check("asc ties ordered by ID asc: "+fmt.Sprint(idList(asc)), equalIDs(asc, "a", "m", "z"))

	// 2. 并列跨 K 边界时截断掉 ID 字典序更大的那个。
	cut, _ := ontology.New(2, ontology.Desc)
	cut.Push("zeta", 5)
	cut.Push("alpha", 5)
	cut.Push("mid", 5)
	check("tie across K boundary keeps smaller IDs: "+fmt.Sprint(idList(cut)),
		equalIDs(cut, "alpha", "mid"))

	// 3. 同一批元素两种到达顺序，Snapshot 逐元素一致。
	batch := []struct {
		id string
		v  float64
	}{{"p", 4}, {"q", 4}, {"r", 8}, {"s", 1}, {"t", 8}}
	s1 := pushBatch(ontology.Desc, 3, batch)
	s2 := pushBatch(ontology.Desc, 3, reverse(batch))
	check(fmt.Sprintf("shuffle-independent snapshot: %v == %v", s1, s2), same(s1, s2))

	// 4. NaN 被跳过且计数正确。
	nanSel, _ := ontology.New(2, ontology.Desc)
	nanSel.Push("x", 9)
	nanSel.Push("bad-1", math.NaN())
	nanSel.Push("bad-2", math.NaN())
	nanSel.Push("y", 3)
	check(fmt.Sprintf("NaN rejected, skipped=%d, kept=%v", nanSel.Skipped(), idList(nanSel)),
		nanSel.Skipped() == 2 && equalIDs(nanSel, "x", "y"))

	// 5. +0.0 与 -0.0 视为相等并列，按 ID 升序。
	zero, _ := ontology.New(3, ontology.Desc)
	zero.Push("z-pos", 0)
	zero.Push("a-neg", math.Copysign(0, -1))
	zero.Push("big", math.Inf(1))
	check("+0/-0 tie by ID asc (inf first): "+fmt.Sprint(idList(zero)),
		equalIDs(zero, "big", "a-neg", "z-pos"))

	// 6. 同 ID 覆盖后分数变差立即掉出 Top-K。
	over, _ := ontology.New(2, ontology.Desc)
	over.Push("a", 100)
	over.Push("b", 50)
	over.Push("a", 1)
	check("overwritten ID drops out immediately: "+fmt.Sprint(idList(over)),
		equalIDs(over, "b"))

	// 7. K <= 0 返回可判定错误。
	bad1, err1 := ontology.New(0, ontology.Desc)
	bad2, err2 := ontology.New(-3, ontology.Asc)
	check("K<=0 returns ErrInvalidCapacity (no panic)",
		bad1 == nil && bad2 == nil &&
			errors.Is(err1, ontology.ErrInvalidCapacity) &&
			errors.Is(err2, ontology.ErrInvalidCapacity))

	// 8. 长流上持有数始终不超过 K。
	const k = 8
	long, _ := ontology.New(k, ontology.Desc)
	boundOK := true
	maxHeld := 0
	for i := 0; i < 5000; i++ {
		long.Push(fmt.Sprintf("id-%05d", i), math.Mod(float64(i)*1.618, 100))
		if h := long.Len(); h > k {
			boundOK = false
		} else if h > maxHeld {
			maxHeld = h
		}
	}
	check(fmt.Sprintf("held count <= K on 5000 pushes (max=%d, K=%d)", maxHeld, k),
		boundOK && long.Len() == k)

	fmt.Printf("TOTAL %d/%d checks passed\n", pass, total)
	if pass != total {
		panic("demo checks failed")
	}
}

func idList(s *ontology.TopK) []string {
	snap := s.Snapshot()
	out := make([]string, len(snap))
	for i, e := range snap {
		out[i] = e.ID
	}
	return out
}

func equalIDs(s *ontology.TopK, want ...string) bool {
	return same(idList(s), want)
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pushBatch(dir ontology.Direction, k int, in []struct {
	id string
	v  float64
}) []string {
	s, _ := ontology.New(k, dir)
	for _, e := range in {
		s.Push(e.id, e.v)
	}
	return idList(s)
}

func reverse(in []struct {
	id string
	v  float64
}) []struct {
	id string
	v  float64
} {
	out := make([]struct {
		id string
		v  float64
	}, len(in))
	copy(out, in)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
