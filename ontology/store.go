package ontology

import "sync"

// Store 是"读写路由与一致性仲裁"模块对外暴露的句柄。
//
// 所有读写、回填与声明变更都在单一互斥锁下全局串行化，因而并发操作的
// 最终效果等价于某个全局串行顺序；操作执行期间不释放锁，回填作为
// "一次特殊写入"与正常写入不可能同时半截应用，天然杜绝了
// "回填与写入都成功却视图矛盾"的情形。
type Store struct {
	mu        sync.Mutex
	decl      *declaration
	instances map[string]*instance
	q         *queue
}

type instance struct {
	id string
	// backfilled 为 true 表示数据已是新版本结构（newRaw）；
	// 为 false 表示仍是旧版本结构（oldRaw），新版本视图按当前声明即时现算。
	backfilled bool
	// convertedAt 是实例完成转换（回填或被旧结构写入等价转换）时的声明修订序号。
	convertedAt int64
	// kindsAtConversion 是转换时刻该实例所应用的属性对应关系快照，
	// 已回填实例的旧视图据此投影；声明后续追加的对应关系不回灌到该实例。
	kindsAtConversion map[AttrName]Kind
	// oldRaw/newRaw 按 backfilled 二选一存放原始属性。
	oldRaw Props
	newRaw Props
}

// NewStore 创建一个尚未发起迁移的存储。
func NewStore(objectType string, from, to Version) *Store {
	return &Store{
		decl:      newDeclaration(objectType, from, to),
		instances: make(map[string]*instance),
		q:         newQueue(),
	}
}

// StartMigration 发起迁移：校验并应用初始声明，随后把全部存量实例
// 按内部顺序（id 稳定排序）加入异步回填队列。
func (s *Store) StartMigration(m Migration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.decl.start(m); err != nil {
		return err
	}
	gen := s.decl.revision
	for _, id := range sortedIDs(s.instances) {
		inst := s.instances[id]
		if !inst.backfilled {
			s.q.push(id, gen)
		}
	}
	return nil
}

// AmendDeclaration 在迁移在途时追加/修订对应关系。
// 被拒绝时声明与任何实例状态都不改变。
func (s *Store) AmendDeclaration(mappings []Mapping) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.decl.started {
		return invalidf("cannot amend declaration before migration starts")
	}
	return s.decl.validateAndApply(mappings)
}

// Revision 返回当前声明修订序号（主要供测试/观测）。
func (s *Store) Revision() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.decl.revision
}

// validatePayload 先于实例存在性检查：校验一份按某版本结构提交的属性，
// 只允许出现该版本视图下合法的属性名，且不允许重复语义之外的脏数据。
// 迁移开始前不做结构限制（对象类型尚未声明任何对应关系）。
func (s *Store) validatePayload(v Version, p Props) error {
	if v != s.decl.from && v != s.decl.to {
		return invalidf("unknown version %d", v)
	}
	if !s.decl.started {
		if v != s.decl.from {
			return invalidf("migration has not started: only version %d is available", s.decl.from)
		}
		return nil
	}
	for attr, val := range p {
		m, known := s.decl.mapping(attr)
		if !known {
			continue // 未纳入迁移声明的属性，两版本视图中原样可见
		}
		switch v {
		case s.decl.to:
			if m.Kind == KindDrop && val.Set {
				return invalidf("attribute %q is dropped in the new version and cannot be written through it", attr)
			}
		case s.decl.from:
			if m.Kind == KindAdd && val.Set {
				return invalidf("attribute %q is newly added in the new version and cannot be written through the old one", attr)
			}
		}
	}
	return nil
}

