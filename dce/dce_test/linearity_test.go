package dce_test

import (
	"fmt"
	"testing"

	"ontology/dce/model"
)

// buildLineGraph 构造一条 n 个模块的链：m_i 的导出名 e 最终解析到 m_n 的声明 leaf。
// 每个模块还导出自身声明，制造星链规模。
func buildLineGraph(n int) (map[string]*model.Module, []string) {
	mods := map[string]*model.Module{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("m%d", i)
		m := &model.Module{
			ID:         id,
			SideEffect: model.SideEffectNo,
			Decls:      []model.Declaration{{Name: "self"}},
			Exports:    []model.Export{localExp("self")},
		}
		if i+1 < n {
			m.Exports = append(m.Exports, starExp(fmt.Sprintf("m%d", i+1)))
			m.Imports = []model.Import{imp(fmt.Sprintf("m%d", i+1))}
		} else {
			m.Decls = append(m.Decls, model.Declaration{Name: "leaf"})
			m.Exports = append(m.Exports, localExp("leaf"))
		}
		mods[id] = m
	}
	return mods, []string{"m0"}
}

// TestNearLinearWork 工作量计数必须随规模线性，而不是按名字/星链重复展开成平方级。
func TestNearLinearWork(t *testing.T) {
	measure := func(n int) (decls, scans, named, stars int) {
		mods, entries := buildLineGraph(n)
		reg := newRegistryFromMap(mods)
		res, err := reg.NewSession(entries).Solve()
		if err != nil {
			t.Fatal(err)
		}
		return res.Stats.DeclProcessed, res.Stats.ModulesScanned,
			res.ResolverStats.NamedResolves, res.ResolverStats.StarExpansions
	}

	d1, s1, n1, st1 := measure(100)
	d2, s2, n2, st2 := measure(400)

	ratio := func(a, b int) float64 {
		if a == 0 {
			return 0
		}
		return float64(b) / float64(a)
	}
	// 规模 4 倍，各项工作量增长不应显著超过线性（留出常数余量 6 倍上界）。
	for _, c := range []struct {
		name       string
		small, big int
	}{
		{"declProcessed", d1, d2},
		{"modulesScanned", s1, s2},
		{"namedResolves", n1, n2},
		{"starExpansions", st1, st2},
	} {
		if r := ratio(c.small, c.big); r > 6 {
			t.Fatalf("%s grew %.2fx for 4x size (likely super-linear): %d -> %d",
				c.name, r, c.small, c.big)
		}
	}
	t.Logf("n=100: decls=%d scans=%d named=%d stars=%d", d1, s1, n1, st1)
	t.Logf("n=400: decls=%d scans=%d named=%d stars=%d", d2, s2, n2, st2)
}
