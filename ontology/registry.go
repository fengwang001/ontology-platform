package ontology

import (
	"errors"
	"sync"
	"time"
)

// 绑定查询/写入可能返回的错误。
var (
	ErrUnknownLinkType     = errors.New("ontology: unknown link type")
	ErrDirectionNotAllowed = errors.New("ontology: binding direction not allowed by declaration")
	ErrBindingInvalid      = errors.New("ontology: binding definition invalid (field deleted)")
	ErrBindingIncompatible = errors.New("ontology: binding is incompatible")
	ErrLinkTypeExists      = errors.New("ontology: link type already declared")
	ErrUnknownObjectType   = errors.New("ontology: unknown object type")
	ErrUnknownField        = errors.New("ontology: unknown field")
)

// IncompatibleError 携带具体的不兼容分类。
type IncompatibleError struct {
	LinkTypeID string
	Kind       IncompatKind
	Detail     string
}

func (e *IncompatibleError) Error() string {
	return "ontology: link type " + e.LinkTypeID + " incompatible: " + e.Kind.String() + ": " + e.Detail
}

func (e *IncompatibleError) Unwrap() error { return ErrBindingIncompatible }

// AuditRecord 记录一次兼容性核验，供事后核对。
type AuditRecord struct {
	Seq               uint64
	Time              time.Time
	LinkTypeID        string
	Trigger           string
	LeftField         FieldDef
	RightField        FieldDef
	LeftFieldPresent  bool
	RightFieldPresent bool
	Result            CheckResult
	InstancesVisited  int
}

// LinkInstance 是一条存活的链接实例。
type LinkInstance struct {
	ID          string
	LeftObject  string
	RightObject string
}

type linkTypeState struct {
	decl      BindingDecl
	result    CheckResult
	instances map[string]LinkInstance
}

// Registry 是对象类型、字段、链接类型与链接实例的注册表。
// 所有字段变更与绑定查询都在同一把互斥锁下完成，
// 保证并发交织时等价于某个全序串行执行。
type Registry struct {
	mu          sync.Mutex
	seq         uint64
	objectTypes map[string]map[string]FieldDef
	linkTypes   map[string]*linkTypeState
	audit       []AuditRecord
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{
		objectTypes: make(map[string]map[string]FieldDef),
		linkTypes:   make(map[string]*linkTypeState),
	}
}

// nextSeq 在锁内分配全局序号；所有公开操作各取一个序号，
// 序号顺序即并发交织等价的全序串行执行顺序。
func (r *Registry) nextSeq() uint64 {
	r.seq++
	return r.seq
}

// RegisterObjectType 注册对象类型及其字段。
func (r *Registry) RegisterObjectType(id string, fields []FieldDef) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextSeq()
	if _, ok := r.objectTypes[id]; ok {
		return errors.New("ontology: object type already registered: " + id)
	}
	fm := make(map[string]FieldDef, len(fields))
	for _, f := range fields {
		if err := validateFieldDef(f); err != nil {
			return err
		}
		fm[f.ID] = f
	}
	r.objectTypes[id] = fm
	return nil
}

func validateFieldDef(f FieldDef) error {
	for _, v := range f.Enum {
		if v.Missing || v.Type != f.Type {
			return errors.New("ontology: enum value type mismatch in field " + f.ID)
		}
	}
	return nil
}

func (r *Registry) field(ref FieldRef) (FieldDef, bool) {
	fields, ok := r.objectTypes[ref.ObjectType]
	if !ok {
		return FieldDef{}, false
	}
	f, ok := fields[ref.FieldID]
	return f, ok
}

// DeclareLinkType 声明链接类型的绑定依据。
// 声明为双向但实际只有单向可对应的，在此阶段即被拒绝；
// 声明通过后对应方式与缺失策略不可再变。
func (r *Registry) DeclareLinkType(decl BindingDecl) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	seq := r.nextSeq()
	if _, ok := r.linkTypes[decl.LinkTypeID]; ok {
		return ErrLinkTypeExists
	}
	if decl.Direction != LeftToRight && decl.Direction != RightToLeft && decl.Direction != TwoWay {
		return errors.New("ontology: invalid binding direction")
	}
	left, lok := r.field(decl.Left)
	right, rok := r.field(decl.Right)
	if !lok || !rok {
		return &IncompatibleError{LinkTypeID: decl.LinkTypeID, Kind: IncompatFieldDeleted, Detail: "binding field does not exist at declaration"}
	}
	if decl.Missing.Kind == MissingForbidden && (left.Nullable || right.Nullable) {
		return errors.New("ontology: missing-forbidden policy requires non-nullable binding fields")
	}
	if decl.Missing.Kind == MissingForbidden {
		for _, p := range decl.Correspondence.Pairs {
			if p.Left.Missing || p.Right.Missing {
				return errors.New("ontology: explicit pair involves missing but policy forbids missing")
			}
		}
	}
	res := CheckBinding(decl, &left, &right)
	if !res.Compatible(decl.Direction) {
		return &IncompatibleError{LinkTypeID: decl.LinkTypeID, Kind: res.Kind, Detail: res.Detail}
	}
	r.linkTypes[decl.LinkTypeID] = &linkTypeState{
		decl:      decl,
		result:    res,
		instances: make(map[string]LinkInstance),
	}
	r.appendAudit(seq, "declare", decl.LinkTypeID, res, &left, &right, 0)
	return nil
}

