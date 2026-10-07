package lifecycle

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Instance 是一个对象实例的运行时状态。
type Instance struct {
	ID    string
	Type  string
	State string
	Attrs map[string]AttrValue
	Clock int64

	// out 只由该实例自身的锁保护（提交方必持有属主锁）。
	// 读取方在属主锁下整体替换或浅读，保证并发无竞态。
	out map[string]map[string]bool
}

// Link 是两个实例之间一条有向、带类型的链接。
type Link struct {
	Type   string
	FromID string
	ToID   string
}

// OpKind 是处理单元内单个操作的种类。
type OpKind int

const (
	// OpFire 触发一条迁移。
	OpFire OpKind = iota + 1
	// OpSetAttr 修改实例属性。
	OpSetAttr
	// OpAddLink 新增一条链接。
	OpAddLink
	// OpDelLink 删除一条已存在链接（终态实例也允许）。
	OpDelLink
)

// Op 是一个处理单元（Batch）内的单个操作。
type Op struct {
	Kind       OpKind
	InstanceID string
	Transition string
	Attr       string
	Value      AttrValue
	Link       Link
}

// Fire 构造一个迁移触发操作。
func Fire(instanceID, transition string) Op {
	return Op{Kind: OpFire, InstanceID: instanceID, Transition: transition}
}

// SetAttr 构造一个属性修改操作。
func SetAttr(instanceID, attr string, value AttrValue) Op {
	return Op{Kind: OpSetAttr, InstanceID: instanceID, Attr: attr, Value: value}
}

// AddLink 构造一个新增链接操作。
func AddLink(linkType, fromID, toID string) Op {
	return Op{Kind: OpAddLink, Link: Link{Type: linkType, FromID: fromID, ToID: toID}}
}

// DelLink 构造一个删除链接操作。
func DelLink(linkType, fromID, toID string) Op {
	return Op{Kind: OpDelLink, Link: Link{Type: linkType, FromID: fromID, ToID: toID}}
}

// FiredStep 记录一次实际生效的迁移环节（含级联随迁）。
type FiredStep struct {
	InstanceID string
	Type       string
	Transition string
	From       string
	To         string
	CascadeOf  string // 非空表示它是由哪个实例的迁移级联触发
}

// Outcome 是单个操作的判定结果。
type Outcome struct {
	Index  int
	OK     bool
	Err    *Error
	Fired  []FiredStep
	Reason []string // 判定依据（前置条件/基数/钩子的求值明细）
}

// BatchResult 是一个处理单元的整体结果。
type BatchResult struct {
	Committed bool
	Outcomes  []*Outcome
}

// Store 是实例与链接的内存存储。
type Store struct {
	mu    sync.RWMutex
	inst  map[string]*atomicPtr // id -> 实例当前版本
	entry map[string]*storeEntry
	clock atomic.Int64
	seq   atomic.Int64
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		inst:  map[string]*atomicPtr{},
		entry: map[string]*storeEntry{},
	}
}

// atomicPtr 是单个实例当前版本的原子槽。提交时构造新版本实例
// （含其属主邻接表的拷贝）后整体 StorePointer，读端无锁 LoadPointer。
type atomicPtr struct {
	v unsafe.Pointer // *Instance
}

func (a *atomicPtr) load() *Instance {
	return (*Instance)(atomic.LoadPointer(&a.v))
}

func (a *atomicPtr) store(in *Instance) {
	atomic.StorePointer(&a.v, unsafe.Pointer(in))
}

// Engine 是生命周期状态机引擎。
type Engine struct {
	schema *Schema
	store  *Store
	logger Logger
	fp     failpoint
}

// NewEngine 创建引擎。
func NewEngine(schema *Schema, store *Store, logger Logger) *Engine {
	if logger == nil {
		logger = DiscardLogger{}
	}
	return &Engine{schema: schema, store: store, logger: logger}
}
