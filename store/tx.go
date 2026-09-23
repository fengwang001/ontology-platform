package store

import (
	"sort"

	"ontology/reclaim"
	"ontology/snapshot"
	"ontology/txid"
	"ontology/version"
)

// Tx 是写事务。写先缓存在事务内（读己之写），提交时原子落链。
type Tx struct {
	st     *Store
	id     txid.T
	snap   snapshot.Snapshot
	writes map[string]version.Version
	done   bool
}

// Begin 开启写事务，分配事务号并以开启时刻为读快照。
func (s *Store) Begin() *Tx {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, _ := s.src.Next()
	t := &Tx{st: s, id: id, writes: make(map[string]version.Version)}
	s.txs[id] = t
	t.snap = s.snapshotLocked()
	return t
}

// Put 缓冲一次写入。
func (t *Tx) Put(key string, val []byte) error {
	t.st.mu.Lock()
	defer t.st.mu.Unlock()
	if t.done {
		return ErrTxClosed
	}
	t.writes[key] = version.Version{Value: append([]byte(nil), val...)}
	return nil
}

// Delete 缓冲一次删除（删除是一个版本：删除标记）。
func (t *Tx) Delete(key string) error {
	t.st.mu.Lock()
	defer t.st.mu.Unlock()
	if t.done {
		return ErrTxClosed
	}
	t.writes[key] = version.Version{Del: true}
	return nil
}

// Get 读己之写优先，否则按事务开启时的快照读。
func (t *Tx) Get(key string) ([]byte, error) {
	t.st.mu.Lock()
	defer t.st.mu.Unlock()
	if w, ok := t.writes[key]; ok {
		if w.Del {
			return nil, ErrNotFound
		}
		return append([]byte(nil), w.Value...), nil
	}
	return t.st.getLocked(t.snap, key)
}

// Commit 原子提交。崩溃钩子点：0=写索引前，1=索引后/追加前，
// 2=每次追加前（多键写一半的中点），3=提交记录前，4=提交记录后。
// 可见性唯一判定点是点 3 之后的 committed 标记。
func (t *Tx) Commit() error {
	s := t.st
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.done {
		return ErrTxClosed
	}
	if s.cfg.MaxTotalVersions > 0 && s.total+len(t.writes) > s.cfg.MaxTotalVersions {
		return ErrTooManyVersions
	}
	for key := range t.writes {
		if s.cfg.MaxVersionsPerKey <= 0 {
			break
		}
		n := 0
		if ch := s.chains[key]; ch != nil {
			n = ch.Len()
		}
		if n+1 > s.cfg.MaxVersionsPerKey {
			return ErrChainTooLong
		}
	}
	keys := make([]string, 0, len(t.writes))
	for key := range t.writes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rec := &commitRecord{keys: keys}
	s.callHook(0)
	s.pend[t.id] = rec
	s.callHook(1)
	for _, key := range keys {
		s.callHook(2)
		ch := s.chains[key]
		if ch == nil {
			ch = &version.Chain{}
			s.chains[key] = ch
		}
		if top, ok := ch.Top(); ok {
			s.rec.Add(reclaim.Candidate{Key: key, Commit: top, Superseder: t.id})
		}
		v := t.writes[key]
		v.Tx = t.id
		ch.Append(v)
		s.total++
	}
	s.callHook(3)
	rec.committed = true
	for _, key := range keys {
		s.chains[key].Commit(t.id)
	}
	s.callHook(4)
	delete(s.pend, t.id)
	delete(s.txs, t.id)
	t.done = true
	return nil
}

// Rollback 回滚：写缓冲直接丢弃，链上无任何残留。
func (t *Tx) Rollback() error {
	s := t.st
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.done {
		return ErrTxClosed
	}
	t.done = true
	t.writes = nil
	delete(s.txs, t.id)
	return nil
}

// View 是只读事务的快照句柄。
type View struct {
	st   *Store
	id   uint64
	snap snapshot.Snapshot
	done bool
}

// BeginView 建立读快照。超过 MaxSnapshots 时拒绝且不改任何状态。
func (s *Store) BeginView() (*View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.MaxSnapshots > 0 && s.reg.Len() >= s.cfg.MaxSnapshots {
		return nil, ErrTooManySnapshots
	}
	v := &View{st: s, snap: s.snapshotLocked()}
	v.id = s.reg.Open(v.snap)
	return v, nil
}

// Get 按快照读键。不可见或可见删除标记都返回 ErrNotFound。
func (v *View) Get(key string) ([]byte, error) {
	v.st.mu.Lock()
	defer v.st.mu.Unlock()
	return v.st.getLocked(v.snap, key)
}

// Status 判定键在该快照下的存在性：从未存在 / 已删除 / 存在。
func (v *View) Status(key string) Status {
	v.st.mu.Lock()
	defer v.st.mu.Unlock()
	ch := v.st.chains[key]
	if ch == nil {
		return NeverExisted
	}
	ver, ok := ch.Visible(v.snap.Point, v.snap.IsActive)
	if !ok {
		return NeverExisted
	}
	if ver.Del {
		return Deleted
	}
	return Exists
}

// Close 关闭快照（幂等）。
func (v *View) Close() {
	v.st.mu.Lock()
	defer v.st.mu.Unlock()
	if !v.done {
		v.done = true
		v.st.reg.Close(v.id)
	}
}