// UpdateField 变更字段定义，并重核验所有以该字段为绑定依据的链接类型。
// 各链接类型的判定相互隔离；已有链接实例不受影响。
// 返回该操作在全局串行序中的序号。
func (r *Registry) UpdateField(objectType string, def FieldDef) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seq := r.nextSeq()
	fields, ok := r.objectTypes[objectType]
	if !ok {
		return seq, ErrUnknownObjectType
	}
	if _, ok := fields[def.ID]; !ok {
		return seq, ErrUnknownField
	}
	if err := validateFieldDef(def); err != nil {
		return seq, err
	}
	fields[def.ID] = def
	r.revalidate(func(lt *linkTypeState) bool {
		return lt.decl.Left == (FieldRef{objectType, def.ID}) || lt.decl.Right == (FieldRef{objectType, def.ID})
	}, "field-update")
	return seq, nil
}

// DeleteField 删除字段；引用它的链接类型被判定为定义失效（优先于其它结论）。
// 返回该操作在全局串行序中的序号。
func (r *Registry) DeleteField(objectType, fieldID string) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seq := r.nextSeq()
	fields, ok := r.objectTypes[objectType]
	if !ok {
		return seq, ErrUnknownObjectType
	}
	if _, ok := fields[fieldID]; !ok {
		return seq, ErrUnknownField
	}
	delete(fields, fieldID)
	r.revalidate(func(lt *linkTypeState) bool {
		return lt.decl.Left == (FieldRef{objectType, fieldID}) || lt.decl.Right == (FieldRef{objectType, fieldID})
	}, "field-delete")
	return seq, nil
}

// revalidate 重核验命中的链接类型。遍历的既有链接实例数量
// 不超过该链接类型当前存活实例总数（只访问 per-link-type 索引）。
func (r *Registry) revalidate(match func(*linkTypeState) bool, trigger string) {
	for id, lt := range r.linkTypes {
		if !match(lt) {
			continue
		}
		left, lok := r.field(lt.decl.Left)
		right, rok := r.field(lt.decl.Right)
		var lp, rp *FieldDef
		if lok {
			lp = &left
		}
		if rok {
			rp = &right
		}
		res := CheckBinding(lt.decl, lp, rp)
		lt.result = res
		visited := 0
		for range lt.instances {
			visited++ // 仅统计本链接类型的存活实例，不触碰对象类型的历史实例
		}
		r.appendAudit(r.nextSeq(), trigger, id, res, lp, rp, visited)
	}
}

func (r *Registry) appendAudit(seq uint64, trigger, linkTypeID string, res CheckResult, left, right *FieldDef, visited int) {
	rec := AuditRecord{
		Seq:              seq,
		Time:             time.Now(),
		LinkTypeID:       linkTypeID,
		Trigger:          trigger,
		Result:           res,
		InstancesVisited: visited,
	}
	if left != nil {
		rec.LeftField = *left
		rec.LeftFieldPresent = true
	}
	if right != nil {
		rec.RightField = *right
		rec.RightFieldPresent = true
	}
	r.audit = append(r.audit, rec)
}

// QueryResult 是一次绑定查询的结论。
type QueryResult struct {
	Seq        uint64
	LinkTypeID string
	Direction  Direction
	OK         bool
	Kind       IncompatKind
}

// QueryBinding 以指定方向发起绑定查询。
// 单向绑定的反方向查询被拒绝；当前不兼容的绑定查询被阻止。
func (r *Registry) QueryBinding(linkTypeID string, dir Direction) (QueryResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seq := r.nextSeq()
	lt, ok := r.linkTypes[linkTypeID]
	if !ok {
		return QueryResult{}, ErrUnknownLinkType
	}
	res := QueryResult{Seq: seq, LinkTypeID: linkTypeID, Direction: dir, Kind: lt.result.Kind}
	if !lt.decl.Direction.Allows(dir) {
		return res, ErrDirectionNotAllowed
	}
	if lt.result.Kind == IncompatFieldDeleted {
		return res, ErrBindingInvalid
	}
	if !lt.result.Compatible(lt.decl.Direction) {
		return res, &IncompatibleError{LinkTypeID: linkTypeID, Kind: lt.result.Kind, Detail: lt.result.Detail}
	}
	res.OK = true
	res.Kind = Compatible
	return res, nil
}

// CreateLinkInstance 建立链接实例；要求绑定当前在声明方向上可用。
func (r *Registry) CreateLinkInstance(linkTypeID, instanceID, leftObject, rightObject string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextSeq()
	lt, ok := r.linkTypes[linkTypeID]
	if !ok {
		return ErrUnknownLinkType
	}
	if lt.result.Kind == IncompatFieldDeleted {
		return ErrBindingInvalid
	}
	if !lt.result.Compatible(lt.decl.Direction) {
		return &IncompatibleError{LinkTypeID: linkTypeID, Kind: lt.result.Kind, Detail: lt.result.Detail}
	}
	lt.instances[instanceID] = LinkInstance{ID: instanceID, LeftObject: leftObject, RightObject: rightObject}
	return nil
}

// RemoveLinkInstance 显式移除链接实例。
func (r *Registry) RemoveLinkInstance(linkTypeID, instanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextSeq()
	lt, ok := r.linkTypes[linkTypeID]
	if !ok {
		return ErrUnknownLinkType
	}
	delete(lt.instances, instanceID)
	return nil
}

// LiveInstanceCount 返回链接类型当前存活实例数。
func (r *Registry) LiveInstanceCount(linkTypeID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if lt, ok := r.linkTypes[linkTypeID]; ok {
		return len(lt.instances)
	}
	return 0
}

// Compatibility 返回链接类型当前的兼容性结论。
func (r *Registry) Compatibility(linkTypeID string) (CheckResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if lt, ok := r.linkTypes[linkTypeID]; ok {
		return lt.result, true
	}
	return CheckResult{}, false
}

// AuditLog 返回全部核验审计记录的副本。
func (r *Registry) AuditLog() []AuditRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]AuditRecord, len(r.audit))
	copy(out, r.audit)
	return out
}
