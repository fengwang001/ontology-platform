package ontology

import (
	"sync"
	"time"
)

// nextSeq 分配全局逻辑序号（调用方须已持有写锁）。
func (p *Platform) nextSeq() uint64 {
	p.seq++
	return p.seq
}

// AddObjectType 注册一个对象类型。initialTz 可选（Version 0 表示注册时不定义默认时区，
// 用于构造“写入时刻尚未定义默认时区”的场景）。
func (p *Platform) AddObjectType(id, groupingProp string, initialTz *TzDefVersion) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := &ObjectType{ID: id, GroupingProp: groupingProp, TypeVersion: 1}
	if initialTz != nil && initialTz.Version > 0 {
		t.TzVersions = append(t.TzVersions, *initialTz)
	}
	p.types[id] = t
}

// AddLink 注册一条链接关系。
func (p *Platform) AddLink(id, leftType, rightType string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.links[id] = LinkDef{ID: id, LeftType: leftType, RightType: rightType}
}

// DeleteObjectType 删除对象类型（单向）。该类型对象将从所有视图中驱逐，
// 后续涉及该类型的事件将触发 ErrLinkEndpointTypeMissing。
func (p *Platform) DeleteObjectType(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.types[id]; ok {
		t.Deleted = true
	}
	for _, v := range p.views {
		if link, ok := p.links[v.LinkID]; ok && (link.LeftType == id || link.RightType == id) {
			v.evictAll("link endpoint object type deleted")
		} else {
			v.evictType(id, "object type deleted")
		}
	}
}

// DeprecateGroupingProperty 对象类型版本迁移：废弃分组所用时间类属性（单向）。
// 该类型对象将从所有视图中驱逐，后续事件触发 ErrGroupingPropertyDeprecated。
func (p *Platform) DeprecateGroupingProperty(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.types[id]; ok && !t.Deleted {
		t.DeprecatedGroupingProp = true
		t.TypeVersion++
	}
	for _, v := range p.views {
		v.evictType(id, "grouping property deprecated")
	}
}

// validateMigration 校验一次默认时区定义版本迁移。
// 规则：类型存在且未删除；版本号严格递增；生效序号严格大于上一版本的生效序号
// （禁止版本间相互覆盖，保证任何写入序号对应唯一版本）；时区已注册。
// 注意：生效序号允许小于当前逻辑时钟（迁移记录本身也可能滞后到达），
// 这不会影响已锚定的事件——事件的分组解释只依赖事件携带的锚定版本。
func (p *Platform) validateMigration(typeID string, v TzDefVersion) error {
	t, ok := p.types[typeID]
	if !ok || t.Deleted {
		return &ViewError{Kind: ErrMigrationValidationFailed, ObjectTypeID: typeID,
			Detail: "object type missing or deleted"}
	}
	if _, ok := LookupZone(v.ZoneID); !ok {
		return &ViewError{Kind: ErrMigrationValidationFailed, ObjectTypeID: typeID,
			Detail: "unknown zone " + v.ZoneID}
	}
	if len(t.TzVersions) > 0 {
		last := t.TzVersions[len(t.TzVersions)-1]
		if v.Version <= last.Version {
			return &ViewError{Kind: ErrMigrationValidationFailed, ObjectTypeID: typeID,
				Detail: "version not strictly increasing"}
		}
		if v.EffectiveSeq <= last.EffectiveSeq {
			return &ViewError{Kind: ErrMigrationValidationFailed, ObjectTypeID: typeID,
				Detail: "effective seq not strictly increasing (retroactive migration rejected)"}
		}
	} else if v.Version <= 0 {
		return &ViewError{Kind: ErrMigrationValidationFailed, ObjectTypeID: typeID,
			Detail: "initial version must be positive"}
	}
	return nil
}

// MigrateDefaultTz 立即执行一次默认时区定义版本迁移。
// 迁移只追加新版本，绝不重算视图中既有对象的分组归属。
func (p *Platform) MigrateDefaultTz(typeID string, v TzDefVersion) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.validateMigration(typeID, v); err != nil {
		for _, view := range p.views {
			view.logMigration(typeID, v, false, err.(*ViewError).Detail)
		}
		return err
	}
	p.applyMigrationLocked(typeID, v)
	return nil
}

func (p *Platform) applyMigrationLocked(typeID string, v TzDefVersion) {
	t := p.types[typeID]
	t.TzVersions = append(t.TzVersions, v)
	for _, view := range p.views {
		view.logMigration(typeID, v, true, "")
	}
}

// EnqueueMigration 把一次迁移请求挂入队列，在下一次 Maintain 中与事件一起处理，
// 用于构造“同一次增量维护中同时触发多类错误”的场景。
func (p *Platform) EnqueueMigration(typeID string, v TzDefVersion) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pendingMigrations = append(p.pendingMigrations, migrationRequest{TypeID: typeID, Version: v})
}

// Platform 是本体平台子系统的门面：对象类型注册表、链接关系、
// 变更事件队列与视图。所有公开操作在同一把互斥锁下串行化，
// 因此任意并发调用的可观察结果都等价于按锁获取顺序串行执行。
type Platform struct {
	mu                sync.RWMutex
	seq               uint64
	types             map[string]*ObjectType
	links             map[string]LinkDef
	views             map[string]*View
	pending           []ChangeEvent
	pendingMigrations []migrationRequest
	history           []ChangeEvent // 全量事件历史，仅供朴素模型对照与审计；视图本身从不读取
}

