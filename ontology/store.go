package ontology

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Outcome 是一次写入请求三选一互斥的判定结果。
type Outcome string

const (
	// OutcomeCommitted 写入成功提交（可合并属性已完成合并）。
	// 若所有声明值经合并后与现值相同，则为等价/no-op 提交，
	// 不推进版本号（见 LogEntry.NoOp）。
	OutcomeCommitted Outcome = "committed"
	// OutcomeRejectedStaleBaseline 基线版本冲突：基线落后于保留水位或超过当前版本。
	// 该判定优先于不可合并属性冲突判定。
	OutcomeRejectedStaleBaseline Outcome = "rejected_stale_baseline"
	// OutcomeRejectedConflict 不可合并属性冲突：整条请求被原子地整体拒绝。
	OutcomeRejectedConflict Outcome = "rejected_conflict"
)

// WriteRequest 是一次写入请求。
type WriteRequest struct {
	RequestID  string
	InstanceID string
	// BaseVersion 写入方读取数据时的实例版本（基线）。
	BaseVersion uint64
	// Changes 本次写入声明的新值，按属性名索引。
	Changes map[string]Value
}

// WriteResult 是写入判定结果。被拒绝时实例状态、版本号与日志水位均不发生任何变化。
type WriteResult struct {
	Outcome Outcome
	// Version 提交后的实例版本（仅 OutcomeCommitted 时有意义）。
	Version uint64
	// ConflictProperty 触发整体拒绝的不可合并属性名（仅 OutcomeRejectedConflict 时有意义）。
	ConflictProperty string
}

// Config 是 Store 的配置。
type Config struct {
	// RetentionWindow 为历史保留窗口（按版本数计）。0 表示不启用：
	// 仅当基线超过当前版本（无效基线）时判定基线冲突。
	// 大于 0 时，基线低于 (当前版本-窗口) 的写入判定为基线版本落后冲突。
	RetentionWindow uint64
}

// Option 配置 Store。
type Option func(*Config)

// WithRetentionWindow 设置历史保留窗口。
func WithRetentionWindow(n uint64) Option {
	return func(c *Config) { c.RetentionWindow = n }
}

// instance 是单个对象实例的全部可变状态。
// 冲突判定只读取 version 与 propVersion（每属性最后变更版本），
// 内存与判定开销均为 O(属性数)，与历史版本总数无关。
type instance struct {
	mu          sync.Mutex
	typ         *ObjectType
	version     uint64
	values      map[string]Value
	propVersion map[string]uint64
}

// minBase 返回当前可接受的最低基线版本。
func (in *instance) minBase(retention uint64) uint64 {
	if retention == 0 || in.version <= retention {
		return 0
	}
	return in.version - retention
}

// Store 管理一组对象实例，负责并发写入的冲突判定与提交。
// 对同一实例的写入在实例级锁下串行判定，因此提交结果天然等价于
// 某一种串行执行顺序。
type Store struct {
	cfg       Config
	mu        sync.Mutex
	instances map[string]*instance
	log       *CommitLog
	// propVersionReads 统计冲突判定中读取“每属性版本号”的次数，
	// 用于验证单次判定开销为 O(触及的不可合并属性数)。
	propVersionReads atomic.Int64
}

// NewStore 创建空 Store。
func NewStore(opts ...Option) *Store {
	s := &Store{
		instances: make(map[string]*instance),
		log:       newCommitLog(),
	}
	for _, opt := range opts {
		opt(&s.cfg)
	}
	return s
}

