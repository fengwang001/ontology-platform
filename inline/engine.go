package inline

import (
	"sort"
	"strconv"
)

// bodySite 是（最终或原始）函数体中一个幸存调用点。
type bodySite struct {
	callee  string
	hotness float64
}

// body 是某一时刻可复制的函数体：当前尺寸与幸存调用点。
type body struct {
	size  int64
	sites []bodySite
}

// bodyNode 是展开中的函数体节点；内联后其位置被子节点取代。
type bodyNode struct {
	site     bodySite
	inlined  bool
	children []*bodyNode
}

// workItem 是待考察的调用点。
type workItem struct {
	id      string
	callee  string
	hotness float64
	pos     int
	path    chain
	node    *bodyNode
}

// lessItem 定义考察次序：热度降序，其次出现位置升序，最后按标识升序，
// 三者共同保证任何环境下次序完全确定。
func lessItem(a, b *workItem) bool {
	if a.hotness != b.hotness {
		return a.hotness > b.hotness
	}
	if a.pos != b.pos {
		return a.pos < b.pos
	}
	return a.id < b.id
}

// insertItem 把新调用点按考察次序插入工作表，而非追加到末尾。
func insertItem(wl []*workItem, it *workItem) []*workItem {
	i := sort.Search(len(wl), func(i int) bool { return lessItem(it, wl[i]) })
	wl = append(wl, nil)
	copy(wl[i+1:], wl[i:])
	wl[i] = it
	return wl
}

// Decide 对会话快照执行一次完整的内联决策，输出确定性报告。
// Decide 不修改会话与注册表，可并发调用。
func Decide(s *Session, cfg Config) (*Report, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	e := &engine{prog: s.Program(), cfg: cfg, bodies: make(map[string]body)}
	return e.run(), nil
}

type engine struct {
	prog   *Program
	cfg    Config
	bodies map[string]body // 已完成根的当前函数体
	stats  Stats
}

// currentBody 返回被调函数当前的函数体：已完成决策的函数给出
// 最终尺寸与幸存调用点，否则给出初始尺寸与原始调用点。
func (e *engine) currentBody(name string) body {
	if b, ok := e.bodies[name]; ok {
		return b
	}
	f, _ := e.prog.Lookup(name)
	sites := make([]bodySite, len(f.CallSites))
	for i, cs := range f.CallSites {
		sites[i] = bodySite{callee: cs.Callee, hotness: cs.Hotness}
	}
	return body{size: f.Size, sites: sites}
}

func (e *engine) run() *Report {
	rep := &Report{}
	for _, name := range e.prog.order {
		rep.Functions = append(rep.Functions, e.decideRoot(name))
	}
	rep.Stats = e.stats
	return rep
}

// check 按固定优先级判定一个调用点，返回第一个成立的拒绝原因；
// 返回 RejectNone 时同时给出应复制的函数体。
func (e *engine) check(it *workItem, acc *accountant) (RejectReason, body) {
	f, ok := e.prog.funcs[it.callee]
	if !ok {
		return RejectUndefined, body{}
	}
	if f.Marks.NoInline {
		return RejectNoInline, body{}
	}
	if it.callee == it.path.top() {
		return RejectDirectRecursion, body{}
	}
	if f.Marks.NonInlinableStructure {
		return RejectNonInlinableStructure, body{}
	}
	e.stats.ChainScanSteps += int64(len(it.path))
	if it.path.count(it.callee) >= e.cfg.MaxChainRepeat {
		return RejectChainLimit, body{}
	}
	b := e.currentBody(it.callee)
	if !f.Marks.AlwaysInline {
		e.stats.BudgetChecks++
		if !acc.withinBudget(b.size - e.cfg.CallOverhead) {
			return RejectBudget, body{}
		}
	}
	return RejectNone, b
}

func (e *engine) decideRoot(name string) FunctionReport {
	f, _ := e.prog.Lookup(name)
	acc := newAccountant(e.cfg, f.Size)
	fr := FunctionReport{Name: name, InitialSize: f.Size, DeepestPath: []string{name}}

	var roots []*bodyNode
	var wl []*workItem
	for i, cs := range f.CallSites {
		n := &bodyNode{site: bodySite{callee: cs.Callee, hotness: cs.Hotness}}
		roots = append(roots, n)
		wl = insertItem(wl, &workItem{
			id:      name + "#" + strconv.Itoa(i),
			callee:  cs.Callee,
			hotness: cs.Hotness,
			pos:     i,
			path:    chain{name},
			node:    n,
		})
	}

	for len(wl) > 0 {
		it := wl[0]
		wl = wl[1:]
		reason, b := e.check(it, acc)
		if reason != RejectNone {
			fr.Decisions = append(fr.Decisions, CallDecision{
				ID: it.id, Callee: it.callee, Hotness: it.hotness,
				Path: []string(it.path), Inlined: false, Reason: reason,
			})
			continue
		}
		delta := b.size - e.cfg.CallOverhead
		acc.apply(delta)
		fr.Decisions = append(fr.Decisions, CallDecision{
			ID: it.id, Callee: it.callee, Hotness: it.hotness,
			Path: []string(it.path), Inlined: true,
		})
		it.node.inlined = true
		p2 := it.path.extend(it.callee)
		if len(p2) > len(fr.DeepestPath) {
			fr.DeepestPath = []string(p2)
		}
		// 复制进来的调用点按缩放后的热度插入工作表重新排序。
		for k, s := range b.sites {
			child := &bodyNode{site: bodySite{callee: s.callee, hotness: it.hotness * s.hotness}}
			it.node.children = append(it.node.children, child)
			wl = insertItem(wl, &workItem{
				id:      it.id + "/" + it.callee + "#" + strconv.Itoa(k),
				callee:  s.callee,
				hotness: child.site.hotness,
				pos:     k,
				path:    p2,
				node:    child,
			})
		}
	}

	fr.FinalSize = acc.size
	fr.surviving = flattenSurviving(roots)
	e.bodies[name] = body{size: acc.size, sites: fr.surviving}
	return fr
}

// flattenSurviving 按最终函数体顺序收集幸存调用点：
// 被内联的位置由其复制体的幸存调用点递归取代。
func flattenSurviving(roots []*bodyNode) []bodySite {
	var out []bodySite
	var walk func(n *bodyNode)
	walk = func(n *bodyNode) {
		if !n.inlined {
			out = append(out, n.site)
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, n := range roots {
		walk(n)
	}
	return out
}
