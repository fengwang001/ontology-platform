package ontology

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// config 是某一提交版本下的全部声明性配置（对象类型、标签规则、授权表）。
// config 一旦提交即不可变，新的配置变更通过整体拷贝产生新版本。
type config struct {
	objectTypes map[string]ObjectType
	rules       map[string]TagRule
	grants      map[string]map[Principal]Grant // tag -> principal -> grant
}

func (c *config) clone() *config {
	out := &config{
		objectTypes: make(map[string]ObjectType, len(c.objectTypes)),
		rules:       make(map[string]TagRule, len(c.rules)),
		grants:      make(map[string]map[Principal]Grant, len(c.grants)),
	}
	for k, v := range c.objectTypes {
		out.objectTypes[k] = v
	}
	for k, v := range c.rules {
		out.rules[k] = v
	}
	for k, v := range c.grants {
		g := make(map[Principal]Grant, len(v))
		for p, grant := range v {
			g[p] = grant
		}
		out.grants[k] = g
	}
	return out
}

func (c *config) grantFor(tag string, p Principal) (Grant, bool) {
	byPrincipal, ok := c.grants[tag]
	if !ok {
		return Grant{}, false
	}
	g, ok := byPrincipal[p]
	return g, ok
}

// state 是一个已提交版本的世界状态：属性取值 + 配置。
// 提交后不可变，快照与并发读取直接共享。
type state struct {
	version uint64
	values  map[string]map[string]Value // instance key -> attr -> value
	config  *config
}

// Snapshot 是可重复读快照的不透明句柄。
// 快照选取规则：Begin 调用被串行化时点的最新已提交版本，
// 该规则在并发写交织下给出确定且可复现的结果。
type Snapshot struct {
	id      uint64
	version uint64
}

// Version 返回快照固定的提交版本。
func (s Snapshot) Version() uint64 { return s.version }

type snapshotLease struct {
	version uint64
	expires time.Time
}

// Engine 是动态标签属性级权限模块的核心。
//
// 并发模型：所有提交（属性写入、规则变更、授权变更）在单一互斥锁下串行化，
// 每次成功提交产生一个单调递增的版本；读取（最新读或快照读）作用于不可变的
// 历史版本，不阻塞写入。任意并发调用的结果都等价于某个串行顺序逐条执行的结果。
type Engine struct {
	mu        sync.Mutex
	latest    *state
	retained  map[uint64]*state
	snapshots map[uint64]snapshotLease
	nextSnap  uint64
	ttl       time.Duration
	now       func() time.Time
	logger    Logger
	logSeq    atomic.Uint64
}

// Option 配置 Engine。
type Option func(*Engine)

// WithSnapshotTTL 设置可重复读快照的租约时长；租约到期后快照不可用。
// 默认为 30 秒。传入 0 表示快照永不过期（仅能由 Close 释放）。
func WithSnapshotTTL(d time.Duration) Option {
	return func(e *Engine) { e.ttl = d }
}

// WithClock 注入时钟，用于快照租约与日志时间戳；测试可注入假时钟以获得确定性。
func WithClock(now func() time.Time) Option {
	return func(e *Engine) { e.now = now }
}

// WithLogger 设置审计日志输出。默认丢弃。
func WithLogger(l Logger) Option {
	return func(e *Engine) { e.logger = l }
}

func NewEngine(opts ...Option) *Engine {
	e := &Engine{
		latest: &state{
			version: 0,
			values:  map[string]map[string]Value{},
			config: &config{
				objectTypes: map[string]ObjectType{},
				rules:       map[string]TagRule{},
				grants:      map[string]map[Principal]Grant{},
			},
		},
		retained:  map[uint64]*state{},
		snapshots: map[uint64]snapshotLease{},
		ttl:       30 * time.Second,
		now:       time.Now,
		logger:    discardLogger{},
	}
	e.retained[0] = e.latest
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Version 返回当前最新已提交版本号。
func (e *Engine) Version() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.latest.version
}

// commitLocked 在持有锁的前提下提交一个新版本，并压缩不再被引用的历史版本。
// 只有校验与权限判定全部通过的调用才会走到这里，因此版本号（时钟）
// 不会因被拒绝的操作而前进。
func (e *Engine) commitLocked(cfg *config, values map[string]map[string]Value) uint64 {
	v := e.latest.version + 1
	st := &state{version: v, values: values, config: cfg}
	e.latest = st
	e.retained[v] = st
	e.compactLocked()
	return v
}

