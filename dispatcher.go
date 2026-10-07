package ontology

import (
	"errors"
	"fmt"
	"sync"
)

// Dispatcher 是多态分派机制的入口：统一维护注册表、对象实例与全序事件。
//
// 一把互斥锁串行化所有会改变“分派视图”的元数据操作（注册、放弃、撤销、
// 类型/对象定义）以及调用发起时的分派段。这样每个操作都有一个确定的
// 线性化点（获取锁的时刻），全部操作天然等价于按事件日志序号的全序串行
// 执行，不可能出现介于两次替换之间、无法对应任何确定时刻的快照。
//
// 执行逻辑本身在锁外运行；它通过分派段拿到的不可变快照与 installNode
// 指针固定，替换只生成新快照、不改写旧节点，故在途调用执行到底。
type Dispatcher struct {
	mu      sync.Mutex
	reg     *Registry
	snap    *snapshot
	types   map[string]*ObjectType
	objects map[string]*Object
	seq     int64
	log     []Event
	records []DispatchRecord
}

// Event 是全序事件日志中的一条（注册/放弃/创建/撤销/调用）。
type Event struct {
	Seq     int64
	Kind    string
	Action  string
	Type    string
	Object  string
	LogicID string
	Ver     int64
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		reg:     newRegistry(),
		snap:    newSnapshot(),
		types:   map[string]*ObjectType{},
		objects: map[string]*Object{},
	}
}

func (d *Dispatcher) DeclareAction(a Action) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.reg.declare(a)
}

