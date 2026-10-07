package ontology

import (
	"errors"
	"fmt"
	"sync"
)

// ErrDenied 表示权限判定拒绝了本次读取或写入。
// 被拒绝的操作不产生任何副作用（属性值、版本时钟、判定日志之外的状态均不变）。
var ErrDenied = errors.New("权限不足，操作被拒绝")

// Effect 是一条授权对某操作类别的结论。
type Effect int

const (
	EffectUnset Effect = iota // 未声明
	EffectAllow
	EffectDeny
)

func (e Effect) String() string {
	switch e {
	case EffectAllow:
		return "allow"
	case EffectDeny:
		return "deny"
	}
	return "unset"
}

// Grant 按标签声明某个主体的权限，与具体实例无关。
// Scope 为可见范围（属性名集合）；为空表示作用于该类型的全部属性。
type Grant struct {
	Tag     string
	Subject string
	Read    Effect
	Write   Effect
	Scope   []string
}

// appliesTo 报告该授权是否覆盖属性 attr。
func (g Grant) appliesTo(attr string) bool {
	if len(g.Scope) == 0 {
		return true
	}
	for _, name := range g.Scope {
		if name == attr {
			return true
		}
	}
	return false
}

// mergeEffects 是确定且与求值顺序无关的合并规则：
// 任一标签拒绝则拒绝（deny-wins）；否则任一允许则允许；
// 携带了标签但无任何适用授权时默认拒绝（封闭世界）。
func mergeEffects(effects []Effect) Effect {
	seenAllow := false
	for _, e := range effects {
		switch e {
		case EffectDeny:
			return EffectDeny
		case EffectAllow:
			seenAllow = true
		}
	}
	if seenAllow {
		return EffectAllow
	}
	return EffectDeny
}

// GrantMatch 记录一次判定中某携带标签贡献的授权结论（判定依据）。
type GrantMatch struct {
	Tag    string
	Effect Effect
}

// Decision 是一次调用在判定日志中的完整记录：
// 输入（主体/对象/属性/快照）、最终输出（是否允许、错误）与判定依据
// （携带的标签集合、各标签的授权结论、实际参与判定的属性读取次数）。
// 日志不记录任何属性取值，避免经日志通道向未授权主体泄漏。
type Decision struct {
	Op              string // "read" / "write" / "tag"
	Subject         string
	ObjectType      string
	InstanceID      string
	Attr            string // write 时为本次写入的属性列表（排序后逗号连接）
	SnapshotVersion uint64
	Allowed         bool
	CarriedTags     []string
	Basis           []GrantMatch
	AttrReads       int
	Err             string
}

// Engine 是动态标签属性级权限引擎。
// 全部操作在同一把互斥锁下串行化，因此任意并发调用的结果
// 必然等价于某个串行执行顺序（可线性化）。
type Engine struct {
	mu     sync.Mutex
	st     *store
	rules  *ruleSet
	grants []Grant
	log    []Decision
}

// NewEngine 创建空引擎。
func NewEngine() *Engine {
	return &Engine{
		st:    newStore(),
		rules: newRuleSet(),
	}
}

// RegisterObjectType 登记对象类型及其属性 schema。
func (e *Engine) RegisterObjectType(ot ObjectType) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ot.Name == "" {
		return fmt.Errorf("对象类型名不能为空")
	}
	attrs := make(map[string]struct{}, len(ot.Attrs))
	for name := range ot.Attrs {
		attrs[name] = struct{}{}
	}
	e.st.types[ot.Name] = &ObjectType{Name: ot.Name, Attrs: attrs}
	return nil
}

// ReplaceRules 原子地整体替换判定规则集合。
// 先对合并后的全量规则做校验（未知属性 > 循环依赖），
// 任一规则不合法则整体拒绝，已有规则不受任何影响。
func (e *Engine) ReplaceRules(rules []TagRule) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	byType := make(map[string]map[string]TagRule)
	for _, r := range rules {
		m := byType[r.ObjectType]
		if m == nil {
			m = make(map[string]TagRule)
			byType[r.ObjectType] = m
		}
		m[r.Tag] = r
	}
	for _, typeName := range sortedKeys(byType) {
		if err := e.rules.validate(e.st, typeName, byType[typeName]); err != nil {
			return err
		}
	}
	e.rules = newRuleSet()
	for typeName, m := range byType {
		e.rules.upsert(typeName, m)
	}
	return nil
}

