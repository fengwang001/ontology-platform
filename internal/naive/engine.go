package naive

import (
	"fmt"
	"sort"
	"sync"
)

// Engine 是朴素参考引擎：一把全局锁串行一切，
// 每一步即时生效并压入逆操作栈；任何失败立即按逆序撤销整批。
type Engine struct {
	mu    sync.Mutex
	spec  *Spec
	inst  map[string]*Instance
	link  map[Link]bool
	clock int64
}

func NewEngine(spec *Spec) *Engine {
	return &Engine{spec: spec, inst: map[string]*Instance{}, link: map[Link]bool{}}
}

func (e *Engine) AddInstance(in *Instance) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := *in
	if c.Attrs == nil {
		c.Attrs = map[string]Value{}
	}
	e.inst[c.ID] = &c
}

func (e *Engine) State(id string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.inst[id]
	if !ok {
		return "", false
	}
	return in.State, true
}

func (e *Engine) Attr(id, name string) (Value, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.inst[id]
	if !ok {
		return nil, false
	}
	v, ok := in.Attrs[name]
	return v, ok
}

func (e *Engine) HasLink(l Link) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.link[l]
}

// PeersOf 返回实例经 linkType 出向连接的对端（排序）。
func (e *Engine) PeersOf(id, linkType string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := &runner{e: e}
	return r.peers(id, linkType)
}

// SeedLink 铺底初始链接，绕过终态校验（预存链接不受“新增”限制）。
func (e *Engine) SeedLink(typ, from, to string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.link[Link{Type: typ, From: from, To: to}] = true
}

func (e *Engine) Clock(id string) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inst[id].Clock
}

// Batch 原子执行整批操作。
func (e *Engine) Batch(ops []Op) *BatchResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	r := &runner{
		e:       e,
		mutex:   map[string]map[string]int{},
		results: make([]OpResult, len(ops)),
	}

	for i, op := range ops {
		var err *OpError
		var fired []Fired
		switch op.Kind {
		case OpFire:
			fired, err = r.fire(op.InstanceID, op.Transition, "", i, map[string]bool{})
		case OpSetAttr:
			err = r.setAttr(op)
		case OpAddLink:
			err = r.addLink(op.Link)
		case OpDelLink:
			err = r.delLink(op.Link)
		default:
			err = &OpError{Code: ErrUnknown, InstanceID: op.InstanceID, Detail: "未知操作"}
		}
		if err != nil {
			// 被拒操作此前环节已通过本批 undo 栈立即反向撤销；
			// 为与生产引擎“整批报告每操作判定”的约定对齐，失败位置之后不再执行。
			for j := i; j < len(ops); j++ {
				r.results[j] = OpResult{}
			}
			r.results[i] = OpResult{Err: err}
			return &BatchResult{Committed: false, Ops: r.results}
		}
		r.results[i] = OpResult{OK: true, Fired: fired}
	}

	e.clock++
	touched := map[string]bool{}
	for _, op := range ops {
		if op.Kind == OpFire {
			for _, fr := range r.resultsFire(op) {
				touched[fr] = true
			}
		}
		if op.Kind == OpSetAttr {
			touched[op.InstanceID] = true
		}
		if op.Kind == OpAddLink || op.Kind == OpDelLink {
			touched[op.Link.From] = true
			touched[op.Link.To] = true
		}
	}
	for id := range touched {
		if in, ok := e.inst[id]; ok {
			in.Clock = e.clock
		}
	}
	return &BatchResult{Committed: true, Ops: r.results}
}

func (r *runner) resultsFire(op Op) []string {
	return []string{op.InstanceID}
}

// runner 承载一次批处理的 undo 栈与互斥占用表。
type runner struct {
	e       *Engine
	undos   []func()
	mutex   map[string]map[string]int
	results []OpResult
}

func (r *runner) pushUndo(fn func()) { r.undos = append(r.undos, fn) }

func (r *runner) rollback() {
	for i := len(r.undos) - 1; i >= 0; i-- {
		r.undos[i]()
	}
	r.undos = nil
}

func (r *runner) fail(code ErrCode, id, detail string) *OpError {
	r.rollback()
	return &OpError{Code: code, InstanceID: id, Detail: detail}
}

func better(a, b ErrCode) ErrCode {
	pa, pb := codePriority(a), codePriority(b)
	if pb < pa {
		return b
	}
	return a
}

func codePriority(c ErrCode) int {
	switch c {
	case ErrUnknown:
		return 0
	case ErrPrecondition:
		return 1
	case ErrMutex:
		return 2
	case ErrCardinality:
		return 3
	case ErrHook:
		return 4
	case ErrCycle:
		return 5
	case ErrTerminal:
		return 6
	default:
		return 7
	}
}

func (r *runner) setAttr(op Op) *OpError {
	in := r.e.inst[op.InstanceID]
	if in == nil {
		return r.fail(ErrUnknown, op.InstanceID, "实例不存在")
	}
	if r.e.spec.Types[in.Type].Final[in.State] {
		return r.fail(ErrTerminal, in.ID, "终态实例拒绝改属性")
	}
	old, had := in.Attrs[op.Attr]
	in.Attrs[op.Attr] = op.Value
	id, key := in.ID, op.Attr
	r.pushUndo(func() {
		if !had {
			delete(r.e.inst[id].Attrs, key)
		} else {
			r.e.inst[id].Attrs[key] = old
		}
	})
	return nil
}

