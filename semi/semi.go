// Package semi 实现只含一条递归规则（传递闭包）的半朴素增量迭代引擎。
// 依赖方向：semi → rel，不依赖任何其他包。
package semi

import (
	"sort"
	"strconv"
	"sync"

	"ontology/rel"
)

// Row 是某一轮结束后的只读轨迹（不含任何累计计数器）。
type Row struct {
	Round                  int
	Delta, Dropped         []rel.T
	Cands, Added, PathSize int
}

// Engine 是一次求值的全部内存状态：结点 intern 成 id，(x,y) 打包为 uint64，
// gen 的键集即 path，值是该元组首次被导出时的轮次戳。
type Engine struct {
	out       [][]int32 // 邻接表，按源点 id 索引
	names     []string  // id → 原始结点名
	base      []uint64  // 初始边打包键（Delta0）
	mu        sync.RWMutex
	gen       map[uint64]uint32
	rows      []Row
	trace     bool
	candTotal int // 非导出：整个求值 join 产生的候选元组总数
	done      bool
}

// New 用给定边集构造引擎（调用方负责输入合法性校验），求值时记录每轮轨迹。
func New(edges []rel.T) *Engine {
	e := &Engine{trace: true}
	ids := map[string]int32{}
	intern := func(s string) int32 {
		if id, ok := ids[s]; ok {
			return id
		}
		id := int32(len(e.names))
		ids[s], e.names = id, append(e.names, s)
		e.out = append(e.out, nil)
		return id
	}
	e.base = make([]uint64, 0, len(edges))
	for _, t := range edges {
		x, y := intern(t.X), intern(t.Y)
		e.out[x] = append(e.out[x], y)
		e.base = append(e.base, pack(x, y))
	}
	return e
}

// Eval 执行半朴素迭代至不动点，返回按 (X,Y) 字典序排序的 path；重复调用幂等。
func (e *Engine) Eval() []rel.T {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evalLocked()
	keys := make([]uint64, 0, len(e.gen))
	for k := range e.gen {
		keys = append(keys, k)
	}
	return e.dump(keys)
}

// evalLocked 跑迭代至不动点（调用方持锁）；trace 关闭时不保存逐轮轨迹。
func (e *Engine) evalLocked() {
	if e.done {
		return
	}
	e.gen = make(map[uint64]uint32, len(e.base)*2) // 第 0 轮：path = Delta0 = edge，戳 1
	delta := make([]uint64, len(e.base))
	for i, k := range e.base {
		e.gen[k], delta[i] = 1, k
	}
	if e.trace {
		e.rows = append(e.rows, Row{Round: 0, Delta: e.dump(delta), Added: len(delta), PathSize: len(e.gen)})
	}
	for round := uint32(2); ; round++ {
		var fresh, dropped []uint64 // 候选 = Delta_{k-1} ∘ edge，只沿上轮新增传播
		cands := 0
		for _, k := range delta {
			z, x := int32(k), int32(k>>32)
			for _, y := range e.out[z] {
				nk := pack(x, y)
				if e.gen[nk] == round {
					continue // 本轮已由别的中间点产出：候选集合内去重
				}
				cands++
				if e.gen[nk] != 0 {
					if e.trace {
						dropped = append(dropped, nk) // 已在 path：去重丢弃
					}
				} else {
					fresh = append(fresh, nk)
				}
				e.gen[nk] = round
			}
		}
		e.candTotal += cands
		if e.trace {
			e.rows = append(e.rows, Row{Round: len(e.rows), Delta: e.dump(fresh),
				Dropped: e.dump(dropped), Cands: cands, Added: len(fresh), PathSize: len(e.gen)})
		}
		if len(fresh) == 0 { // 末轮 Delta 为空 → 不动点
			break
		}
		delta = fresh
	}
	e.done = true
}

// Rows 返回各轮轨迹的独立副本，供求值完成后并发读取。
func (e *Engine) Rows() []Row {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]Row(nil), e.rows...)
}

// VerifyChain 构造 m 条边的链 a1→…→a_{m+1}，核验候选总数恰为 m(m-1)/2。
// 只返回布尔判定，不经过任何导出途径暴露计数器数值；不留轨迹不排序，可上万规模。
func VerifyChain(m int) bool {
	edges := make([]rel.T, 0, m)
	for i := 1; i <= m; i++ {
		edges = append(edges, rel.T{X: "a" + strconv.Itoa(i), Y: "a" + strconv.Itoa(i+1)})
	}
	e := New(edges)
	e.trace = false
	e.mu.Lock()
	e.evalLocked()
	got, pathSize := e.candTotal, len(e.gen)
	e.mu.Unlock()
	return got == m*(m-1)/2 && pathSize == m*(m+1)/2
}

func pack(x, y int32) uint64 { return uint64(uint32(x))<<32 | uint64(uint32(y)) }

// dump 把打包键切片转回 rel.T 并按原始结点名字典序排序。
func (e *Engine) dump(keys []uint64) []rel.T {
	out := make([]rel.T, len(keys))
	for i, k := range keys {
		out[i] = rel.T{X: e.names[int32(k>>32)], Y: e.names[int32(k)]}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].X < out[j].X || out[i].X == out[j].X && out[i].Y < out[j].Y
	})
	return out
}