// SetGrants 原子地整体替换授权表。
func (e *Engine) SetGrants(grants []Grant) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.grants = append([]Grant(nil), grants...)
}

// BeginSnapshot 在当前提交点开启一个可重复读快照。
// 快照选取规则确定且可复现：快照绑定创建时刻的全局提交时钟，
// 其后的并发写入不改变该快照上任何读取的结果。
func (e *Engine) BeginSnapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.beginSnapshot()
}

// Latest 返回表示“读最新已提交状态”的快照（每次调用解析为当前时钟）。
func Latest() Snapshot { return Snapshot{latest: true} }

// Prune 触发版本垃圾回收并推进可读水位；水位之下的快照随后将报
// ErrKindSnapshotExpired。返回当前可读水位。
func (e *Engine) Prune() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.gc()
}

// Log 返回判定日志的副本（按调用串行顺序排列）。
func (e *Engine) Log() []Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Decision, len(e.log))
	copy(out, e.log)
	return out
}

// evalContext 是一次判定内的求值上下文：按快照版本读取属性真实取值，
// 对标签结果做判定内备忘，并统计实际参与判定的属性读取次数。
type evalContext struct {
	eng       *Engine
	key       instanceKey
	version   uint64
	memo      map[string]bool
	attrReads int
}

func (c *evalContext) resolveAttr(name string) Value {
	c.attrReads++
	v, _ := c.eng.st.get(c.key, name, c.version)
	return v
}

// carried 判定实例在快照版本下是否携带标签 tag。
// 求值基于属性真实取值，与发起主体的可读权限无关；
// 求值错误（如类型不匹配）按“不携带”处理，错误细节不向外传播。
func (c *evalContext) carried(tag string) (bool, error) {
	if v, ok := c.memo[tag]; ok {
		return v, nil
	}
	rules := c.eng.rules.rulesOf(c.key.typeName)
	rule, ok := rules[tag]
	if !ok {
		return false, nil // 未登记的标签视为不携带
	}
	v, err := eval(rule.Expr, c.resolveAttr, c.carried)
	if err != nil {
		c.memo[tag] = false
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		c.memo[tag] = false
		return false, nil
	}
	c.memo[tag] = b
	return b, nil
}

// carriedTags 返回实例在快照版本下携带的全部标签（字典序，确定）。
func (c *evalContext) carriedTags() ([]string, error) {
	rules := c.eng.rules.rulesOf(c.key.typeName)
	var carried []string
	for _, tag := range sortedKeys(rules) {
		ok, err := c.carried(tag)
		if err != nil {
			return nil, err
		}
		if ok {
			carried = append(carried, tag)
		}
	}
	return carried, nil
}

// mergeFor 对携带标签集合在 (subject, attr, 操作类别) 上做 deny-wins 合并，
// 返回最终结论与判定依据。结论只依赖集合，不依赖求值顺序。
// 实例未携带任何标签时没有任何限制生效，结论为允许。
func (e *Engine) mergeFor(subject, attr string, carried []string, write bool) (Effect, []GrantMatch) {
	if len(carried) == 0 {
		return EffectAllow, nil
	}
	effects := make([]Effect, 0, len(carried))
	basis := make([]GrantMatch, 0, len(carried))
	for _, tag := range carried {
		for _, g := range e.grants {
			if g.Tag != tag || g.Subject != subject || !g.appliesTo(attr) {
				continue
			}
			eff := g.Read
			if write {
				eff = g.Write
			}
			if eff == EffectUnset {
				continue
			}
			effects = append(effects, eff)
			basis = append(basis, GrantMatch{Tag: tag, Effect: eff})
		}
	}
	return mergeEffects(effects), basis
}

// preflight 按固定错误优先级做操作前检查：
// 未知属性（第一优先级）先于快照过期（第三优先级）汇报。
func (e *Engine) preflight(typeName, attr string, snap Snapshot) (uint64, error) {
	ot := e.st.types[typeName]
	if ot == nil {
		return 0, newError(ErrKindUnknownAttribute, "对象类型 %q 未登记", typeName)
	}
	if attr != "" && !ot.HasAttr(attr) {
		return 0, newError(ErrKindUnknownAttribute,
			"对象类型 %q 不存在属性 %q", typeName, attr)
	}
	return e.st.resolve(snap)
}