func (r *runner) addLink(l Link) *OpError {
	from, to := r.e.inst[l.From], r.e.inst[l.To]
	if from == nil || to == nil {
		return r.fail(ErrUnknown, l.From, "链接端点不存在")
	}
	if r.e.spec.Types[from.Type].Final[from.State] ||
		r.e.spec.Types[to.Type].Final[to.State] {
		return r.fail(ErrTerminal, l.From, "终态实例拒绝新增链接")
	}
	if r.e.link[l] {
		return nil
	}
	r.e.link[l] = true
	r.pushUndo(func() { delete(r.e.link, l) })
	return nil
}

func (r *runner) delLink(l Link) *OpError {
	if _, ok := r.e.inst[l.From]; !ok {
		return r.fail(ErrUnknown, l.From, "链接端点不存在")
	}
	if _, ok := r.e.inst[l.To]; !ok {
		return r.fail(ErrUnknown, l.To, "链接端点不存在")
	}
	if !r.e.link[l] {
		return nil
	}
	delete(r.e.link, l)
	r.pushUndo(func() { r.e.link[l] = true })
	return nil
}

func (r *runner) peers(id, linkType string) []string {
	var out []string
	for l := range r.e.link {
		if l.From == id && l.Type == linkType {
			out = append(out, l.To)
		}
	}
	sort.Strings(out)
	return out
}

func (r *runner) linkCount(id, linkType string) int {
	n := 0
	for l := range r.e.link {
		if l.From == id && l.Type == linkType {
			n++
		}
	}
	return n
}

func inList(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// fire 执行一次迁移（含级联）。
// 朴素做法：即时改状态 -> 递归级联 -> 迁移后基数与钩子校验；
// 任一步失败立即触发整棵触发树（含此前所有操作）的反向撤销。
// path 记录当前递归路径，用于在任何状态继续扩散前检测循环。
func (r *runner) fire(id, transName, cascadeOf string, opIdx int,
	path map[string]bool) ([]Fired, *OpError) {
	in := r.e.inst[id]
	if in == nil {
		return nil, r.fail(ErrUnknown, id, "实例不存在")
	}
	ot := r.e.spec.Types[in.Type]
	tr := ot.Transitions[transName]
	if tr == nil || tr.From != in.State {
		return nil, r.fail(ErrUnknown, id,
			fmt.Sprintf("状态 %s 下未声明迁移 %s", in.State, transName))
	}
	if ot.Final[in.State] {
		return nil, r.fail(ErrTerminal, id, "终态实例拒绝迁移")
	}

	// 循环检测先于任何状态改变。
	if path[id] {
		return nil, r.fail(ErrCycle, id, "链式触发形成循环")
	}

	// 前置条件（迁移前真实状态）。
	for _, p := range tr.Preconds {
		if !r.precond(in, p) {
			return nil, r.fail(ErrPrecondition, id, "前置条件不成立")
		}
	}

	// 互斥仲裁（批内显式顺序为优先顺序）。
	if tr.MutexGroupID != "" {
		if holder := r.mutex[id]; holder != nil {
			if by, ok := holder[tr.MutexGroupID]; ok && by != opIdx {
				return nil, r.fail(ErrMutex, id,
					fmt.Sprintf("互斥组 %s 已被操作 %d 占用", tr.MutexGroupID, by))
			}
		}
	}

	// 即时生效状态迁移，并记录逆操作。
	old := in.State
	in.State = tr.To
	r.pushUndo(func() { r.e.inst[id].State = old })
	fired := []Fired{{InstanceID: id, Transition: tr.Name, From: old, To: tr.To,
		CascadeOf: cascadeOf}}
	if tr.MutexGroupID != "" {
		if r.mutex[id] == nil {
			r.mutex[id] = map[string]int{}
		}
		r.mutex[id][tr.MutexGroupID] = opIdx
	}

	// 递归级联。
	newPath := map[string]bool{}
	for k := range path {
		newPath[k] = true
	}
	newPath[id] = true
	for _, c := range tr.Cascades {
		for _, pid := range r.peers(id, c.LinkType) {
			pst := r.e.inst[pid].State
			if !inList(c.WhenStates, pst) {
				continue
			}
			cf, err := r.fire(pid, c.Transition, id, opIdx, newPath)
			if err != nil {
				return nil, err
			}
			fired = append(fired, cf...)
		}
	}

	// 迁移后基数校验（此刻整棵树已生效，看到的是迁移后真实链接状态）。
	for _, c := range tr.MaxCard {
		if n := r.linkCount(id, c.LinkType); n > c.Max {
			return nil, r.fail(ErrCardinality, id,
				fmt.Sprintf("迁移后基数 %d 超过 %d", n, c.Max))
		}
	}
	// 跨实例钩子。
	for _, h := range tr.Hooks {
		for _, pid := range r.peers(id, h.LinkType) {
			pst := r.e.inst[pid].State
			if !inList(h.States, pst) {
				return nil, r.fail(ErrHook, id,
					fmt.Sprintf("钩子拒绝：对端 %s 状态 %s", pid, pst))
			}
		}
	}
	return fired, nil
}

func (r *runner) precond(in *Instance, p Precondition) bool {
	switch p.Kind {
	case PrecondAttr:
		v, ok := in.Attrs[p.Attr]
		return ok && v == p.Equals
	case PrecondLinkCount:
		n := r.linkCount(in.ID, p.LinkType)
		return (p.Min < 0 || n >= p.Min) && (p.Max < 0 || n <= p.Max)
	case PrecondPeerState:
		for _, pid := range r.peers(in.ID, p.LinkType) {
			if !inList(p.States, r.e.inst[pid].State) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
