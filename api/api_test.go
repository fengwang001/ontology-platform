package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/api"
)

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

// refModel 是朴素参照：扫描全部持有资源，算全局天花板/他人天花板/有效优先级。
type refModel struct {
	prio, ceil map[string]int
	hold       map[string]string
}

func newRef() *refModel { return &refModel{map[string]int{}, map[string]int{}, map[string]string{}} }
func (p *refModel) vals(t string) (sys, oth, ep int) {
	ep = p.prio[t]
	for x, h := range p.hold {
		c := p.ceil[x]
		sys = max(sys, c)
		if h != t {
			oth = max(oth, c)
		} else {
			ep = max(ep, c)
		}
	}
	return
}
func eight(t *testing.T) *api.Manager {
	t.Helper()
	m := api.New()
	m.AddTask("T1", 5)
	m.AddTask("T2", 3)
	m.AddTask("T3", 1)
	m.AddResource("R_x")
	m.AddResource("R_y")
	for _, u := range [][2]string{{"T1", "R_x"}, {"T3", "R_x"}, {"T2", "R_y"}, {"T3", "R_y"}} {
		must(t, m.Use(u[0], u[1]))
	}
	return m
}

// TestNaiveReference 钉住不变量1：只在已声明 Use 的对上请求，每次 Acquire 与朴素扫描一致。
func TestNaiveReference(t *testing.T) {
	for seed := int64(0); seed < 4; seed++ {
		m, p, rd := api.New(), newRef(), rand.New(rand.NewSource(seed))
		n := 4 + int(seed%4)
		ids, res := make([]string, n), make([]string, n)
		for i := 0; i < n; i++ {
			ids[i], res[i] = fmt.Sprintf("t%d", i), fmt.Sprintf("r%d", i)
			p.prio[ids[i]] = 1 + rd.Intn(9)
			m.AddTask(ids[i], p.prio[ids[i]])
			m.AddResource(res[i])
		}
		seen := map[string]bool{}
		var pairs [][2]string
		for k := 0; k < 3*n; k++ {
			tk, x := ids[rd.Intn(n)], res[rd.Intn(n)]
			if seen[tk+x] {
				continue
			}
			seen[tk+x], pairs = true, append(pairs, [2]string{tk, x})
			must(t, m.Use(tk, x))
			p.ceil[x] = max(p.ceil[x], p.prio[tk])
		}
		for step := 0; step < 150; step++ {
			q := pairs[rd.Intn(len(pairs))]
			tk, x := q[0], q[1]
			if p.hold[x] == tk {
				continue
			}
			if h := p.hold[x]; h != "" && rd.Intn(2) == 0 {
				must(t, m.Release(h, x))
				delete(p.hold, x)
				continue
			}
			_, oth, _ := p.vals(tk)
			got, e := m.Acquire(tk, x)
			must(t, e)
			if got != (p.prio[tk] > oth) {
				t.Fatalf("seed=%d step=%d grant mismatch", seed, step)
			}
			if got {
				p.hold[x] = tk
			}
			sys, _, ep := p.vals(tk)
			if m.SystemCeiling() != sys || m.EffectivePriority(tk) != ep {
				t.Fatalf("seed=%d step=%d ceiling/ep drift", seed, step)
			}
		}
	}
}

// TestEightStepSequence 钉住不变量2/3：天花板、第1步授予/抬升，并跑内置完整八步 SelfCheck。
func TestEightStepSequence(t *testing.T) {
	m := eight(t)
	if m.Ceiling("R_x") != 5 || m.Ceiling("R_y") != 3 {
		t.Fatal("ceiling")
	}
	if g, e := m.Acquire("T3", "R_x"); !g || e != nil ||
		m.SystemCeiling() != 5 || m.EffectivePriority("T3") != 5 {
		t.Fatal("step1 grant/ceiling/effective-priority")
	}
	must(t, m.Release("T3", "R_x"))
	must(t, m.SelfCheck()) // SelfCheck 在全新实例上逐行对拍完整八步并核验四不变量
}

// TestSentinelErrorsNoTrace 钉住不变量4：四类错误互不相同，被拒后状态不变且可继续使用。
func TestSentinelErrorsNoTrace(t *testing.T) {
	m := eight(t)
	rej := func(want error, f func() error) {
		if e := f(); !errors.Is(e, want) {
			t.Fatalf("got %v want %v", e, want)
		}
	}
	rej(api.ErrUnknownTask, func() error { _, e := m.Acquire("??", "R_x"); return e })
	rej(api.ErrUnknownResource, func() error { _, e := m.Acquire("T1", "??"); return e })
	rej(api.ErrUnknownTask, func() error { return m.Use("??", "R_x") })
	rej(api.ErrUnknownResource, func() error { return m.Release("T1", "??") })
	if m.SystemCeiling() != 0 {
		t.Fatal("trace after rejects")
	}
	if g, e := m.Acquire("T1", "R_x"); !g || e != nil {
		t.Fatalf("grant %v %v", g, e)
	}
	rej(api.ErrAlreadyHeld, func() error { _, e := m.Acquire("T1", "R_x"); return e })
	rej(api.ErrNotHeld, func() error { return m.Release("T2", "R_x") })
	if m.SystemCeiling() != 5 || m.EffectivePriority("T1") != 5 {
		t.Fatal("state changed after rejects")
	}
	if e := m.Release("T1", "R_x"); e != nil || m.SystemCeiling() != 0 {
		t.Fatalf("not usable after rejects: %v", e)
	}
	a := [...]error{api.ErrUnknownTask, api.ErrUnknownResource, api.ErrAlreadyHeld, api.ErrNotHeld}
	for i := 0; i < 3; i++ {
		if errors.Is(a[i], a[i+1]) {
			t.Fatal("sentinels not distinct")
		}
	}
}
