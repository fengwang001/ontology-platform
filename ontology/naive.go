package ontology

// Naive 是一个独立的朴素串行参考实现：没有锁、没有日志，
// 只按调用顺序逐个执行提交与写入。它用于在并发测试中与
// Engine 对照：把 Engine 全序日志中的操作按序重放到 Naive 上，
// 两者最终接受的字段定义与写入结果必须一致。
type Naive struct {
	store *InstanceStore
	types map[string]*ObjectType
	refs  *RefRegistry
}

func NewNaive() *Naive {
	return &Naive{
		store: NewInstanceStore(),
		types: make(map[string]*ObjectType),
		refs:  NewRefRegistry(),
	}
}

// RegisterObjectType 注册对象类型。
func (n *Naive) RegisterObjectType(ot *ObjectType) { n.types[ot.Name] = ot }

// RegisterRef 登记引用方并捕获当前字段语义快照。
func (n *Naive) RegisterRef(ref FieldReference) {
	if ot, ok := n.types[ref.ObjectType]; ok {
		ref.Captured = SignatureOf(ot.field(ref.Field))
	}
	n.refs.Register(ref)
}

// Commit 朴素地逐项判定、全部通过才整体生效。
func (n *Naive) Commit(batch []FieldChange) BatchResult {
	checker := &Checker{Store: n.store, Refs: n.refs}
	res := BatchResult{Accepted: true}
	for _, ch := range batch {
		// 与 Engine 相同的 CAS 前提：Old 必须与当前定义一致。
		ot := n.types[ch.ObjectType]
		var cur *FieldDef
		if ot != nil {
			cur = ot.field(ch.Field)
		}
		if !signatureEqual(SignatureOf(cur), SignatureOf(ch.Old)) {
			res.Accepted = false
			res.Decisions = append(res.Decisions, Decision{Change: ch, Compatible: false, Reason: "stale base"})
			continue
		}
		d, err := checker.Check(ch)
		if err != nil || !d.Compatible {
			res.Accepted = false
			res.Categories |= d.Categories
		}
		res.Decisions = append(res.Decisions, d)
	}
	if !res.Accepted {
		return res
	}
	for _, ch := range batch {
		n.apply(ch)
	}
	return res
}

func (n *Naive) apply(ch FieldChange) {
	ot := n.types[ch.ObjectType]
	if ch.New == nil {
		delete(ot.Fields, ch.Field)
		n.store.EachLive(ch.ObjectType, func(in *Instance) bool {
			delete(in.Values, ch.Field)
			return true
		})
	} else {
		def := *ch.New
		ot.Fields[ch.Field] = &def
		if ch.Old == nil && ch.Backfill != nil {
			n.store.EachLive(ch.ObjectType, func(in *Instance) bool {
				if v, ok := ch.Backfill(in.ID); ok {
					in.Values[ch.Field] = v
				}
				return true
			})
		}
		if ch.Old != nil && ch.Old.Type.Kind() != ch.New.Type.Kind() {
			n.store.EachLive(ch.ObjectType, func(in *Instance) bool {
				if v, ok := in.Values[ch.Field]; ok {
					if nv, ok := ch.Old.Type.ReinterpretTo(ch.New.Type, v); ok {
						in.Values[ch.Field] = nv
					}
				}
				return true
			})
		}
	}
	newSig := SignatureOf(ch.New)
	for _, ref := range n.refs.OnField(ch.ObjectType, ch.Field) {
		ref.Captured = newSig
		n.refs.Register(ref)
	}
}

// Write 朴素地按当前字段定义校验后写入。
func (n *Naive) Write(in Instance) error {
	e := &Engine{types: n.types}
	if err := e.validateWrite(in); err != nil {
		return err
	}
	n.store.Put(in)
	return nil
}

// ObjectTypeDef 返回字段定义快照。
func (n *Naive) ObjectTypeDef(name string) map[string]FieldDef {
	return snapshotFields(n.types[name])
}

// LiveInstances 返回全部存活实例的拷贝。
func (n *Naive) LiveInstances(objectType string) []Instance {
	var out []Instance
	n.store.EachLive(objectType, func(in *Instance) bool {
		out = append(out, in.Clone())
		return true
	})
	return out
}
