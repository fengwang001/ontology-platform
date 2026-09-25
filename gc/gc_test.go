package gc

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"testing"

	"ontology/vv"
)

func tv(xs ...int) vv.Vector { return vv.Vector(xs) }
func rv(rng *rand.Rand, r int) vv.Vector {
	v := make(vv.Vector, r)
	for i := range v {
		v[i] = rng.Intn(4)
	}
	return v
}
func failIf(t *testing.T, c bool, f string, a ...any) {
	t.Helper()
	if c {
		t.Fatalf(f, a...)
	}
}
func mustStore(r int) *Store { s, _ := New(r); return s }

// TestEightSteps 钉住 NOTES.md 第三节八步：每步 Stable、第5/8步 GC、第6步 stale 写。
func TestEightSteps(t *testing.T) {
	s, _ := New(3)
	wst := []vv.Vector{tv(0, 0, 0), tv(0, 0, 0), tv(0, 0, 0), tv(0, 0, 0),
		tv(0, 0, 0), tv(1, 0, 0), tv(1, 1, 0), tv(1, 1, 0)}
	ops := []func(){
		func() { _ = s.Write(0, "k", "a", tv(1, 0, 0)) },
		func() { _ = s.Sync(1, tv(1, 0, 0)) },
		func() { _ = s.Delete(1, "k", tv(1, 1, 0)) },
		func() { _ = s.Sync(0, tv(1, 1, 0)) },
		func() { failIf(t, len(s.GC()) != 0, "step5 removed") },
		func() { _ = s.Write(2, "k", "stale", tv(1, 0, 0)) },
		func() { _ = s.Sync(2, tv(1, 1, 0)) },
		func() { r := s.GC(); failIf(t, len(r) != 1 || r[0] != "k", "step8 GC=%v", r) },
	}
	for i, f := range ops {
		f()
		failIf(t, !reflect.DeepEqual(s.Stable(), wst[i]), "step%d stable=%v", i+1, s.Stable())
		if i == 5 {
			e := s.store["k"]
			failIf(t, !e.Tomb || !reflect.DeepEqual(e.Vec, tv(1, 1, 0)), "step6 %+v", e)
		}
	}
	failIf(t, len(s.View()) != 0, "step8 View=%v", s.View())
	_ = s.Write(2, "k", "z", tv(0, 9, 9))
	_, ok := s.View()["k"]
	failIf(t, ok, "post-GC stale write resurrected k")
}

// refModel 是规格直译的独立参考：无堆，GC 全表扫描。
type refModel struct {
	clk  []vv.Vector
	e    map[string]Entry
	gone map[string]vv.Vector
}

func newRef(r int) *refModel {
	m := &refModel{e: map[string]Entry{}, gone: map[string]vv.Vector{}}
	for i := 0; i < r; i++ {
		m.clk = append(m.clk, make(vv.Vector, r))
	}
	return m
}
func (m *refModel) put(rep int, key string, v vv.Vector, val string, tomb bool) {
	m.clk[rep] = vv.MergeMax(m.clk[rep], v)
	if g, ok := m.gone[key]; ok && vv.Cmp(v, g) <= 0 {
		return
	}
	if c, has := m.e[key]; has && vv.Cmp(v, c.Vec) <= 0 {
		return
	}
	delete(m.gone, key)
	m.e[key] = Entry{Vec: append(vv.Vector(nil), v...), Val: val, Tomb: tomb}
}
func (m *refModel) gc() []string {
	st, r := vv.MinPointwise(m.clk), []string{}
	for k, en := range m.e {
		if en.Tomb && vv.LeqPointwise(en.Vec, st) {
			delete(m.e, k)
			m.gone[k] = append(vv.Vector(nil), en.Vec...)
			r = append(r, k)
		}
	}
	sort.Strings(r)
	return r
}

// TestViewOracle 不变量1：随机事件+随机顺序+穿插 GC，堆实现与全表重算逐字段一致。
func TestViewOracle(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		R := 2 + rng.Intn(3)
		s, m := mustStore(R), newRef(R)
		var evs []func()
		for i := 0; i < 200; i++ {
			v, k, rr := rv(rng, R), fmt.Sprintf("k%d", rng.Intn(7)), rng.Intn(R)
			switch "WDSG"[rng.Intn(4)] {
			case 'W':
				evs = append(evs, func() { _ = s.Write(rr, k, "x", v); m.put(rr, k, v, "x", false) })
			case 'D':
				evs = append(evs, func() { _ = s.Delete(rr, k, v); m.put(rr, k, v, "", true) })
			case 'S':
				evs = append(evs, func() { _ = s.Sync(rr, v); m.clk[rr] = vv.MergeMax(m.clk[rr], v) })
			case 'G':
				evs = append(evs, func() { failIf(t, !slices.Equal(s.GC(), m.gc()), "seed%d GC mismatch", seed) })
			}
		}
		rng.Shuffle(len(evs), func(i, j int) { evs[i], evs[j] = evs[j], evs[i] })
		for i, f := range evs {
			f()
			g := s.View()
			failIf(t, len(g) != len(m.e), "seed%d step%d len", seed, i)
			for k, a := range g {
				b := m.e[k]
				failIf(t, a.Val != b.Val || a.Tomb != b.Tomb || !reflect.DeepEqual(a.Vec, b.Vec), "seed%d %s", seed, k)
			}
		}
	}
}