// Create 以指定版本结构创建一个尚不存在的实例。
// 迁移在途时以旧版本结构创建的实例仍以旧结构存放并进入回填队列；
// 以新版本结构创建的实例直接就是已回填形态。
func (s *Store) Create(id string, v Version, p Props) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validatePayload(v, p); err != nil {
		return err
	}
	if _, exists := s.instances[id]; exists {
		return invalidf("instance %q already exists", id)
	}
	inst := &instance{id: id}
	if s.decl.started && v == s.decl.to {
		inst.backfilled = true
		inst.convertedAt = s.decl.revision
		inst.kindsAtConversion = s.kindSnapshot()
		inst.newRaw = cloneProps(p)
	} else {
		inst.oldRaw = cloneProps(p)
		if s.decl.started {
			s.q.push(id, s.decl.revision)
		}
	}
	s.instances[id] = inst
	return nil
}

// Read 按指定版本结构返回实例视图。
// 尚未回填的实例若按新版本读取，即时按当前声明现算视图，无需等待回填。
func (s *Store) Read(id string, v Version) (Props, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v != s.decl.from && v != s.decl.to {
		return nil, invalidf("unknown version %d", v)
	}
	inst, ok := s.instances[id]
	if !ok {
		return nil, notFoundf("instance %q", id)
	}
	return s.viewLocked(inst, v), nil
}

// viewLocked 必须在持锁状态下调用。
func (s *Store) viewLocked(inst *instance, v Version) Props {
	if inst.backfilled {
		// 已回填：新视图即存储本体；旧视图按转换时刻的对应关系投影，
		// 因此声明后来追加/修订的对应关系不会改变已回填实例。
		if v == s.decl.to {
			return cloneProps(inst.newRaw)
		}
		out := make(Props, len(inst.newRaw))
		for attr, val := range inst.newRaw {
			if k, snap := inst.kindsAtConversion[attr]; snap && k != KindKeep {
				continue // KindAdd：旧版本不存在
			}
			out[attr] = val
		}
		return out
	}
	// 未回填：旧视图即存储本体；新视图按当前声明做逐属性投影。
	// 投影只遍历实例自身属性（O(实例属性数)），从声明哈希表 O(1) 取对应关系，
	// 从不重放任何历史修订，开销与历史对应关系变更次数无关。
	if v == s.decl.from {
		return cloneProps(inst.oldRaw)
	}
	out := make(Props, len(inst.oldRaw))
	for attr, val := range inst.oldRaw {
		if m, known := s.decl.mapping(attr); known && m.Kind == KindDrop {
			continue // KindDrop：新版本不可见
		}
		out[attr] = val
	}
	return out
}

// Write 按指定版本结构整体写入已存在实例。
//
// 对尚未回填实例的旧结构写入不会直接落旧结构：先等价转换为新结构写入，
// 转换时补默认值、应用约束，并把实例标记为已回填（语义上即一次回填）。
func (s *Store) Write(id string, v Version, p Props) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validatePayload(v, p); err != nil {
		return err
	}
	inst, ok := s.instances[id]
	if !ok {
		return notFoundf("instance %q", id)
	}

	if v == s.decl.to || !s.decl.started {
		if !s.decl.started {
			inst.oldRaw = cloneProps(p)
		} else {
			// 新结构写入就是新版本下的整体替换：不注入任何旧存量值/默认值；
			// 但目标若是未回填实例，本次写入同样令其成为新版本形态（已回填）。
			if !inst.backfilled {
				inst.backfilled = true
				inst.convertedAt = s.decl.revision
				inst.kindsAtConversion = s.kindSnapshot()
				inst.oldRaw = nil
			}
			inst.newRaw = cloneProps(p)
		}
		return nil
	}

	// 旧结构写入。
	if inst.backfilled {
		// 已回填实例的旧结构写入：转换为新结构整体写入。
		next := make(Props, len(p))
		for attr, val := range p {
			if k, snap := inst.kindsAtConversion[attr]; snap && k != KindKeep {
				continue
			}
			next[attr] = val
		}
		inst.newRaw = next
		return nil
	}
	// 未回填实例的旧结构写入：等价转换为新结构写入后该实例即视为已回填。
	s.convertLocked(inst)
	// 以转换结果为底（已含新增属性默认值），再用本次写入覆盖。
	next := cloneProps(inst.newRaw)
	for attr, val := range p {
		if m, known := s.decl.mapping(attr); known && m.Kind == KindDrop {
			continue
		}
		next[attr] = val
	}
	inst.newRaw = next
	return nil
}

