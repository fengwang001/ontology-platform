package aggview

import "sync"

// lock 是处理单元的全局互斥锁。
type lock = sync.Mutex

// undo 记录一步可逆操作；处理单元失败时按逆序执行全部 undo。
type undo func()

// txn 在 Store 之上附加 undo 日志。Store 的每次修改都注册逆操作，
// 同时聚合桶与归属版本的修改也注册逆操作，从而保证整体回滚到处理单元前状态。
type txn struct {
	store  *Store
	undoes []undo
}

func newTxn(st *Store) *txn { return &txn{store: st} }

func (t *txn) rollback() {
	for i := len(t.undoes) - 1; i >= 0; i-- {
		t.undoes[i]()
	}
	t.undoes = nil
}

func (t *txn) push(fn undo) { t.undoes = append(t.undoes, fn) }

// mutator 是处理单元内对 Store 与聚合状态的唯一写入入口。
type mutator struct {
	txn      *txn
	eng      *Engine
	chg      *Change
	reason   string
	affected []GroupContribution
	touched  []Touched
}

func (t *txn) getProp(id, prop ID) Value {
	return t.store.GetProperty(id, prop)
}

func (t *txn) linksFrom(id, linkType ID) []ID {
	return t.store.LinksFrom(id, linkType)
}

func (t *txn) linksTo(linkType, to ID) []ID {
	return t.store.LinksTo(linkType, to)
}

func (m *mutator) setProp(id, prop ID, v Value) (Value, error) {
	old := m.txn.store.GetProperty(id, prop)
	newV := cloneValue(v)
	if _, err := m.txn.store.SetProperty(id, prop, newV); err != nil {
		return Value{}, err
	}
	oldCopy := cloneValue(old)
	m.txn.push(func() {
		if oldCopy.Present {
			m.txn.store.SetProperty(id, prop, oldCopy)
		} else {
			m.txn.store.SetProperty(id, prop, AbsentValue())
		}
	})
	return old, nil
}

func (m *mutator) addLink(from, linkType, to ID) (bool, error) {
	added, err := m.txn.store.AddLink(from, linkType, to)
	if err != nil || !added {
		return added, err
	}
	m.txn.push(func() { m.txn.store.RemoveLink(from, linkType, to) })
	return true, nil
}

func (m *mutator) removeLink(from, linkType, to ID) (bool, error) {
	removed, err := m.txn.store.RemoveLink(from, linkType, to)
	if err != nil || !removed {
		return removed, err
	}
	m.txn.push(func() { m.txn.store.AddLink(from, linkType, to) })
	return true, nil
}

func (m *mutator) deleteObject(id ID) (bool, error) {
	// DeleteObject 物理删除并清理双向索引；为支持处理单元回滚，
	// 这里快照整个实例及其相关反向索引项。
	st := m.txn.store
	o := st.objs[id]
	if o == nil {
		return false, nil
	}
	type revKey struct{ link, target ID }
	snapLinks := map[ID]map[ID]bool{}
	for lt, ts := range o.links {
		snapLinks[lt] = map[ID]bool{}
		for t := range ts {
			snapLinks[lt][t] = true
		}
	}
	snapProps := map[ID]Value{}
	for p, val := range o.props {
		snapProps[p] = cloneValue(val)
	}
	deleted, err := st.DeleteObject(id)
	if err != nil || !deleted {
		return deleted, err
	}
	m.txn.push(func() {
		restored := &objSnapshot{
			alive: true,
			typ:   o.typ,
			links: snapLinks,
			props: snapProps,
		}
		st.objs[id] = restored
		for lt, ts := range snapLinks {
			byTarget, ok := st.rev[lt]
			if !ok {
				byTarget = map[ID]map[ID]bool{}
				st.rev[lt] = byTarget
			}
			for t := range ts {
				sources, ok := byTarget[t]
				if !ok {
					sources = map[ID]bool{}
					byTarget[t] = sources
				}
				sources[id] = true
			}
		}
	})
	return true, nil
}
