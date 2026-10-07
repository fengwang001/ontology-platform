package binding

import (
	"errors"
	"fmt"
	"sync"
)

// ErrBindingNotFound 在链接类型未声明绑定时返回。
var ErrBindingNotFound = errors.New("binding: link type has no binding declaration")

// ErrBidirectionalRejected 表示双向绑定在声明阶段即被拒绝
// （实际只有单向可对应，或对应关系根本不成立）。
type ErrBidirectionalRejected struct{ Result Result }

func (e *ErrBidirectionalRejected) Error() string {
	return "binding: bidirectional declaration rejected: " + e.Result.Verdict.String()
}

func (v Verdict) String() string { return string(v) }

type fieldVersion struct {
	def     FieldDef
	version int64
	deleted bool
}

type bindingState struct {
	spec BindingSpec
	// cached 是最近一次核验的不可变快照；其可见性与字段版本切换
	// 在同一把锁内原子完成，因此外部查询不可能读到跨版本的中间状态。
	cached Result
	// blocked 为 true 时拒绝新的绑定查询；已有链接实例不被撤销。
	blocked bool
	// freshLeft/freshRight 记录 cached 所依据的字段版本。
	freshLeft  int64
	freshRight int64
}

// AuditEntry 是一次核验留下的事后核对记录。
type AuditEntry struct {
	Seq          int64
	LinkType     string
	LeftField    FieldDef
	RightField   FieldDef
	LeftVersion  int64
	RightVersion int64
	Basis        CheckBasis
	Result       Result
	Trigger      string
}

// Registry 管理字段版本、链接类型绑定声明、实例遍历与核验结论。
type Registry struct {
	mu     sync.RWMutex
	fields map[string][]fieldVersion // key: objectType + "\x00" + fieldName
	links  map[string]*bindingState
	// referrers 记录每个字段被哪些链接类型引用，各链接类型独立重新核验。
	referrers map[string]map[string]struct{}
	audit     []AuditEntry
	auditSeq  int64
	store     InstanceStore
}

func NewRegistry(store InstanceStore) *Registry {
	return &Registry{
		fields:    map[string][]fieldVersion{},
		links:     map[string]*bindingState{},
		referrers: map[string]map[string]struct{}{},
		store:     store,
	}
}

func fieldKey(objectType, name string) string { return objectType + "\x00" + name }

// DeclareField 登记字段的首个版本。
func (r *Registry) DeclareField(def FieldDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fieldKey(def.ObjectType, def.Name)
	r.fields[key] = []fieldVersion{{def: def, version: 1}}
}

// UpdateField 以新版本替换字段定义并独立触发相关链接类型的重新核验。
func (r *Registry) UpdateField(def FieldDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fieldKey(def.ObjectType, def.Name)
	versions := r.fields[key]
	if len(versions) == 0 {
		versions = []fieldVersion{}
	}
	next := fieldVersion{def: def, version: int64(len(versions)) + 1}
	r.fields[key] = append(versions, next)
	for linkType := range r.referrers[key] {
		r.reevaluateLocked(linkType, "field_update:"+key)
	}
}

// DeleteField 将字段标记为删除，绑定该字段的链接类型定义随即失效。
func (r *Registry) DeleteField(objectType, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fieldKey(objectType, name)
	versions := r.fields[key]
	if len(versions) == 0 {
		return
	}
	last := versions[len(versions)-1]
	// 追加一个“已删除”的新版本；历史版本对象永不原地修改。
	tombstone := fieldVersion{def: last.def, version: last.version + 1, deleted: true}
	r.fields[key] = append(versions, tombstone)
	for linkType := range r.referrers[key] {
		r.reevaluateLocked(linkType, "field_delete:"+key)
	}
}

// DeclareBinding 在声明阶段核验绑定；双向绑定若实际仅单向可对应会被拒绝。
func (r *Registry) DeclareBinding(spec BindingSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.links[spec.LinkType]; exists {
		return fmt.Errorf("binding: link type %q already declared", spec.LinkType)
	}
	state := &bindingState{spec: spec}
	result := r.evaluateLocked(state, "declare")
	if spec.Direction == Bidirectional && result.Verdict != VerdictCompatible {
		// 声明阶段即拒绝，不允许等到反方向首次使用时才暴露。
		return &ErrBidirectionalRejected{Result: result}
	}
	state.cached = result
	state.freshLeft = result.LeftVersion
	state.freshRight = result.RightVersion
	state.blocked = result.Verdict != VerdictCompatible
	r.links[spec.LinkType] = state
	r.addReferrer(spec.LinkType, spec.LeftObject, spec.LeftField)
	r.addReferrer(spec.LinkType, spec.RightObject, spec.RightField)
	return nil
}

func (r *Registry) addReferrer(linkType, objectType, name string) {
	key := fieldKey(objectType, name)
	set := r.referrers[key]
	if set == nil {
		set = map[string]struct{}{}
		r.referrers[key] = set
	}
	set[linkType] = struct{}{}
}

