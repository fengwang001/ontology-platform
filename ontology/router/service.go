// Package router 负责对象实例的读写路由与一致性仲裁。
//
// Service 持有对象类型的迁移声明(migration.Declaration)与全部实例,
// 所有读写、删除、迁移声明变更、回填应用都经过同一把互斥锁仲裁,
// 因此任意并发操作集合的最终效果都等价于按某个全局串行顺序逐一应用,
// 重放同一操作序列必然得到完全相同的最终状态与读取结果。
//
// 实例数据约定:一旦写入 Service 的属性 map 即视为不可变,
// 任何修改都先复制再整体替换,因此读取与现算视图无需防御性深拷贝。
package router

import (
	"fmt"
	"sort"
	"sync"

	"ontology/ontology/migration"
)

// Version 标识一次读写请求所依据的对象类型结构版本。
type Version int

const (
	// VersionOld 旧版本结构。
	VersionOld Version = iota
	// VersionNew 新版本结构。
	VersionNew
)

func (v Version) String() string {
	switch v {
	case VersionOld:
		return "old"
	case VersionNew:
		return "new"
	default:
		return fmt.Sprintf("version(%d)", int(v))
	}
}

func (v Version) valid() bool { return v == VersionOld || v == VersionNew }

// instance 是单个对象实例的内部记录。
type instance struct {
	// data 是实例的当前属性集:未回填时为旧版本结构,已回填后为新版本结构。
	// 该 map 一旦存入即不可变,修改必须整体替换。
	data map[string]any
	// migrated 表示实例是否已回填到新版本结构。
	migrated bool
	// generation 是单调递增的版本号,回填应用时据此做 compare-and-swap,
	// 识别「回填即将应用时被正常写入抢先」的竞争。
	generation uint64
}

// Service 是读写路由与一致性仲裁核心,对本对象类型的所有操作入口。
type Service struct {
	mu        sync.Mutex
	decl      *migration.Declaration
	instances map[string]*instance
}

// NewService 创建一个对象类型的读写仲裁服务,decl 为其迁移声明。
func NewService(decl *migration.Declaration) *Service {
	return &Service{
		decl:      decl,
		instances: make(map[string]*instance),
	}
}

// validateProps 校验一组属性赋值在给定版本下是否全部可写。
// 废弃属性在两个版本下都不可写:它即将被移除,允许写入只会被转换丢弃,
// 拒绝比静默丢数据更诚实。
func (s *Service) validateProps(v Version, props map[string]any) error {
	if !v.valid() {
		return fmt.Errorf("%w: unknown version %d", ErrInvalidArgument, int(v))
	}
	for p := range props {
		if s.decl.IsDeprecated(p) {
			return fmt.Errorf("%w: property %q is deprecated and not writable in any version", ErrInvalidArgument, p)
		}
		if v == VersionOld && s.decl.IsAdded(p) {
			return fmt.Errorf("%w: property %q only exists in the new version", ErrInvalidArgument, p)
		}
	}
	return nil
}

// validateCreateProps 校验创建实例时的初始属性集。
// 与写入不同,以旧版本结构创建允许携带废弃属性——那正是待迁移的历史数据。
func (s *Service) validateCreateProps(v Version, props map[string]any) error {
	if !v.valid() {
		return fmt.Errorf("%w: unknown version %d", ErrInvalidArgument, int(v))
	}
	for p := range props {
		if v == VersionNew && s.decl.IsDeprecated(p) {
			return fmt.Errorf("%w: property %q is deprecated and not writable in the new version", ErrInvalidArgument, p)
		}
		if v == VersionOld && s.decl.IsAdded(p) {
			return fmt.Errorf("%w: property %q only exists in the new version", ErrInvalidArgument, p)
		}
	}
	return nil
}

// Create 创建实例。以新版本结构创建的实例直接视为已回填。
// 拒绝次序:参数非法在前,实例已存在(同样按参数非法报告)次之。
func (s *Service) Create(id string, v Version, props map[string]any) error {
	if err := s.validateCreateProps(v, props); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.instances[id]; ok {
		return fmt.Errorf("%w: instance %q already exists", ErrInvalidArgument, id)
	}
	data := make(map[string]any, len(props))
	for p, val := range props {
		data[p] = val
	}
	s.instances[id] = &instance{data: data, migrated: v == VersionNew}
	return nil
}