func (d *Dispatcher) DefineType(name, parent string) error {
	if name == "" {
		return errors.New("define type: empty name")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, dup := d.types[name]; dup {
		return fmt.Errorf("define type: %q already defined", name)
	}
	var parentType *ObjectType
	if parent != "" {
		var ok bool
		parentType, ok = d.types[parent]
		if !ok {
			return fmt.Errorf("define type: parent %q not defined", parent)
		}
	}
	d.types[name] = &ObjectType{Name: name, Parent: parentType}
	return nil
}

func (d *Dispatcher) CreateObject(id, typeName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, dup := d.objects[id]; dup {
		return fmt.Errorf("create object: %q already exists", id)
	}
	t, ok := d.types[typeName]
	if !ok {
		return fmt.Errorf("create object: type %q not defined", typeName)
	}
	d.objects[id] = &Object{id: id, type_: t}
	d.seq++
	d.log = append(d.log, Event{Seq: d.seq, Kind: "create", Object: id, Type: typeName})
	return nil
}

// InstallLogic 注册或热替换执行逻辑；relax 为放宽范围声明（无放宽时传 nil）。
// 线性化点：进入本方法获取锁的时刻；此前在途调用持有旧快照与旧节点指针。
func (d *Dispatcher) InstallLogic(action, typeName string, logic Logic, relax RelaxScope) (Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reg.action(action) == nil {
		return Event{}, fmt.Errorf("install: action %q not declared", action)
	}
	if _, ok := d.types[typeName]; !ok {
		return Event{}, fmt.Errorf("install: type %q not defined", typeName)
	}
	if logic.Execute == nil {
		return Event{}, errors.New("install: logic.Execute is nil")
	}
	d.seq++
	d.snap = install(d.snap, action, typeName, logic, relax, d.seq)
	ev := Event{Seq: d.seq, Kind: "install", Action: action, Type: typeName, LogicID: logic.ID, Ver: d.snap.ver}
	d.log = append(d.log, ev)
	return ev, nil
}

// Waive 将具体类型标记为对某动作显式放弃直接处理。
func (d *Dispatcher) Waive(action, typeName string) (Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reg.action(action) == nil {
		return Event{}, fmt.Errorf("waive: action %q not declared", action)
	}
	if _, ok := d.types[typeName]; !ok {
		return Event{}, fmt.Errorf("waive: type %q not defined", typeName)
	}
	d.seq++
	d.snap = waive(d.snap, action, typeName, d.seq)
	ev := Event{Seq: d.seq, Kind: "waive", Action: action, Type: typeName, Ver: d.snap.ver}
	d.log = append(d.log, ev)
	return ev, nil
}

// RevokeObject 撤销对象实例。与调用发起共用同一把锁与序号，因此
// “查找过程中被撤销”可以通过全序精确判定，而不依赖时序猜测。
func (d *Dispatcher) RevokeObject(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	obj, ok := d.objects[id]
	if !ok {
		return fmt.Errorf("revoke: object %q not found", id)
	}
	if obj.Revoked() {
		return fmt.Errorf("revoke: object %q already revoked", id)
	}
	obj.revoke()
	d.seq++
	d.log = append(d.log, Event{Seq: d.seq, Kind: "revoke", Object: id})
	return nil
}

// Invoke 发起一次调用。
//
// 调用发起时刻（线性化点）= 进入分派段获取 d.mu 的时刻。在该临界区内
// 原子完成：取得当前不可变快照、读取实例当前绑定类型与撤销状态、沿继承
// 链查找、记录结论。随后在锁外用“那一刻固定下来”的逻辑执行到底。
//
// 判定优先级（严格按需求）：
//  1. 两种无法分派在执行任何逻辑之前判定；
//  2. 查找段对象被并发撤销导致整体失败；
//  3. 分派成功之后才可能出现的前置/后置/执行错误。
func (d *Dispatcher) Invoke(objectID, action string, in Input) (Output, *DispatchRecord) {
	d.mu.Lock()
	d.seq++
	invokeSeq := d.seq
	snap := d.snap

	rec := &DispatchRecord{Seq: invokeSeq, Action: action, ObjectID: objectID}
	d.log = append(d.log, Event{Seq: invokeSeq, Kind: "invoke", Action: action, Object: objectID, Ver: snap.ver})

	obj := d.objects[objectID]
	if obj == nil {
		rec.Status = DispatchStatus("invoke:object-not-found")
		rec.Err = fmt.Sprintf("object %q not found", objectID)
		d.finishLocked(rec)
		d.mu.Unlock()
		return nil, rec
	}
	concrete := obj.type_
	rec.ConcreteType = concrete.Name
	rec.RegistryVer = snap.ver

	chain := ancestorChain(concrete)
	node, hitType, basis, path, steps, sawWaive := resolveOnChain(snap, action, chain)
	rec.Path = path
	rec.Steps = steps

	// 撤销判定在同一个临界区、同一张全序快照内完成：
	// 若全序中撤销排在本次 invoke 之前，则本次整体失败，绝不执行写入。
	revokedAtDispatch := obj.Revoked()

	if basis == HitNone {
		rec.HitBasis = HitNone
		if sawWaive {
			rec.Status = StatusNoDispatchWaived
			rec.Err = "type explicitly waived direct handling and no inherited logic registered"
		} else {
			rec.Status = StatusNoDispatchNone
			rec.Err = "neither concrete type nor any ancestor registered logic"
		}
		d.finishLocked(rec)
		d.mu.Unlock()
		return nil, rec
	}

	if revokedAtDispatch {
		rec.HitBasis = basis
		rec.HitType = hitType
		rec.LogicID = node.logic.ID
		rec.Status = StatusRevokedDuringLookup
		rec.Err = "object revoked during dispatch (revoke precedes invoke in total order)"
		d.finishLocked(rec)
		d.mu.Unlock()
		return nil, rec
	}

	// 分派成功：固定具体逻辑指针与对象引用，之后全部在锁外完成。
	logic := node.logic
	rec.HitBasis = basis
	rec.HitType = hitType
	rec.LogicID = logic.ID
	d.mu.Unlock()

	// 以下任何替换都不会影响 logic：替换生成新快照，从不原地改写本节点。
	if logic.Pre != nil {
		if err := logic.Pre(obj, in); err != nil {
			rec.Status = StatusPreconditionFailed
			rec.Err = err.Error()
			d.record(rec)
			return nil, rec
		}
	}
	out, err := logic.Execute(obj, in)
	if err != nil {
		rec.Status = StatusExecuteFailed
		rec.Err = err.Error()
		d.record(rec)
		return nil, rec
	}
	if logic.Post != nil {
		if err := logic.Post(obj, in, out); err != nil {
			rec.Status = StatusPostconditionFailed
			rec.Err = err.Error()
			d.record(rec)
			return nil, rec
		}
	}
	rec.Status = StatusOK
	rec.Output = out
	d.record(rec)
	return out, rec
}

// finishLocked 在持锁状态下保存一条记录（用于分派段失败路径）。
func (d *Dispatcher) finishLocked(rec *DispatchRecord) {
	d.records = append(d.records, *rec)
}

func (d *Dispatcher) record(rec *DispatchRecord) {
	d.mu.Lock()
	d.records = append(d.records, *rec)
	d.mu.Unlock()
}

func (d *Dispatcher) EventLog() []Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]Event, len(d.log))
	copy(cp, d.log)
	return cp
}

// Records 返回全部调用记录的副本，供事后核对。
func (d *Dispatcher) Records() []DispatchRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]DispatchRecord, len(d.records))
	copy(cp, d.records)
	return cp
}

// currentSnapshot 供审计等只读场景在锁内取得当前不可变快照。
func (d *Dispatcher) currentSnapshotLocked() *snapshot { return d.snap }