// Evaluate 对指定链接类型执行（必要时重新）核验并返回结论。
func (r *Registry) Evaluate(linkType string) Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.links[linkType]; !ok {
		return Result{Verdict: VerdictFieldDeleted, Reason: ErrBindingNotFound.Error()}
	}
	return r.reevaluateLocked(linkType, "explicit_evaluate")
}

// Query 按指定方向使用绑定；反方向单向绑定被拒绝，不静默退化为双向。
func (r *Registry) Query(linkType string, dir QueryDirection) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.links[linkType]
	if !ok {
		return Result{}, ErrBindingNotFound
	}
	// 字段版本切换、实例变更与核验结论在同一把锁内原子发布。
	// 读时若缓存版本滞后则当场重新核验，保证返回结论等价于
	// “全部变更与查询按某个全序串行执行”后该时刻的结论。
	lv, lok := r.currentField(state.spec.LeftObject, state.spec.LeftField)
	rv, rok := r.currentField(state.spec.RightObject, state.spec.RightField)
	if lok && rok && lv.version == state.freshLeft && rv.version == state.freshRight {
		result := state.cached
		if !directionAllowed(state.spec.Direction, dir) {
			result.Verdict = VerdictDirectionDenied
			result.Reason = "binding declared " + string(state.spec.Direction) + "; query in reverse direction is rejected"
		}
		return result, nil
	}
	result := r.reevaluateLocked(linkType, "stale_query")
	if !directionAllowed(state.spec.Direction, dir) {
		result.Verdict = VerdictDirectionDenied
		result.Reason = "binding declared " + string(state.spec.Direction) + "; query in reverse direction is rejected"
	}
	return result, nil
}

// AuditLog 返回全部核验记录的副本。
func (r *Registry) AuditLog() []AuditEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AuditEntry, len(r.audit))
	copy(out, r.audit)
	return out
}

func directionAllowed(declared Direction, query QueryDirection) bool {
	switch declared {
	case Bidirectional:
		return true
	case LeftToRight:
		return query == QLeftToRight
	case RightToLeft:
		return query == QRightToLeft
	default:
		return false
	}
}

// reevaluateLocked 重新核验并原子更新状态；字段删除优先级在 evaluateLocked 内保证。
func (r *Registry) reevaluateLocked(linkType, trigger string) Result {
	state := r.links[linkType]
	result := r.evaluateLocked(state, trigger)
	state.cached = result
	// 原对应方式不再双向唯一/单向成立时，阻止新的绑定查询；
	// 不撤销任何已存在的链接实例。
	state.blocked = result.Verdict != VerdictCompatible
	return result
}

func (r *Registry) evaluateLocked(state *bindingState, trigger string) Result {
	spec := state.spec
	leftVer, leftOK := r.currentField(spec.LeftObject, spec.LeftField)
	rightVer, rightOK := r.currentField(spec.RightObject, spec.RightField)

	result := Result{Direction: spec.Direction}
	var leftDef, rightDef FieldDef
	result.LeftVersion = leftVer.version
	result.RightVersion = rightVer.version
	switch {
	case !leftOK || leftVer.deleted || !rightOK || rightVer.deleted:
		// 最高优先级：绑定依据字段缺失/被删除，其余三类判断都依赖字段存在。
		result.Verdict = VerdictFieldDeleted
		switch {
		case !leftOK || leftVer.deleted:
			result.Reason = "left binding field is deleted: " + fieldKey(spec.LeftObject, spec.LeftField)
		}
		switch {
		case !rightOK || rightVer.deleted:
			result.Reason = "right binding field is deleted: " + fieldKey(spec.RightObject, spec.RightField)
		}
	default:
		leftDef = leftVer.def
		rightDef = rightVer.def
		result = Check(&leftDef, &rightDef, spec)
		result.LeftVersion = leftVer.version
		result.RightVersion = rightVer.version
	}

	// 遍历只用于给既有存活实例加盖核验版本戳；遍历数量严格受限于
	// 当前存活集合快照大小。任何不兼容都不删除实例，历史绑定保持不变。
	if r.store != nil {
		live := r.store.LiveCount(spec.LinkType)
		result.InstancesLive = live
		seen := 0
		_ = r.store.VisitLive(spec.LinkType, func(Instance) error {
			seen++
			return nil
		})
		result.InstancesSeen = seen
	}

	r.auditSeq++
	r.audit = append(r.audit, AuditEntry{
		Seq:          r.auditSeq,
		LinkType:     spec.LinkType,
		LeftField:    leftDef,
		RightField:   rightDef,
		LeftVersion:  result.LeftVersion,
		RightVersion: result.RightVersion,
		Basis:        result.Basis,
		Result:       result,
		Trigger:      trigger,
	})
	return result
}

func (r *Registry) currentField(objectType, name string) (fieldVersion, bool) {
	versions := r.fields[fieldKey(objectType, name)]
	if len(versions) == 0 {
		return fieldVersion{}, false
	}
	return versions[len(versions)-1], true
}