// convertLocked 用当前声明把一个未回填实例转换为新结构：
// 保留属性原样带入、新增属性补默认值、废弃属性不进入新结构，并冻结
// 本次应用到的全部对应关系。仅在实例确实发生转换时调用一次。
func (s *Store) convertLocked(inst *instance) {
	next := make(Props, len(inst.oldRaw))
	used := make([]AttrName, 0, len(inst.oldRaw))
	for attr, val := range inst.oldRaw {
		m, known := s.decl.mapping(attr)
		if known {
			used = append(used, attr)
			if m.Kind == KindDrop {
				continue
			}
		}
		next[attr] = val
	}
	// 新增属性补默认值：这是转换（写入路径）成本，不属于"读时即时现算"。
	for attr, m := range s.decl.active {
		if m.Kind == KindAdd {
			if _, exists := next[attr]; !exists {
				next[attr] = m.Default
			}
			used = append(used, attr)
		}
	}
	inst.newRaw = next
	inst.backfilled = true
	inst.convertedAt = s.decl.revision
	inst.kindsAtConversion = s.kindSnapshot()
	inst.oldRaw = nil
	s.decl.freeze(used)
}

// kindSnapshot 记录当前声明中全部属性的 Kind，供已回填实例投影旧视图。
func (s *Store) kindSnapshot() map[AttrName]Kind {
	snap := make(map[AttrName]Kind, len(s.decl.active))
	for attr, m := range s.decl.active {
		snap[attr] = m.Kind
	}
	return snap
}

// Delete 删除实例。删除对任何实例都合法（删除幂等：不存在按 ErrNotFound 报告，
// 以满足"目标实例不存在次之"的错误次序）。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.instances[id]; !ok {
		return notFoundf("instance %q", id)
	}
	delete(s.instances, id)
	return nil
}

// BackfillOutcome 描述一次回填触发的结果，供调用方/测试打印与断言。
type BackfillOutcome struct {
	ID      string
	DidWork bool
	// Skipped 为 true 表示出队时实例已被抢先回填或已删除：跳过，不是错误。
	Skipped    bool
	Reason     string
	Backfilled bool
}

// RunBackfill 执行回填队列的一次步骤（异步回填模块的一次触发）。
func (s *Store) RunBackfill() BackfillOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.q.pop()
	if !ok {
		return BackfillOutcome{DidWork: false}
	}
	inst, exists := s.instances[item.id]
	if !exists {
		// 回填期间被删除：识别并跳过，不凭空复活。
		return BackfillOutcome{ID: item.id, DidWork: true, Skipped: true, Reason: "deleted-before-backfill"}
	}
	if inst.backfilled {
		// 已被某次正常写入抢先回填：识别并跳过，不覆盖、不报错。
		return BackfillOutcome{ID: item.id, DidWork: true, Skipped: true, Reason: "already-backfilled-by-write"}
	}
	s.convertLocked(inst)
	return BackfillOutcome{ID: item.id, DidWork: true, Backfilled: true, Reason: "backfilled"}
}

// PendingBackfill 返回回填队列当前长度。
func (s *Store) PendingBackfill() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 队列允许保留陈旧项（待出队时显式跳过），因此"未回填数量"以
	// 实际实例状态为准，而不是队列长度。
	n := 0
	for _, inst := range s.instances {
		if !inst.backfilled {
			n++
		}
	}
	return n
}

// IsBackfilled 返回某实例当前是否已回填（不存在返回 false,false）。
func (s *Store) IsBackfilled(id string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return false, false
	}
	return inst.backfilled, true
}

func notFoundf(format string, args ...any) error {
	return errNotFound{message: sprintf(format, args...)}
}

type errNotFound struct{ message string }

func (e errNotFound) Error() string { return e.message }
func (e errNotFound) Is(target error) bool {
	return target == ErrNotFound
}