// Read 在快照 snap 上判定主体是否可读某属性，可读时返回其取值。
// 标签状态按快照版本实时重算；判定依据写入日志。
func (e *Engine) Read(subject, typeName, id, attr string, snap Snapshot) (Value, Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := instanceKey{typeName: typeName, id: id}
	dec := Decision{Op: "read", Subject: subject, ObjectType: typeName, InstanceID: id, Attr: attr}
	version, err := e.preflight(typeName, attr, snap)
	if err != nil {
		dec.Err = err.Error()
		e.log = append(e.log, dec)
		return nil, dec, err
	}
	dec.SnapshotVersion = version
	ctx := &evalContext{eng: e, key: key, version: version, memo: map[string]bool{}}
	carried, err := ctx.carriedTags()
	if err != nil {
		dec.Err = err.Error()
		e.log = append(e.log, dec)
		return nil, dec, err
	}
	eff, basis := e.mergeFor(subject, attr, carried, false)
	dec.CarriedTags = carried
	dec.Basis = basis
	dec.AttrReads = ctx.attrReads
	dec.Allowed = eff == EffectAllow
	if !dec.Allowed {
		dec.Err = ErrDenied.Error()
		e.log = append(e.log, dec)
		return nil, dec, ErrDenied
	}
	v, _ := e.st.get(key, attr, version)
	e.log = append(e.log, dec)
	return v, dec, nil
}

// Write 判定主体是否可写一批属性，可写时作为一次原子提交生效。
// 权限判定基于写入前最新状态实时重算；被拒绝时不推进版本时钟、
// 不改变任何属性取值。提交成功后，后续读取立即按新标签状态裁决。
func (e *Engine) Write(subject, typeName, id string, attrs map[string]Value) (uint64, Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := instanceKey{typeName: typeName, id: id}
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sortStrings(names)
	dec := Decision{Op: "write", Subject: subject, ObjectType: typeName, InstanceID: id, Attr: joinNames(names)}
	// 第一优先级：写入的属性必须全部存在于 schema。
	ot := e.st.types[typeName]
	if ot == nil {
		err := newError(ErrKindUnknownAttribute, "对象类型 %q 未登记", typeName)
		dec.Err = err.Error()
		e.log = append(e.log, dec)
		return 0, dec, err
	}
	for _, name := range names {
		if !ot.HasAttr(name) {
			err := newError(ErrKindUnknownAttribute, "对象类型 %q 不存在属性 %q", typeName, name)
			dec.Err = err.Error()
			e.log = append(e.log, dec)
			return 0, dec, err
		}
	}
	// 权限判定基于写入前状态（当前时钟）。
	version := e.st.clock
	dec.SnapshotVersion = version
	ctx := &evalContext{eng: e, key: key, version: version, memo: map[string]bool{}}
	carried, err := ctx.carriedTags()
	if err != nil {
		dec.Err = err.Error()
		e.log = append(e.log, dec)
		return 0, dec, err
	}
	dec.CarriedTags = carried
	dec.AttrReads = ctx.attrReads
	for _, name := range names {
		eff, basis := e.mergeFor(subject, name, carried, true)
		dec.Basis = append(dec.Basis, basis...)
		if eff != EffectAllow {
			dec.Err = ErrDenied.Error()
			e.log = append(e.log, dec)
			return 0, dec, ErrDenied
		}
	}
	dec.Allowed = true
	newVersion := e.st.commit(key, attrs)
	e.log = append(e.log, dec)
	return newVersion, dec, nil
}

// TagState 返回实例在快照下是否携带某标签，以及本次判定实际读取的
// 属性数量。AttrReads 是“判定开销只与实际参与判定的属性数量相关”
// 的可观测证明手段：它与已登记规则总数无关。
func (e *Engine) TagState(typeName, id, tag string, snap Snapshot) (bool, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := instanceKey{typeName: typeName, id: id}
	version, err := e.preflight(typeName, "", snap)
	if err != nil {
		return false, 0, err
	}
	ctx := &evalContext{eng: e, key: key, version: version, memo: map[string]bool{}}
	ok, err := ctx.carried(tag)
	if err != nil {
		return false, 0, err
	}
	e.log = append(e.log, Decision{
		Op: "tag", ObjectType: typeName, InstanceID: id, Attr: tag,
		SnapshotVersion: version, Allowed: ok, AttrReads: ctx.attrReads,
	})
	return ok, ctx.attrReads, nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}
