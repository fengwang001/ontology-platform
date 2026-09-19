package ontology

import "fmt"

// CreateObject 在事务内创建对象。只读钩子调用时写入被吞掉并记越权。
func (t *Txn) CreateObject(id, objType string, attrs map[string]any) error {
	if t.root.readOnly {
		t.noteViolation(t.currentHook())
		return nil
	}
	if err := t.store.checkWritable(); err != nil {
		return err
	}
	if !t.touches(objType) {
		return fmt.Errorf("action %q 未声明触碰对象类型 %q", t.action, objType)
	}
	if _, exists := t.store.objects[id]; exists {
		return fmt.Errorf("对象 %q 已存在", id)
	}
	obj := &Object{ID: id, Type: objType, Attributes: copyAttrs(attrs)}
	t.store.objects[id] = obj
	t.log(undoEntry{
		desc: fmt.Sprintf("create object %s(%s)", id, objType),
		undo: func() error {
			if _, ok := t.store.objects[id]; !ok {
				return fmt.Errorf("对象 %q 已不在存储中", id)
			}
			delete(t.store.objects, id)
			return nil
		},
	})
	t.addImpact(ImpactItem{Kind: ImpactObjectCreated, ObjectID: id, ObjType: objType})
	return nil
}

// SetAttribute 修改对象属性（记录旧值，支持逆序撤销）。
func (t *Txn) SetAttribute(id, attr string, value any) error {
	if t.root.readOnly {
		t.noteViolation(t.currentHook())
		return nil
	}
	if err := t.store.checkWritable(); err != nil {
		return err
	}
	obj, ok := t.store.objects[id]
	if !ok {
		return fmt.Errorf("对象 %q 不存在", id)
	}
	if !t.touches(obj.Type) {
		return fmt.Errorf("action %q 未声明触碰对象类型 %q", t.action, obj.Type)
	}
	old, had := obj.Attributes[attr]
	obj.Attributes[attr] = value
	t.log(undoEntry{
		desc: fmt.Sprintf("set %s.%s", id, attr),
		undo: func() error {
			cur, ok := t.store.objects[id]
			if !ok {
				return fmt.Errorf("对象 %q 已不在存储中", id)
			}
			if had {
				cur.Attributes[attr] = old
			} else {
				delete(cur.Attributes, attr)
			}
			return nil
		},
	})
	t.addImpact(ImpactItem{Kind: ImpactObjectChanged, ObjectID: id, Attr: attr})
	return nil
}

// AddRelation 建立关系，要求两端对象都存在。
func (t *Txn) AddRelation(rel Relation) error {
	if t.root.readOnly {
		t.noteViolation(t.currentHook())
		return nil
	}
	if err := t.store.checkWritable(); err != nil {
		return err
	}
	if _, ok := t.store.objects[rel.FromID]; !ok {
		return fmt.Errorf("关系起点对象 %q 不存在", rel.FromID)
	}
	if _, ok := t.store.objects[rel.ToID]; !ok {
		return fmt.Errorf("关系终点对象 %q 不存在", rel.ToID)
	}
	if _, exists := t.store.relations[rel]; exists {
		return fmt.Errorf("关系 %s: %s -> %s 已存在", rel.Type, rel.FromID, rel.ToID)
	}
	t.store.relations[rel] = struct{}{}
	t.log(undoEntry{
		desc: fmt.Sprintf("add relation %s %s->%s", rel.Type, rel.FromID, rel.ToID),
		undo: func() error { delete(t.store.relations, rel); return nil },
	})
	t.addImpact(ImpactItem{Kind: ImpactRelationAdded, RelType: rel})
	return nil
}

// RemoveRelation 删除关系。
func (t *Txn) RemoveRelation(rel Relation) error {
	if t.root.readOnly {
		t.noteViolation(t.currentHook())
		return nil
	}
	if err := t.store.checkWritable(); err != nil {
		return err
	}
	if _, ok := t.store.relations[rel]; !ok {
		return fmt.Errorf("关系 %s: %s -> %s 不存在", rel.Type, rel.FromID, rel.ToID)
	}
	delete(t.store.relations, rel)
	t.log(undoEntry{
		desc: fmt.Sprintf("remove relation %s %s->%s", rel.Type, rel.FromID, rel.ToID),
		undo: func() error {
			t.store.relations[rel] = struct{}{}
			return nil
		},
	})
	t.addImpact(ImpactItem{Kind: ImpactRelationRemoved, RelType: rel})
	return nil
}

// GetObject 从事务视角读对象（钩子可读未提交修改）。
func (t *Txn) GetObject(id string) (*Object, bool) {
	o, ok := t.store.objects[id]
	if !ok {
		return nil, false
	}
	cp := *o
	cp.Attributes = copyAttrs(o.Attributes)
	return &cp, true
}

// HasRelation 从事务视角判断关系是否存在。
func (t *Txn) HasRelation(rel Relation) bool {
	_, ok := t.store.relations[rel]
	return ok
}

func (t *Txn) touches(objType string) bool {
	if len(t.allowedTypes) == 0 {
		return true
	}
	_, ok := t.allowedTypes[objType]
	return ok
}

func copyAttrs(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
