package dagg

import (
	"errors"
	"maps"
	"math/rand"
	"slices"
	"strconv"
)

func recompute(acc []Change) map[string]int {
	type gv struct{ g, v string }
	mult, v := map[gv]int{}, map[string]int{}
	for _, c := range acc {
		mult[gv{c.Group, c.Val}] += c.Sign
	}
	for p, n := range mult {
		if n > 0 {
			v[p.g]++
		}
	}
	return v
}

// applyLog 模拟下游应用日志，核验每个前缀：每组至多一值、- 撤的恰为现值。
func applyLog(l []Out) (map[string]int, error) {
	v := map[string]int{}
	for _, o := range l {
		cur, ok := v[o.Group]
		switch {
		case o.Sign == 1 && ok:
			return nil, errors.New("log: group already present before +")
		case o.Sign == 1:
			v[o.Group] = o.N
		case !ok || cur != o.N:
			return nil, errors.New("log: minus does not retract current value")
		default:
			delete(v, o.Group)
		}
	}
	return v, nil
}

func noEqualPair(outs []Out) bool {
	for i := 1; i < len(outs); i++ {
		if p, o := outs[i-1], outs[i]; p.Sign == -1 && o.Sign == 1 && p.Group == o.Group && p.N == o.N {
			return false
		}
	}
	return true
}

// scan 白盒核验：distinct 与表一致、无 mult<=0 条目、全局 entries 一致（不变量 3）。
func (a *Agg) scan() error {
	sum := 0
	for _, s := range a.groups {
		e := s.Entries()
		for v, n := range e {
			if n <= 0 {
				return errors.New("invariant: non-positive entry " + v)
			}
			sum++
		}
		if s.Distinct() != len(e) {
			return errors.New("invariant: distinct counter diverges")
		}
	}
	if sum != a.entries {
		return errors.New("invariant: global entries counter diverges")
	}
	return nil
}

// SelfCheck 用内置九批序列及随机批次核验四条不变量，通过返回 nil。
func (a *Agg) SelfCheck() error {
	const g = "g"
	p := func(v string, s int) Change { return Change{g, v, s} }
	B := [][]Change{{p("a", 1)}, {p("b", 1)}, {p("a", 1)}, {p("a", -1)}, {p("b", -1)}, {p("b", -1)}, {p("c", 1), p("c", -1)}, {p("a", -1)}, {p("a", 1)}}
	W := [][]Out{{{g, 1, 1}}, {{g, 1, -1}, {g, 2, 1}}, nil, nil, {{g, 2, -1}, {g, 1, 1}}, nil, nil, {{g, 1, -1}}, {{g, 1, 1}}}
	z, acc, lg := New(10), []Change{}, []Out{}
	for i, b := range B {
		o, err := z.Feed(b)
		if i == 5 && (!errors.Is(err, ErrWithdraw) || !maps.Equal(z.View(), recompute(acc))) {
			return errors.New("batch 6 must be rejected without trace")
		} else if i == 5 {
			continue
		}
		if err != nil || !slices.Equal(o, W[i]) || !noEqualPair(o) {
			return errors.New("batch " + strconv.Itoa(i+1) + " log mismatch")
		}
		acc, lg = append(acc, b...), append(lg, o...)
		if err := z.scan(); err != nil {
			return err
		}
		if dv, lerr := applyLog(lg); lerr != nil || !maps.Equal(dv, recompute(acc)) || !maps.Equal(z.View(), recompute(acc)) {
			return errors.New("invariant 1/2 violated at batch " + strconv.Itoa(i+1))
		}
	}
	before := z.View() // 颠倒第 7 批：-(g,c) 在前必须拒绝且状态不变
	if _, err := z.Feed([]Change{p("c", -1), p("c", 1)}); !errors.Is(err, ErrWithdraw) || !maps.Equal(z.View(), before) {
		return errors.New("reversed batch 7 must be rejected without trace")
	}
	r := rand.New(rand.NewSource(1))
	rz, racc, rl := New(0), []Change{}, []Out{}
	rc := func() Change {
		return Change{string(rune('g' + r.Intn(3))), string(rune('a' + r.Intn(3))), r.Intn(2)*2 - 1}
	}
	for t := 0; t < 1000; t++ {
		b := make([]Change, 1+r.Intn(2))
		for k := range b {
			b[k] = rc()
		}
		o, err := rz.Feed(b)
		if err == nil && !noEqualPair(o) {
			return errors.New("random: equal old/new pair in batch")
		} else if err == nil {
			racc, rl = append(racc, b...), append(rl, o...)
		}
		if err := rz.scan(); err != nil {
			return err
		}
		if !maps.Equal(rz.View(), recompute(racc)) {
			return errors.New("random: view diverges")
		}
	}
	_, err := applyLog(rl)
	return err
}

// ScalingOK 只回布尔：单条变更批的检查条数不随组内 distinct 规模 m 增长（数值不导出，仅内部比较）。
func (a *Agg) ScalingOK() bool {
	for _, m := range []int{100, 1000, 10000} {
		z := New(0)
		for i := 0; i < m; i++ {
			if _, e := z.Feed([]Change{{"g", "v" + strconv.Itoa(i), 1}}); e != nil {
				return false
			}
		}
		probe := func(c Change) bool {
			_, e1 := z.Feed([]Change{c})
			got := z.checked // 探测批（单条）实际检查条目数
			_, e2 := z.Feed([]Change{{"g", c.Val, -c.Sign}})
			return e1 == nil && e2 == nil && got <= 2 // 常数，与 m 无关
		}
		if !probe(Change{"g", "fresh", 1}) || !probe(Change{"g", "v0", 1}) || !probe(Change{"g", "v1", -1}) {
			return false
		}
	}
	return true
}
