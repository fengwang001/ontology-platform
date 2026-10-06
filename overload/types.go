package overload

// 本文件负责：类型登记、转换规则登记（提升 / 用户定义）、提升闭包与
// 「至多一次用户定义转换」可达性的预计算，以及供无锁决议使用的不可变快照。

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Rank 是一次实参→形参转换的等级，数字越小越优。
type Rank int

const (
	// RankIdentity 恒等转换（类型相同）。
	RankIdentity Rank = 1
	// RankPromotion 仅由若干次提升构成的转换。
	RankPromotion Rank = 2
	// RankUserDefined 恰好包含一次用户定义转换，其前后各可附带至多一次提升。
	RankUserDefined Rank = 3
)

func (r Rank) String() string {
	switch r {
	case RankIdentity:
		return "identity"
	case RankPromotion:
		return "promotion"
	case RankUserDefined:
		return "user-defined"
	default:
		return fmt.Sprintf("rank(%d)", int(r))
	}
}

// ConvertKind 区分两种转换规则等级。
type ConvertKind int

const (
	// KindPromotion 提升转换，可串联；提升图必须无环。
	KindPromotion ConvertKind = iota + 1
	// KindUserDefined 用户定义转换，允许双向存在且不参与提升环检测。
	KindUserDefined
)

// kindEdge 是登记态下的一条有向转换规则（不含恒等边）。
type kindEdge struct {
	from string
	to   string
	kind ConvertKind
}

// Snapshot 是某一时刻登记状态的不可变视图；决议只读取快照，因此与登记
// 并发时，每次决议要么完整看到登记之前的集合，要么完整看到之后的集合。
type Snapshot struct {
	// 全部已登记类型。
	types map[string]struct{}
	// 提升图闭包：prom[a][b] 表示 a 可经零或若干次提升到达 b。
	// 对角元素恒为 true。
	prom map[string]map[string]bool
	// user[a][b] 表示 a 经「至多一次用户定义转换 + 其前后各一次提升」
	// 可达 b，且该路径上用户定义转换恰好一次。不含纯提升可达对。
	user map[string]map[string]bool
	// chains[a][b] 表示 a 到 b 的任意可达路径上需要串联至少两次用户定义
	// 转换。chains 与 prom/user 可能同时包含同一有序对：只要存在一条需要
	// 两次用户定义转换的路径，就不能把该候选简单当作「更差」，而应判定为
	// 不适用（串联用户定义转换）。
	chains map[string]map[string]bool
	// 同名声明按登记先后保存；决议前按稳定键排序，保证次序无关性。
	funcs map[string][]*Decl
}

func newSnapshot() *Snapshot {
	return &Snapshot{
		types:  map[string]struct{}{},
		prom:   map[string]map[string]bool{},
		user:   map[string]map[string]bool{},
		chains: map[string]map[string]bool{},
		funcs:  map[string][]*Decl{},
	}
}

func (s *Snapshot) hasType(t string) bool {
	_, ok := s.types[t]
	return ok
}

// Registry 是类型、转换规则与函数声明的登记入口。
// 所有变更串行化（mu），决议通过原子替换的不可变快照进行（无锁读）。
type Registry struct {
	mu      sync.Mutex
	types   map[string]struct{}
	edges   map[kindEdge]struct{}
	funcs   map[string][]*Decl
	current atomic.Pointer[Snapshot]
}

// NewRegistry 创建空登记表。
func NewRegistry() *Registry {
	r := &Registry{
		types: map[string]struct{}{},
		edges: map[kindEdge]struct{}{},
		funcs: map[string][]*Decl{},
	}
	r.current.Store(newSnapshot())
	return r
}

// Snapshot 原子地取得当前登记表的不可变快照。
func (r *Registry) Snapshot() *Snapshot {
	return r.current.Load()
}

