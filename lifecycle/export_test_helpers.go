package lifecycle

// 仅供对拍测试使用的只读观察入口（非测试代码请使用 SnapshotInstance /
// Neighbors）。

func (e *Engine) GetStateForTest(id InstanceID) (State, bool) {
	inst, ok := e.store.SnapshotInstance(id)
	if !ok {
		return "", false
	}
	return inst.State, true
}

func (e *Engine) GetAttrForTest(id InstanceID, key AttrKey) AttrValue {
	inst, ok := e.store.SnapshotInstance(id)
	if !ok {
		return nil
	}
	return inst.Attrs[key]
}

func (e *Engine) LinkedForTest(src InstanceID, link LinkType, dst InstanceID) bool {
	for _, nb := range e.store.Neighbors(src, link) {
		if nb == dst {
			return true
		}
	}
	return false
}
