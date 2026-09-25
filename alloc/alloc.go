// Package alloc 在 drf 的单任务判定之上做多任务 DRF 分配：主导份额相等化、
// 硬约束推进与绑定约束定位。只依赖 drf，反向依赖不允许。
package alloc

import "ontology/drf"

// Task 是一个待分配任务：单位需求向量 (CPU,Mem)。
type Task struct {
	ID  string
	CPU int64
	Mem int64
}

// group 聚合同需求向量的任务：主导份额 β 与单位份额资源系数 γ 恒等，
// 水位推进中一个组只需一个代表参与定位（增量聚合，不逐个重扫任务）。
type group struct {
	cpu, mem   int64
	n          int64
	ids        []string
	active     bool
	hit        bool // 本次定位是否考察过该组代表
	beta       drf.Frac
	gamC, gamM drf.Frac // 每单位主导份额消耗的 CPU / Mem
}

// Allocator 保存容量与任务集合。
type Allocator struct {
	cc       int64
	cm       int64
	groups   []*group
	examined int // 非导出计数器：最近一次 Allocate 份额定位考察过的组代表数
}

// New 创建容量为 (cc,cm) 的分配器；容量合法性由 api 层校验。
func New(cc, cm int64) *Allocator { return &Allocator{cc: cc, cm: cm} }

// Add 增量加入一个合法任务（合法性由 api 层校验）：同需求向量即并入已有组。
func (a *Allocator) Add(t Task) {
	for _, g := range a.groups {
		if g.cpu == t.CPU && g.mem == t.Mem {
			g.n++
			g.ids = append(g.ids, t.ID)
			return
		}
	}
	a.groups = append(a.groups, &group{cpu: t.CPU, mem: t.Mem, n: 1, ids: []string{t.ID}})
}

func intFrac(v int64) drf.Frac { return drf.Frac{N: v, D: 1} }
func zeroFrac() drf.Frac       { return drf.Frac{N: 0, D: 1} }

// coeff 计算 β=主导资源比例，以及份额 s（单位数 a=s/β）下的单位份额资源消耗
// γ_r=demand_r/β：主导资源上恒等于其容量，另一资源按比例，零需求为 0。
func (g *group) coeff(cc, cm int64) {
	if drf.Dominant(g.cpu, g.mem, cc, cm) == drf.CPU {
		g.beta, _ = drf.NewFrac(g.cpu, cc)
		g.gamC, g.gamM = intFrac(cc), zeroFrac()
		if g.mem > 0 {
			g.gamM, _ = drf.NewFrac(g.mem*cc, g.cpu)
		}
	} else {
		g.beta, _ = drf.NewFrac(g.mem, cm)
		g.gamC, g.gamM = zeroFrac(), intFrac(cm)
		if g.cpu > 0 {
			g.gamC, _ = drf.NewFrac(g.cpu*cm, g.mem)
		}
	}
}

// fill 是共享的水位推进核心：主导份额从 0 起逐段抬升，每段遍历全部活跃组
// 求单位份额聚合需求 S_r，取最先耗尽的资源为绑定约束；绑定资源上边际需求为正
// 的组在该水位冻结（其主导资源必在其中），对绑定资源零需求的组继续抬升。
// 返回 id→单位数与本次考察过的组代表数。每任务一个组即等价于逐任务重扫的朴素算法。
func fill(gs []*group, cc, cm int64) (map[string]drf.Frac, int) {
	for _, g := range gs {
		g.coeff(cc, cm)
		g.active, g.hit = true, false
	}
	res := map[string]drf.Frac{}
	level, uc, um := zeroFrac(), zeroFrac(), zeroFrac()
	for {
		sC, sM := zeroFrac(), zeroFrac()
		for _, g := range gs {
			if g.active {
				g.hit = true
				sC = drf.Add(sC, drf.Mul(intFrac(g.n), g.gamC))
				sM = drf.Add(sM, drf.Mul(intFrac(g.n), g.gamM))
			}
		}
		if drf.Cmp(sC, zeroFrac()) == 0 && drf.Cmp(sM, zeroFrac()) == 0 {
			break
		}
		dC := drf.Div(drf.Sub(intFrac(cc), uc), sC)
		dM := drf.Div(drf.Sub(intFrac(cm), um), sM)
		ds, bindC, bindM := dM, false, true
		if drf.Cmp(sC, zeroFrac()) > 0 && drf.Cmp(sM, zeroFrac()) > 0 {
			if drf.Cmp(dC, dM) < 0 { // 相等时记 Mem 绑定，纯 CPU 组下一段继续
				ds, bindC, bindM = dC, true, false
			}
		} else if drf.Cmp(sC, zeroFrac()) > 0 {
			ds, bindC = dC, true
		}
		level = drf.Add(level, ds)
		uc = drf.Add(uc, drf.Mul(sC, ds))
		um = drf.Add(um, drf.Mul(sM, ds))
		for _, g := range gs {
			if g.active && ((bindC && g.cpu > 0) || (bindM && g.mem > 0)) {
				for _, id := range g.ids {
					res[id] = drf.Div(level, g.beta)
				}
				g.active = false
			}
		}
	}
	n := 0
	for _, g := range gs {
		if g.hit {
			n++
		}
	}
	return res, n
}

// Allocate 返回每个任务 id 的精确分配单位数 a_i；任务在 Add 时已按需求向量
// 增量聚合，故定位只遍历组代表，不逐个重扫。
func (a *Allocator) Allocate() map[string]drf.Frac {
	res, n := fill(a.groups, a.cc, a.cm)
	a.examined = n
	return res
}

// NaiveAllocate 是朴素参照：每个任务单独成组（不聚合），fill 每段都对全部
// 任务逐个重扫求聚合需求、逐段抬升主导份额直到硬约束耗尽。仅用于与 Allocate 对照。
func NaiveAllocate(cc, cm int64, tasks []Task) map[string]drf.Frac {
	gs := make([]*group, len(tasks))
	for i, t := range tasks {
		gs[i] = &group{cpu: t.CPU, mem: t.Mem, n: 1, ids: []string{t.ID}}
	}
	res, _ := fill(gs, cc, cm)
	return res
}