type migrationRequest struct {
	TypeID  string
	Version TzDefVersion
}

// NewPlatform 创建一个空平台。
func NewPlatform() *Platform {
	return &Platform{
		types: make(map[string]*ObjectType),
		links: make(map[string]LinkDef),
		views: make(map[string]*View),
	}
}

// CreateView 在指定链接关系上创建聚合视图。
func (p *Platform) CreateView(id, linkID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.views[id] = newView(id, linkID)
}

// WriteTimeProperty 写入某对象的时间类属性取值。
// 写入时刻生效的默认时区定义版本在此被锚定进事件；
// 若写入时刻尚未定义默认时区，锚定版本为 0（由视图维护时报告错误）。
// 返回生成的事件（含写入序号与锚定版本）。
func (p *Platform) WriteTimeProperty(objectTypeID, objectID string, local time.Time) ChangeEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	seq := p.nextSeq()
	anchored := 0
	anchoredZone := ""
	if t, ok := p.types[objectTypeID]; ok && !t.Deleted {
		if tv, ok := t.tzVersionAt(seq); ok {
			anchored = tv.Version
			anchoredZone = tv.ZoneID
		}
	}
	evt := ChangeEvent{
		ObjectID:          objectID,
		ObjectTypeID:      objectTypeID,
		WriteSeq:          seq,
		LocalValue:        local,
		AnchoredTzVersion: anchored,
		AnchoredZoneID:    anchoredZone,
	}
	p.pending = append(p.pending, evt)
	p.history = append(p.history, evt)
	return evt
}

// InjectEvent 直接注入一条构造好的事件，用于模拟滞后到达 / 乱序到达的变更。
// 写入序号从 1 开始分配，0 为保留的非法值；序号为 0 的事件被直接拒绝。
func (p *Platform) InjectEvent(evt ChangeEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if evt.WriteSeq == 0 {
		return
	}
	p.pending = append(p.pending, evt)
	p.history = append(p.history, evt)
	if evt.WriteSeq > p.seq {
		p.seq = evt.WriteSeq
	}
}

// MaintenanceResult 汇总一次增量维护的结果。
type MaintenanceResult struct {
	ViewID   string
	Applied  int
	Stale    int
	Errors   map[ErrorKind]int
	TopError *ViewError // 本次维护触发错误中优先级最高的一类；nil 表示无错误
}

// Maintain 对指定视图执行一次增量维护：先应用队列中的迁移请求，
// 再应用队列中的全部待处理事件。多类错误同时触发时只在 TopError 中报告
// 优先级最高的一类，完整明细见 Errors 与决策日志。
func (p *Platform) Maintain(viewID string) MaintenanceResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	res := MaintenanceResult{ViewID: viewID, Errors: make(map[ErrorKind]int)}
	v, ok := p.views[viewID]
	if !ok {
		return res
	}
	recordErr := func(ve *ViewError) {
		res.Errors[ve.Kind]++
		if res.TopError == nil || ve.Kind < res.TopError.Kind {
			res.TopError = ve
		}
	}
	migrations := p.pendingMigrations
	p.pendingMigrations = nil
	for _, m := range migrations {
		if err := p.validateMigration(m.TypeID, m.Version); err != nil {
			ve := err.(*ViewError)
			ve.ViewID = viewID
			v.logMigration(m.TypeID, m.Version, false, ve.Detail)
			recordErr(ve)
			continue
		}
		p.applyMigrationLocked(m.TypeID, m.Version)
	}
	events := p.pending
	p.pending = nil
	for _, evt := range events {
		before := len(v.decisions)
		if ve := v.applyEvent(p, evt); ve != nil {
			recordErr(ve)
			continue
		}
		if len(v.decisions) > before && v.decisions[before].Op == "stale" {
			res.Stale++
			continue
		}
		res.Applied++
	}
	return res
}

// Query 返回视图当前的全部分组与组内有序成员，以及本次查询实际访问的
// 条目数。查询只遍历当前分组索引，开销与历史事件总量、历史迁移次数无关。
func (p *Platform) Query(viewID string) ([]GroupView, QueryStats) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.views[viewID]
	if !ok {
		return nil, QueryStats{}
	}
	var stats QueryStats
	out := make([]GroupView, 0, len(v.keys))
	for _, key := range v.keys {
		g := v.groups[key]
		gv := GroupView{Key: key, Members: make([]GroupMemberView, 0, len(g.members))}
		for _, m := range g.members {
			gv.Members = append(gv.Members, GroupMemberView{
				ObjectID:     m.ObjectID,
				ObjectTypeID: m.TypeID,
				InstantUnix:  m.Instant,
			})
			stats.EntriesVisited++
		}
		stats.GroupsVisited++
		out = append(out, gv)
	}
	return out, stats
}

// DecisionLog 返回视图决策日志的副本，供事后核查。
func (p *Platform) DecisionLog(viewID string) []DecisionRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.views[viewID]
	if !ok {
		return nil
	}
	out := make([]DecisionRecord, len(v.decisions))
	copy(out, v.decisions)
	return out
}

// History 返回全量事件历史（供朴素模型与审计使用）。
func (p *Platform) History() []ChangeEvent {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]ChangeEvent, len(p.history))
	copy(out, p.history)
	return out
}