// compactLocked 回收既非最新版本、也未被任何未过期快照钉住的历史版本。
func (e *Engine) compactLocked() {
	now := e.now()
	pinned := map[uint64]struct{}{e.latest.version: {}}
	for id, s := range e.snapshots {
		if e.ttl > 0 && !now.Before(s.expires) {
			delete(e.snapshots, id)
			continue
		}
		pinned[s.version] = struct{}{}
	}
	for v := range e.retained {
		if _, ok := pinned[v]; !ok {
			delete(e.retained, v)
		}
	}
}

// RegisterObjectType 注册对象类型。重复注册同一名称是空操作（不推进版本）。
func (e *Engine) RegisterObjectType(ot ObjectType) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.latest.config.objectTypes[ot.Name]; ok {
		return nil
	}
	cfg := e.latest.config.clone()
	cfg.objectTypes[ot.Name] = ot
	v := e.commitLocked(cfg, e.latest.values)
	e.log(Entry{Op: OpRegisterType, Instance: ot.Name, Decision: DecisionAllow, Version: v})
	return nil
}

// SetTagRule 注册或替换一条标签判定规则。
// 规则的静态校验（未知属性、循环依赖等）在判定时刻按需进行，
// 以便按固定优先级与快照错误等其它失败统一汇报；此处仅校验对象类型存在。
func (e *Engine) SetTagRule(r TagRule) error {
	if r.Expr == nil {
		return errf(ErrRuleType, "tag rule %q has nil expression", r.Tag)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.latest.config.objectTypes[r.ObjectType]; !ok {
		return errf(ErrUnknownObjectType, "object type %q is not registered", r.ObjectType)
	}
	cfg := e.latest.config.clone()
	cfg.rules[r.Tag] = r
	v := e.commitLocked(cfg, e.latest.values)
	e.log(Entry{Op: OpSetRule, Instance: r.Tag, Decision: DecisionAllow, Version: v})
	return nil
}

// DeleteTagRule 删除一条标签判定规则。仍被其他规则引用的标签不可删除。
func (e *Engine) DeleteTagRule(tag string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.latest.config.rules[tag]; !ok {
		return errf(ErrUnknownTag, "tag %q is not registered", tag)
	}
	for _, r := range e.latest.config.rules {
		if _, refs := r.Expr.Tags()[tag]; refs {
			return errf(ErrTagInUse, "tag %q is referenced by rule %q", tag, r.Tag)
		}
	}
	cfg := e.latest.config.clone()
	delete(cfg.rules, tag)
	v := e.commitLocked(cfg, e.latest.values)
	e.log(Entry{Op: OpDeleteRule, Instance: tag, Decision: DecisionAllow, Version: v})
	return nil
}

// SetGrant 设置某标签对某主体的授权（可读、可写、可见范围），与具体实例无关。
func (e *Engine) SetGrant(tag string, p Principal, g Grant) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	cfg := e.latest.config.clone()
	byPrincipal, ok := cfg.grants[tag]
	if !ok {
		byPrincipal = map[Principal]Grant{}
		cfg.grants[tag] = byPrincipal
	}
	byPrincipal[p] = g
	v := e.commitLocked(cfg, e.latest.values)
	e.log(Entry{Op: OpSetGrant, Principal: string(p), Instance: tag, Decision: DecisionAllow, Version: v})
	return nil
}

// Begin 开启一个可重复读快照。快照固定于本调用串行化时点的最新已提交版本。
func (e *Engine) Begin() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextSnap++
	id := e.nextSnap
	lease := snapshotLease{version: e.latest.version}
	if e.ttl > 0 {
		lease.expires = e.now().Add(e.ttl)
	}
	e.snapshots[id] = lease
	return Snapshot{id: id, version: lease.version}
}

// Close 显式释放快照。释放后再使用该快照将报告 ErrSnapshotUnavailable。
func (e *Engine) Close(s Snapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.snapshots, s.id)
}

// snapshotStateLocked 解析快照句柄对应的版本状态。
func (e *Engine) snapshotStateLocked(s Snapshot) (*state, bool) {
	lease, ok := e.snapshots[s.id]
	if !ok || lease.version != s.version {
		return nil, false
	}
	if e.ttl > 0 && !e.now().Before(lease.expires) {
		return nil, false
	}
	st, ok := e.retained[lease.version]
	return st, ok
}

