package genericinst

import "sync"

// Body 是实例化体：实例登记成功后执行，内部可通过 Session 嵌套请求别的定义。
// body 返回非 nil 错误会使本次顶层请求连同其全部中间实例一起撤销。
type Body func(sess *Session) error

// Session 表示一次顶层请求持有的嵌套上下文（复用同一个串行临界区）。
type Session struct {
	r       *Registry
	depth   int
	parent  int              // 直接外层实例 id；顶层会话为 -1
	created map[int]struct{} // 本次请求新建的实例；失败时整体撤销
	hits    int              // 本次请求（含嵌套）命中已有实例次数；失败时还原
}

// Request 在实例化体内嵌套请求另一个定义的实例，并登记 parent -> 目标 的依赖边。
func (s *Session) Request(defName string, args []Type, body Body) (inst *Instance, err *RequestError) {
	inst, err = s.r.requestLocked(s, defName, args, body)
	return inst, err
}

// Registry 是泛型实例化登记中心。所有方法可被多个编译单元并发调用，
// 对外呈现严格串行化语义（等价于某一种全局串行顺序）。
type Registry struct {
	mu sync.Mutex

	defs      map[string]*Def
	instances map[int]*Instance
	index     map[string]int // "def\x00argsKey" -> 仅有效实例 id；命中查找 O(1)
	nextID    int

	totalHits    int
	totalCreates int

	maxInstances int // 实例总数上限（过期实例在清理前仍占用）
	maxPerDef    int // 单定义实例数上限
	maxDepth     int // 允许的最大嵌套深度（顶层请求深度为 1）

	// 内部计数器，供复杂度测试做白盒断言。
	lookupSteps int // 命中路径对索引的探测次数
	staleVisits int // 最近一次过期传播访问的实例数

	log Logger
}

// Option 配置登记中心。
type Option func(*Registry)

func WithMaxInstances(n int) Option { return func(r *Registry) { r.maxInstances = n } }
func WithMaxPerDef(n int) Option    { return func(r *Registry) { r.maxPerDef = n } }
func WithMaxDepth(n int) Option     { return func(r *Registry) { r.maxDepth = n } }
func WithLogger(l Logger) Option    { return func(r *Registry) { r.log = l } }