// Read 按指定版本结构读取实例。
// 对尚未回填的实例按新版本读取时,即时按迁移声明现算出新版本视图返回,
// 不等待异步回填;读取本身不改变实例的回填状态。
// 拒绝次序:参数非法(版本非法)在前,实例不存在次之。
func (s *Service) Read(id string, v Version) (map[string]any, error) {
	if !v.valid() {
		return nil, fmt.Errorf("%w: unknown version %d", ErrInvalidArgument, int(v))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return s.viewLocked(inst, v), nil
}

// viewLocked 计算实例在指定版本下的视图,调用方须持有锁。
func (s *Service) viewLocked(inst *instance, v Version) map[string]any {
	var view map[string]any
	switch {
	case inst.migrated && v == VersionOld:
		view = s.decl.ReverseView(inst.data)
	case !inst.migrated && v == VersionNew:
		view = s.decl.ForwardView(inst.data)
	default:
		view = inst.data
	}
	out := make(map[string]any, len(view))
	for p, val := range view {
		out[p] = val
	}
	return out
}

// Write 按指定版本结构对实例写入一组属性赋值。
//
// 无论新旧版本,写入都会把实例转换为新版本结构(即视为一次回填):
// 旧版本写入先作用于旧视图,再按迁移声明转换存储,因此旧结构的写入路径
// 不会绕开新结构的约束与默认值规则。写入完成后实例视为已回填。
// 拒绝次序:参数非法在前,实例不存在次之;被拒绝的写入不改变任何状态。
func (s *Service) Write(id string, v Version, props map[string]any) error {
	if err := s.validateProps(v, props); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	s.applyWriteLocked(inst, v, props)
	return nil
}

// applyWriteLocked 在持锁状态下应用写入,把实例转换为新版本结构。
func (s *Service) applyWriteLocked(inst *instance, v Version, props map[string]any) {
	if !inst.migrated {
		oldData := inst.data
		base := oldData
		if v == VersionNew {
			base = s.decl.ForwardView(oldData)
		}
		next := make(map[string]any, len(base)+len(props))
		for p, val := range base {
			next[p] = val
		}
		for p, val := range props {
			next[p] = val
		}
		if v == VersionOld {
			next = s.decl.ForwardView(next)
		}
		inst.data = next
		inst.migrated = true
		s.decl.MarkEffective(oldData)
	} else {
		base := inst.data
		if v == VersionOld {
			base = s.decl.ReverseView(base)
		}
		next := make(map[string]any, len(base)+len(props))
		for p, val := range base {
			next[p] = val
		}
		for p, val := range props {
			next[p] = val
		}
		if v == VersionOld {
			next = s.decl.ForwardView(next)
		}
		inst.data = next
	}
	inst.generation++
}

// Delete 删除实例。已删除的实例不会被回填流程复活。
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.instances[id]; !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	delete(s.instances, id)
	return nil
}

// AmendDeclaration 变更迁移声明(追加新对应关系,或替换尚未生效的对应关系)。
// 声明层面的非法(自相矛盾、修改已生效的对应关系)一律报告为参数非法;
// 被拒绝的变更不改变声明与任何实例的状态。
func (s *Service) AmendDeclaration(batch []migration.Mapping) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.decl.Amend(batch); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	return nil
}

// Status 返回实例是否存在以及是否已回填,供回填调度与测试观测。
func (s *Service) Status(id string) (exists, migrated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return false, false
	}
	return true, inst.migrated
}

// PendingIDs 按确定性的内部顺序(字典序)返回尚未回填的实例,
// 回填流程按此顺序逐个处理,保证重放结果一致。
func (s *Service) PendingIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, inst := range s.instances {
		if !inst.migrated {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// InstanceIDs 按字典序返回全部现存实例 ID,供状态快照与测试对照。
func (s *Service) InstanceIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.instances))
	for id := range s.instances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
