package compensate

import "sync"

// Graph 是被副作用修改的对象图：objectID -> property -> value。
type Graph struct {
	mu    sync.Mutex
	data  map[string]map[string]string
	locks *LockManager
}

func NewGraph() *Graph {
	return &Graph{data: map[string]map[string]string{}, locks: newLockManager()}
}

// slotKey 是对象图上的最小互斥粒度（某对象的某属性）。
func slotKey(objectID, property string) string { return objectID + "\x00" + property }

// Acquire 以严格 2PL 方式为某动作持有槽位锁直到 ReleaseAll。骨架占位。
func (g *Graph) Acquire(actionID, objectID, property string) {
	g.locks.Lock(actionID, slotKey(objectID, property))
}

// ReleaseAll 释放某动作持有的全部槽位锁。骨架占位。
func (g *Graph) ReleaseAll(actionID string) {
	g.locks.UnlockAll(actionID)
}

// CommitSet 写入属性值并返回可用于补偿的旧值记录。调用方必须已 Acquire。骨架占位。
func (g *Graph) CommitSet(actionID, objectID, property, value string) UndoRecord {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.locks.holds(actionID, slotKey(objectID, property)) {
		panic("CommitSet without holding lock: " + actionID)
	}
	obj := g.data[objectID]
	rec := UndoRecord{ObjectID: objectID, Property: property}
	if obj != nil {
		if old, existed := obj[property]; existed {
			rec.Existed = true
			rec.OldValue = old
		}
	}
	if obj == nil {
		obj = map[string]string{}
		g.data[objectID] = obj
	}
	obj[property] = value
	return rec
}

// Restore 按旧值记录补偿。调用方必须仍持有锁。骨架占位。
func (g *Graph) Restore(actionID string, rec UndoRecord) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.locks.holds(actionID, slotKey(rec.ObjectID, rec.Property)) {
		panic("Restore without holding lock: " + actionID)
	}
	obj := g.data[rec.ObjectID]
	if !rec.Existed {
		if obj != nil {
			delete(obj, rec.Property)
			if len(obj) == 0 {
				delete(g.data, rec.ObjectID)
			}
		}
		return
	}
	if obj == nil {
		obj = map[string]string{}
		g.data[rec.ObjectID] = obj
	}
	obj[rec.Property] = rec.OldValue
}

// Snapshot 返回对象图的深拷贝，用于串行/并发结果对拍。骨架占位。
func (g *Graph) Snapshot() map[string]map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]map[string]string, len(g.data))
	for id, props := range g.data {
		cp := make(map[string]string, len(props))
		for k, v := range props {
			cp[k] = v
		}
		out[id] = cp
	}
	return out
}

// UndoRecord 记录单次写入的旧值，构成确定性逆操作。
type UndoRecord struct {
	ObjectID string
	Property string
	Existed  bool
	OldValue string
}