// New 创建登记中心。
func New(opts ...Option) *Registry {
	r := &Registry{
		defs:         map[string]*Def{},
		instances:    map[int]*Instance{},
		index:        map[string]int{},
		maxInstances: 1 << 30,
		maxPerDef:    1 << 30,
		maxDepth:     64,
		log:          nopLogger{},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// RegisterDef 登记（或覆盖登记）一个泛型定义。首次登记不触发过期；
// 覆盖已存在的定义等价于 UpdateDef。
func (r *Registry) RegisterDef(d *Def) {
	r.mu.Lock()
	_, existed := r.defs[d.Name]
	r.defs[d.Name] = cloneDef(d)
	r.log.Log("register", "def="+d.Name, "existed="+boolStr(existed))
	if existed {
		r.invalidateLocked(d.Name)
	}
	r.mu.Unlock()
}

// UpdateDef 更新定义：新声明立即生效，并把旧定义生成的全部实例及传递依赖标记过期。
func (r *Registry) UpdateDef(d *Def) {
	r.mu.Lock()
	r.defs[d.Name] = cloneDef(d)
	r.log.Log("update", "def="+d.Name)
	r.invalidateLocked(d.Name)
	r.mu.Unlock()
}

// Request 请求以具体类型实参实例化某定义。命中已有实例不消耗配额。
func (r *Registry) Request(unit, defName string, args []Type, body Body) (*Instance, *RequestError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sess := &Session{r: r, depth: 0, parent: -1, created: map[int]struct{}{}}
	in, e := r.requestLocked(sess, defName, args, body)
	r.logResult(unit, defName, args, in, e)
	return in, e
}

// Lookup 只读查询一个实例（过期实例仍可查到其过期状态）。
func (r *Registry) Lookup(id int) *Instance {
	r.mu.Lock()
	in := r.instances[id]
	r.mu.Unlock()
	return in
}

// requestLocked 是嵌套请求的内部实现，调用方必须已持有 r.mu。
func (r *Registry) requestLocked(sess *Session, defName string, args []Type, body Body) (*Instance, *RequestError) {
	newDepth := sess.depth + 1

	// 固定拒绝次序：未定义 > 参数错误 > 约束错误 > 深度错误 > 配额错误。
	d, ok := r.defs[defName]
	if !ok {
		return nil, &RequestError{Code: ErrUndefined, Msg: "no such definition " + quote(defName)}
	}
	norm, e := normalizeArgs(d, args)
	if e != nil {
		return nil, e
	}
	if e := checkConstraints(d, norm); e != nil {
		return nil, e
	}
	if newDepth > r.maxDepth {
		return nil, &RequestError{Code: ErrDepth,
			Msg: "instantiation depth " + itoa(newDepth) + " exceeds limit " + itoa(r.maxDepth)}
	}

	// 命中路径只做一次哈希探测，开销与已登记实例总数无关。
	key := defName + "\x00" + argsKey(norm)
	r.lookupSteps++
	if id, hit := r.index[key]; hit {
		in := r.instances[id]
		r.totalHits++
		sess.hits++
		if sess.parent >= 0 {
			r.addEdgeLocked(sess.parent, in.ID)
		}
		r.log.Log("hit", "def="+defName, "id="+itoa(id), "depth="+itoa(sess.depth))
		return in, nil
	}

	// 新建前做配额检查（含取等边界）；过期实例仍占用配额。
	if len(r.instances) >= r.maxInstances {
		return nil, &RequestError{Code: ErrQuota, Msg: "total instance quota exhausted"}
	}
	if r.countPerDefLocked(defName) >= r.maxPerDef {
		return nil, &RequestError{Code: ErrQuota, Msg: "per-definition instance quota exhausted for " + defName}
	}

	in := &Instance{
		ID:     r.nextID,
		Def:    defName,
		Args:   norm,
		deps:   map[int]struct{}{},
		depend: map[int]struct{}{},
	}
	r.nextID++
	r.instances[in.ID] = in
	r.index[key] = in.ID
	sess.created[in.ID] = struct{}{}
	r.totalCreates++
	r.log.Log("create", "def="+defName, "id="+itoa(in.ID), "depth="+itoa(newDepth))
	if sess.parent >= 0 {
		r.addEdgeLocked(sess.parent, in.ID)
	}

	// 执行实例化体；嵌套请求复用同一临界区与同一撤销集合。
	if body != nil {
		nested := &Session{r: r, depth: newDepth, parent: in.ID, created: sess.created}
		be := body(nested)
		if re, isRE := be.(*RequestError); isRE && re != nil {
			be = re
		} else if isRE {
			be = nil // 具体类型为 nil 的 *RequestError 不构成失败
		}
		if be != nil {
			r.rollbackLocked(sess.created)
			r.totalHits -= sess.hits // 被拒绝请求不得改变统计量
			if re, ok := be.(*RequestError); ok {
				return nil, re // 保留嵌套请求的原始错误码（如 depth/quota）
			}
			return nil, &RequestError{Code: ErrDepth, Msg: be.Error()}
		}
	}
	return in, nil
}

// rollbackLocked 撤销本次请求新建的全部中间实例，并解除它们之间登记的边。
// 已有实例（不在 created 集合内）绝不触碰，复用实例完好无损。
func (r *Registry) rollbackLocked(created map[int]struct{}) {
	// 先固定本次要删除的实例集合，避免边删除过程中集合变动造成遗漏。
	victims := make([]int, 0, len(created))
	for id := range created {
		if _, ok := r.instances[id]; ok {
			victims = append(victims, id)
		}
	}
	victimSet := make(map[int]struct{}, len(victims))
	for _, id := range victims {
		victimSet[id] = struct{}{}
	}
	for _, id := range victims {
		in := r.instances[id]
		deps := make([]int, 0, len(in.deps))
		for dep := range in.deps {
			deps = append(deps, dep)
		}
		dependents := make([]int, 0, len(in.depend))
		for p := range in.depend {
			dependents = append(dependents, p)
		}
		// 对每个存活邻居解除指向本实例的边。
		// 复用实例上的跨边必须显式清除，否则会残留悬空反向边。
		for _, dep := range deps {
			if child, ok := r.instances[dep]; ok {
				delete(child.depend, id)
			}
		}
		for _, p := range dependents {
			if parent, ok := r.instances[p]; ok {
				delete(parent.deps, id)
			}
		}
		_ = victimSet
		key := in.Def + "\x00" + argsKey(in.Args)
		delete(r.index, key)
		delete(r.instances, id)
		r.totalCreates--
		r.log.Log("rollback", "id="+itoa(id), "def="+in.Def)
	}
}

func (r *Registry) countPerDefLocked(defName string) int {
	n := 0
	for _, in := range r.instances {
		if in.Def == defName {
			n++
		}
	}
	return n
}

func (r *Registry) addEdgeLocked(parentID, childID int) {
	if parentID == childID {
		return
	}
	parent := r.instances[parentID]
	child := r.instances[childID]
	if parent == nil || child == nil {
		return
	}
	parent.deps[childID] = struct{}{}
	child.depend[parentID] = struct{}{}
}

func (r *Registry) logResult(unit, defName string, args []Type, in *Instance, e *RequestError) {
	argDescs := make([]string, len(args))
	for i, a := range args {
		if a == nil {
			argDescs[i] = "<nil>"
		} else {
			argDescs[i] = a.CanonKey()
		}
	}
	if e != nil {
		r.log.Log("request", "unit="+unit, "def="+defName, "args=["+joinComma(argDescs)+"]",
			"result=reject", "code="+e.Code.String(), "reason="+e.Msg)
		return
	}
	if in == nil {
		r.log.Log("request", "unit="+unit, "def="+defName, "args=["+joinComma(argDescs)+"]",
			"result=ok", "id=<nil>")
		return
	}
	r.log.Log("request", "unit="+unit, "def="+defName, "args=["+joinComma(argDescs)+"]",
		"result=ok", "id="+itoa(in.ID))
}

func cloneDef(d *Def) *Def {
	cp := &Def{Name: d.Name, Params: make([]Param, len(d.Params))}
	copy(cp.Params, d.Params)
	return cp
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}