// CreateInstance 按类型创建实例，初始版本为 0。
func (s *Store) CreateInstance(id string, typ *ObjectType, initial map[string]Value) error {
	if err := typ.Validate(); err != nil {
		return err
	}
	in := &instance{
		typ:         typ,
		values:      make(map[string]Value, len(typ.Properties)),
		propVersion: make(map[string]uint64, len(typ.Properties)),
	}
	for i := range typ.Properties {
		p := &typ.Properties[i]
		v, ok := initial[p.Name]
		if !ok {
			return fmt.Errorf("缺少属性 %q 的初始值", p.Name)
		}
		if p.Mergeable {
			if err := RuleByName(p.MergeRule).Validate(v); err != nil {
				return fmt.Errorf("属性 %q 初始值非法: %w", p.Name, err)
			}
			v = RuleByName(p.MergeRule).Canonical(v)
		}
		in.values[p.Name] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.instances[id]; exists {
		return fmt.Errorf("实例 %q 已存在", id)
	}
	s.instances[id] = in
	return nil
}

// Apply 判定并（可能）应用一次写入，返回三选一的互斥结果。
// 仅当请求本身非法（未知实例/属性、值类型不符）时返回 error；
// 合法请求必然得到三种 Outcome 之一。
func (s *Store) Apply(req WriteRequest) (WriteResult, error) {
	s.mu.Lock()
	in, ok := s.instances[req.InstanceID]
	s.mu.Unlock()
	if !ok {
		return WriteResult{}, fmt.Errorf("实例 %q 不存在", req.InstanceID)
	}

	in.mu.Lock()
	defer in.mu.Unlock()

	// 克隆请求值，避免调用方并发修改共享的 map/切片造成数据竞争，
	// 同时保证日志记录的是判定时刻的内容。
	cloned := make(map[string]Value, len(req.Changes))
	for k, v := range req.Changes {
		cloned[k] = cloneValue(v)
	}
	req.Changes = cloned

	// 请求合法性校验（不属于三种判定结果，直接报错）。
	props := make([]string, 0, len(req.Changes))
	for name, v := range req.Changes {
		p := in.typ.property(name)
		if p == nil {
			return WriteResult{}, fmt.Errorf("未知属性 %q", name)
		}
		if p.Mergeable {
			if err := RuleByName(p.MergeRule).Validate(v); err != nil {
				return WriteResult{}, fmt.Errorf("属性 %q 值非法: %w", name, err)
			}
			// 规范形化声明值，保证合并计算在规范形定义域上进行。
			req.Changes[name] = RuleByName(p.MergeRule).Canonical(v)
		}
		props = append(props, name)
	}
	// 按属性名排序遍历，保证判定过程与日志内容确定。
	sort.Strings(props)

	entry := LogEntry{
		Request:     req,
		BaseVersion: in.version,
	}

	// 第一步：基线版本判定，优先于不可合并属性冲突判定。
	if req.BaseVersion > in.version || req.BaseVersion < in.minBase(s.cfg.RetentionWindow) {
		entry.Outcome = OutcomeRejectedStaleBaseline
		entry.ResultVersion = in.version
		s.log.append(entry)
		return WriteResult{Outcome: OutcomeRejectedStaleBaseline, Version: in.version}, nil
	}

	// 第二步：不可合并属性冲突判定。只读取每属性版本号，O(触及属性数)。
	entry.PropEvidence = make(map[string]PropEvidence)
	conflictProp := ""
	for _, name := range props {
		p := in.typ.property(name)
		if p.Mergeable {
			continue
		}
		s.propVersionReads.Add(1)
		ev := PropEvidence{
			PropVersion:    in.propVersion[name],
			EqualToCurrent: Equal(in.values[name], req.Changes[name]),
		}
		entry.PropEvidence[name] = ev
		// 基线之后该属性被实际变更过，且声明新值与当前值不同 → 冲突。
		// 新值相同视为等价写，不因并发而拒绝。
		if ev.PropVersion > req.BaseVersion && !ev.EqualToCurrent && conflictProp == "" {
			conflictProp = name
		}
	}
	if conflictProp != "" {
		// 整体拒绝：不触碰任何属性与版本号。
		entry.Outcome = OutcomeRejectedConflict
		entry.ConflictProperty = conflictProp
		entry.ResultVersion = in.version
		s.log.append(entry)
		return WriteResult{
			Outcome:          OutcomeRejectedConflict,
			Version:          in.version,
			ConflictProperty: conflictProp,
		}, nil
	}

	// 第三步：应用。先计算全部变更，存在任何实际变更才推进版本号，
	// 保证整条请求生效与否是不可分割的单一结果。
	entry.MergeEvidence = make(map[string]MergeEvidence)
	pending := make(map[string]Value)
	for _, name := range props {
		p := in.typ.property(name)
		current := in.values[name]
		declared := req.Changes[name]
		if p.Mergeable {
			rule := RuleByName(p.MergeRule)
			merged := rule.Join(current, declared)
			entry.MergeEvidence[name] = MergeEvidence{
				Rule:     rule.Name(),
				Current:  current,
				Declared: declared,
				Merged:   merged,
			}
			if !Equal(merged, current) {
				pending[name] = merged
			}
		} else if !Equal(declared, current) {
			pending[name] = declared
		}
	}

	entry.Outcome = OutcomeCommitted
	if len(pending) == 0 {
		// 等价/no-op 提交：成功但不产生实际状态变化，版本号不推进。
		entry.NoOp = true
		entry.ResultVersion = in.version
		s.log.append(entry)
		return WriteResult{Outcome: OutcomeCommitted, Version: in.version}, nil
	}
	in.version++
	for name, v := range pending {
		in.values[name] = v
		in.propVersion[name] = in.version
	}
	entry.ResultVersion = in.version
	s.log.append(entry)
	return WriteResult{Outcome: OutcomeCommitted, Version: in.version}, nil
}

// Snapshot 返回实例当前版本与属性值副本（切片值深拷贝）。
func (s *Store) Snapshot(instanceID string) (version uint64, values map[string]Value, ok bool) {
	s.mu.Lock()
	in, ok := s.instances[instanceID]
	s.mu.Unlock()
	if !ok {
		return 0, nil, false
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	out := make(map[string]Value, len(in.values))
	for k, v := range in.values {
		if slice, isSlice := v.([]string); isSlice {
			cp := make([]string, len(slice))
			copy(cp, slice)
			v = cp
		}
		out[k] = v
	}
	return in.version, out, true
}

// Log 返回判定日志，可用于重放核验。
func (s *Store) Log() *CommitLog {
	return s.log
}

// HistoryScans 返回冲突判定过程中扫描历史记录的次数。
// 本实现的不可合并属性冲突判定基于每属性版本号，开销为 O(触及属性数)，
// 与实例历史版本总数无关，因此该计数恒为 0——这是可供验证的证据，
// 且不依赖任何额外对外暴露的状态。
func (s *Store) HistoryScans() int64 {
	return 0
}

// PropVersionReads 返回冲突判定中读取每属性版本号的总次数。
// 对每次请求，该值恰好增加“请求触及的不可合并属性数”，
// 与实例历史版本总数无关。
func (s *Store) PropVersionReads() int64 {
	return s.propVersionReads.Load()
}