// Write 原子地写入某实例的若干属性。
//
// 写权限按写入前的最新状态判定；判定与提交在同一把锁内完成，
// 外部不可能观察到属性值已更新但标签判定尚未更新的中间状态。
// 写入成功后，所有后续读取立即反映标签状态变化后的权限结论。
// 被拒绝的写入不修改任何属性取值，也不推进版本号。
func (e *Engine) Write(p Principal, inst InstanceRef, attrs map[string]Value) (uint64, error) {
	names := sortedKeys(valueMapKeys(attrs))
	entry := Entry{Op: OpWrite, Principal: string(p), Instance: inst.key(), Attributes: names}
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.latest
	basis, loads, err := authorize(st, inst, p, names, false)
	entry.Basis = basis
	entry.AttrLoads = loads
	if err != nil {
		entry.Decision = decisionOf(err)
		entry.ErrClass = err.Class.String()
		e.log(entry)
		return 0, err
	}
	values := st.values
	next := make(map[string]Value, len(values[inst.key()])+len(attrs))
	for k, v := range values[inst.key()] {
		next[k] = v
	}
	for k, v := range attrs {
		next[k] = v
	}
	newValues := make(map[string]map[string]Value, len(values)+1)
	for k, v := range values {
		newValues[k] = v
	}
	newValues[inst.key()] = next
	v := e.commitLocked(st.config, newValues)
	entry.Decision = DecisionAllow
	entry.Version = v
	e.log(entry)
	return v, nil
}

// Read 在最新已提交版本上读取某实例的若干属性。
func (e *Engine) Read(p Principal, inst InstanceRef, attrs []string) (Result, error) {
	e.mu.Lock()
	st := e.latest
	e.mu.Unlock()
	return e.readOnState(st, 0, p, inst, attrs)
}

// ReadAt 在声明的可重复读快照上读取某实例的若干属性。
// 无论其间发生何种并发写入，同一快照上的多次读取都得到与该快照
// 版本一致的属性取值、标签状态与权限结论。
func (e *Engine) ReadAt(s Snapshot, p Principal, inst InstanceRef, attrs []string) (Result, error) {
	e.mu.Lock()
	st, ok := e.snapshotStateLocked(s)
	latest := e.latest
	e.mu.Unlock()
	if !ok {
		// 快照不可用，但仍须先按优先级汇报可能存在的规则类错误：
		// 用最新配置做静态校验，再报告快照错误。
		if err := staticValidate(latest.config, inst, attrs); err != nil {
			e.log(Entry{Op: OpReadAt, Principal: string(p), Instance: inst.key(),
				Attributes: sortedCopy(attrs), Decision: decisionOf(err), ErrClass: err.Class.String()})
			return Result{}, err
		}
		err := errf(ErrSnapshotUnavailable, "snapshot is expired or unknown")
		e.log(Entry{Op: OpReadAt, Principal: string(p), Instance: inst.key(),
			Attributes: sortedCopy(attrs), Decision: DecisionError, ErrClass: err.Class.String()})
		return Result{}, err
	}
	return e.readOnState(st, s.id, p, inst, attrs)
}

// readOnState 在确定的版本状态上执行读取的校验、判定与取值。
func (e *Engine) readOnState(st *state, snapID uint64, p Principal, inst InstanceRef, attrs []string) (Result, error) {
	names := sortedCopy(attrs)
	entry := Entry{Op: OpRead, Principal: string(p), Instance: inst.key(), Attributes: names, SnapshotID: snapID}
	if snapID != 0 {
		entry.Op = OpReadAt
	}
	values, basis, loads, err := readValues(st, inst, p, names)
	entry.Basis = basis
	entry.AttrLoads = loads
	entry.Version = st.version
	if err != nil {
		entry.Decision = decisionOf(err)
		entry.ErrClass = err.Class.String()
		e.log(entry)
		return Result{}, err
	}
	entry.Decision = DecisionAllow
	e.log(entry)
	return Result{Values: values, Version: st.version}, nil
}

// TagsOf 返回实例当前携带、且对主体可见（授权表 Visible）的标签列表。
// 标签携带状态按需根据当前属性取值实时求值。
func (e *Engine) TagsOf(p Principal, inst InstanceRef) ([]string, error) {
	e.mu.Lock()
	st := e.latest
	e.mu.Unlock()
	entry := Entry{Op: OpTagsOf, Principal: string(p), Instance: inst.key()}
	tags, basis, loads, err := visibleTags(st, inst, p)
	entry.Basis = basis
	entry.AttrLoads = loads
	entry.Version = st.version
	if err != nil {
		entry.Decision = decisionOf(err)
		entry.ErrClass = err.Class.String()
		e.log(entry)
		return nil, err
	}
	entry.Decision = DecisionAllow
	e.log(entry)
	return tags, nil
}

func (e *Engine) log(entry Entry) {
	entry.Seq = e.logSeq.Add(1)
	entry.Time = e.now()
	e.logger.Log(entry)
}

func decisionOf(err *Error) string {
	if err != nil && err.Class == ErrPermissionDenied {
		return DecisionDeny
	}
	return DecisionError
}

func valueMapKeys(m map[string]Value) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

func sortedCopy(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	sort.Strings(out)
	return out
}