// DefineType 登记一个类型；重复登记同一类型是幂等操作。
func (r *Registry) DefineType(name string) error {
	if name == "" {
		return fmt.Errorf("%w: type name is empty", ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types[name] = struct{}{}
	r.publishLocked()
	return nil
}

// AddConversion 登记一条转换规则。
// 提升规则若使某类型经若干次提升回到自身（含自环），返回 ErrPromotionCycle。
// 用户定义转换允许双向存在，不构成提升环。
func (r *Registry) AddConversion(from, to string, kind ConvertKind) error {
	if from == "" || to == "" {
		return fmt.Errorf("%w: conversion endpoint is empty", ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.hasTypeLocked(from) || !r.hasTypeLocked(to) {
		return fmt.Errorf("%w: conversion %s -> %s references an undefined type",
			ErrUndefinedType, from, to)
	}
	if kind == KindPromotion {
		// 先在临时提升闭包上检查加入该边是否成环；成环则整体拒绝。
		prom := cloneReach(r.promLocked())
		if reachBeforeAdd(prom, to, from) || to == from {
			return fmt.Errorf("%w: promotion %s -> %s closes a cycle",
				ErrPromotionCycle, from, to)
		}
	}
	if kind != KindPromotion && kind != KindUserDefined {
		return fmt.Errorf("%w: unknown conversion kind %d", ErrInvalidInput, kind)
	}
	r.edges[kindEdge{from: from, to: to, kind: kind}] = struct{}{}
	r.publishLocked()
	return nil
}

func (r *Registry) hasTypeLocked(t string) bool {
	_, ok := r.types[t]
	return ok
}

// promLocked 基于当前已登记的提升边计算自反传递闭包。
// 登记在任何成环边被加入之前即拒绝，所以此处输入恒为无环。
func (r *Registry) promLocked() map[string]map[string]bool {
	verts := sortedKeys(r.types)
	direct := map[string][]string{}
	for e := range r.edges {
		if e.kind == KindPromotion {
			direct[e.from] = append(direct[e.from], e.to)
		}
	}
	for _, v := range verts {
		sort.Strings(direct[v])
	}
	reach := map[string]map[string]bool{}
	for _, v := range verts {
		reach[v] = map[string]bool{v: true}
	}
	for _, v := range verts {
		var walk func(cur string)
		walk = func(cur string) {
			for _, nxt := range direct[cur] {
				if !reach[v][nxt] {
					reach[v][nxt] = true
					walk(nxt)
				}
			}
		}
		walk(v)
	}
	return reach
}

// userLocked 计算「一次用户定义转换，其前后各至多一次提升（提升可多步，
// 仍只占一个提升位）」的精确一次-UD 可达关系；chainsLocked 计算需要至少
// 两次 UD 才能到达的有序对。
func (r *Registry) derivedLocked(prom map[string]map[string]bool) (
	map[string]map[string]bool, map[string]map[string]bool,
) {
	verts := sortedKeys(r.types)
	oneUD := map[string]map[string]bool{}
	// preUD[a] 收集 a 经提升可达、且存在外发出的 UD 边的类型；为统一记号，
	// 这里直接枚举 UD 边并在两侧做提升闭包扩张。
	type udEdge struct{ x, y string }
	var uds []udEdge
	for e := range r.edges {
		if e.kind == KindUserDefined {
			uds = append(uds, udEdge{e.from, e.to})
		}
	}
	sort.Slice(uds, func(i, j int) bool {
		if uds[i].x != uds[j].x {
			return uds[i].x < uds[j].x
		}
		return uds[i].y < uds[j].y
	})
	add := func(m map[string]map[string]bool, a, b string) {
		if m[a] == nil {
			m[a] = map[string]bool{}
		}
		m[a][b] = true
	}
	// 一次 UD：a -提升*-> x -(UD)-> y -提升*-> b。
	for _, e := range uds {
		for _, a := range verts {
			if !prom[a][e.x] {
				continue
			}
			for _, b := range verts {
				if prom[e.y][b] && !(a == b && prom[a][b]) {
					// 排除纯提升即可到达的情况（该情况保持 promotion 等级）。
					if !prom[a][b] {
						add(oneUD, a, b)
					}
				}
			}
		}
	}
	// 两次及以上 UD 的串联可达：以「一次 UD 步」为边做组合闭包。
	step := oneUD // 一步恰好一次 UD
	twoPlus := map[string]map[string]bool{}
	// cur[n][a][b]：恰好 n 次 UD 的可达。迭代到不再增长。
	cur := cloneReach(step)
	accum := cloneReach(step)
	for {
		nxt := map[string]map[string]bool{}
		for a, tos := range cur {
			for m := range tos {
				for b := range step[m] {
					add(nxt, a, b)
					if !prom[a][b] {
						add(twoPlus, a, b)
					}
				}
			}
		}
		if reachSame(accum, nxt) {
		}
		grew := false
		for a, tos := range nxt {
			for b := range tos {
				if accum[a] == nil || !accum[a][b] {
					grew = true
					add(accum, a, b)
				}
			}
		}
		cur = nxt
		if !grew {
			break
		}
	}
	return oneUD, twoPlus
}

// publishLocked 在互斥区外构造不可变快照并原子发布；登记失败的调用不会
// 执行到这里，因此被拒绝的登记不可能改变候选集合。
func (r *Registry) publishLocked() {
	prom := r.promLocked()
	user, chains := r.derivedLocked(prom)
	snap := &Snapshot{
		types:  cloneSet(r.types),
		prom:   prom,
		user:   user,
		chains: chains,
		funcs:  map[string][]*Decl{},
	}
	for name, ds := range r.funcs {
		cp := make([]*Decl, len(ds))
		copy(cp, ds)
		snap.funcs[name] = cp
	}
	r.current.Store(snap)
}

// reachBeforeAdd 判断在当前闭包 reach 中 from->to 是否已经可达。
func reachBeforeAdd(reach map[string]map[string]bool, from, to string) bool {
	return reach[from] != nil && reach[from][to]
}

func cloneReach(in map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(in))
	for k, v := range in {
		cp := make(map[string]bool, len(v))
		for kk := range v {
			cp[kk] = true
		}
		out[k] = cp
	}
	return out
}

func cloneSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}
func reachSame(a, b map[string]map[string]bool) bool { return len(a) == len(b) }

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
