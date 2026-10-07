package ontology

import "sync"

// IndexStatus 描述索引对查询而言的可用性状态。
type IndexStatus uint8

const (
	// StatusDeclared 已声明但从未成功重建：查询按“重建中/不可用”还是“不存在”？
	// 已声明即属于“索引存在但不可用”，区别于未声明（不存在）。
	StatusDeclared IndexStatus = iota
	StatusBuilding
	StatusFailed
	StatusAvailable
)

type indexKey struct {
	t TypeID
	a AttrName
}

type entryMeta struct {
	sourceSeq int64 // 索引条目当前取值的来源写入序号
}

type indexState struct {
	status       IndexStatus
	flaggedDirty bool // 复核曾发现不一致并已标记（需修复），查询仍可进行
	entries      map[Value]map[ObjectID]entryMeta
	audit        *AuditRecord
	pending      *rebuildState
}

type rebuildState struct {
	id           string
	baselineSeq  int64
	baselineSnap map[ObjectID]map[AttrName]AttrCell // 基准点对象快照
	deltas       []Write                            // (B, C] 区间按生效顺序记录
	built        map[Value]map[ObjectID]entryMeta   // 离线构建中的新索引（含基线）
	failed       bool
}

// Platform 是本体对象平台：对象存储、属性索引与重建/复核全部在此。
// 所有状态变更在同一把锁下串行化，保证等价于某个全局串行顺序。
type Platform struct {
	mu       sync.Mutex
	seq      int64
	objects  map[TypeID]map[ObjectID]map[AttrName]AttrCell
	declared map[indexKey]bool
	indexes  map[indexKey]*indexState
	writeLog []Write // 仅供朴素模型与测试使用；复核器禁止读取
	logger   DecisionLogger
}

// NewPlatform 创建空平台。
func NewPlatform(logger DecisionLogger) *Platform {
	return &Platform{
		objects:  map[TypeID]map[ObjectID]map[AttrName]AttrCell{},
		declared: map[indexKey]bool{},
		indexes:  map[indexKey]*indexState{},
		logger:   logger,
	}
}

// QueryResult 是查询结果，DirtyNotice 非空表示索引可用但存在未修复的不一致。
type QueryResult struct {
	Objects     []ObjectID
	DirtyNotice string
}

// DeclareIndex 在对象类型上声明一个按值查找的属性索引。
// 声明后索引“存在但尚无完整审计记录”，在首次成功重建前查询得到
// KindRebuildingUnavailable，而非 KindIndexNotDeclared。
func (p *Platform) DeclareIndex(t TypeID, a AttrName) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := indexKey{t: t, a: a}
	if p.declared[k] {
		return
	}
	p.declared[k] = true
	p.indexes[k] = &indexState{
		status:  StatusDeclared,
		entries: map[Value]map[ObjectID]entryMeta{},
	}
	p.logLocked("declare_index", map[string]string{"type": string(t), "attr": string(a)},
		"声明索引：存在但首次重建前不可用", "ok")
}

// Put 对对象属性生效一次写入，返回其全局单调序号。
func (p *Platform) Put(t TypeID, obj ObjectID, a AttrName, v Value) Write {
	p.mu.Lock()
	w := p.applyWriteLocked(t, obj, a, v, OpPut)
	basis := "seq 单调分配，对象与可用索引同临界区更新"
	if st := p.indexes[indexKey{t: t, a: a}]; st != nil && st.pending != nil {
		basis = "seq 单调分配，对象生效；该索引重建中，此写入归入增量范围待按序重放"
	}
	p.mu.Unlock()
	p.log("put", w, basis, w)
	return w
}

// DeleteAttr 对对象属性生效一次删除（墓碑）。
func (p *Platform) DeleteAttr(t TypeID, obj ObjectID, a AttrName) Write {
	p.mu.Lock()
	w := p.applyWriteLocked(t, obj, a, "", OpDelete)
	basis := "seq 单调分配，墓碑写入"
	if st := p.indexes[indexKey{t: t, a: a}]; st != nil && st.pending != nil {
		basis = "seq 单调分配，墓碑生效；该索引重建中，此删除归入增量范围"
	}
	p.mu.Unlock()
	p.log("delete", w, basis, w)
	return w
}

// Query 按值查询索引，实现拒绝优先级：
// 未声明 > 重建中/失败不可用 > 可用但被标记不一致（仍返回并声明）> 正常。
func (p *Platform) Query(t TypeID, a AttrName, v Value) (*QueryResult, *Error) {
	p.mu.Lock()
	k := indexKey{t: t, a: a}
	st, declared := p.indexes[k]
	if !declared {
		p.mu.Unlock()
		err := &Error{Kind: KindIndexNotDeclared,
			Msg: "对象类型未声明该索引：索引不存在"}
		p.log("query", queryInput{t, a, v}, "优先级1：未声明（不存在）", err)
		return nil, err
	}
	if st.status != StatusAvailable {
		reason := "索引正在重建且尚无完整审计记录"
		if st.status == StatusFailed {
			reason = "上次重建中途失败，无完整审计记录，整体不可用"
		}
		p.mu.Unlock()
		err := &Error{Kind: KindRebuildingUnavailable, Msg: reason}
		p.log("query", queryInput{t, a, v}, "优先级2：重建中/失败不可用", err)
		return nil, err
	}
	var out []ObjectID
	for obj := range st.entries[v] {
		out = append(out, obj)
	}
	res := &QueryResult{Objects: out}
	basis := "优先级3/正常：索引可用"
	if st.flaggedDirty {
		res.DirtyNotice = "索引曾被复核标记存在未修复的条目级不一致，结果仅供参考"
		basis = "优先级3：可用但已标记不一致，仍返回并声明"
	}
	p.mu.Unlock()
	p.log("query", queryInput{t, a, v}, basis, res)
	return res, nil
}

type queryInput struct {
	Type TypeID
	Attr AttrName
	V    Value
}

// RebuildHandle 表示一次进行中的重建。
type RebuildHandle struct {
	p   *Platform
	key indexKey
}
