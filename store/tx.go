package store

import (
	"ontology/snapshot"
	"ontology/txid"
)

// writeIntent 是事务对单个键的写入意图（未提交）。
type writeIntent struct {
	value   []byte
	deleted bool
}

// txRecord 是事务的意图记录（相当于 WAL intent），
// 崩溃恢复时据此判断该事务应回滚还是已完成。
type txRecord struct {
	id        txid.ID
	snap      *snapshot.Snapshot
	writes    map[string]writeIntent
	appended  []string // 已写入版本体的键（按序）
	indexed   []string // 已记入索引的键（按序）
	committed bool     // 提交标记：崩溃恢复的全有/全无分界
}

// Tx 是一个读写事务。写先落在私有写集，提交时才进入版本链。
type Tx struct {
	store  *Store
	id     txid.ID
	snap   *snapshot.Snapshot
	rec    *txRecord
	closed bool
}

// BeginTx 开启一个读写事务。事务号分配与活跃登记原子完成。
func (s *Store) BeginTx() (*Tx, error) {
	id, err := s.snaps.BeginWriter()
	if err != nil {
		return nil, err
	}
	snap, err := s.snaps.Begin()
	if err != nil {
		s.snaps.EndWriter(id)
		return nil, err
	}
	tx := &Tx{
		store: s,
		id:    id,
		snap:  snap,
		rec:   &txRecord{id: id, snap: snap, writes: map[string]writeIntent{}},
	}
	s.mu.Lock()
	s.txs[id] = tx.rec
	s.mu.Unlock()
	return tx, nil
}

// ID 返回事务号。
func (tx *Tx) ID() txid.ID { return tx.id }

// Write 缓冲一次写（未提交，对他人不可见）。
func (tx *Tx) Write(key string, value []byte) error {
	if tx.closed {
		return ErrTxClosed
	}
	tx.rec.writes[key] = writeIntent{value: value}
	return nil
}

// Delete 缓冲一次删除（提交时写入删除标记版本）。
func (tx *Tx) Delete(key string) error {
	if tx.closed {
		return ErrTxClosed
	}
	tx.rec.writes[key] = writeIntent{deleted: true}
	return nil
}

// Read 读己之写优先，否则按事务快照读。
func (tx *Tx) Read(key string) ([]byte, Lookup) {
	if tx.closed {
		return nil, LookupNever
	}
	if w, ok := tx.rec.writes[key]; ok {
		if w.deleted {
			return nil, LookupDeleted
		}
		return w.value, LookupFound
	}
	return tx.store.ReadAt(tx.snap, key)
}

// Rollback 回滚事务。正常路径写从未进入版本链；
// 若提交曾在崩溃点中断，部分状态一并撤销，不留任何残留。
func (tx *Tx) Rollback() error {
	if tx.closed {
		return ErrTxClosed
	}
	tx.closed = true
	s := tx.store
	s.mu.Lock()
	s.undoRecordLocked(tx.rec)
	delete(s.txs, tx.id)
	s.mu.Unlock()
	s.snaps.EndWriter(tx.id)
	s.snaps.Release(tx.snap)
	return nil
}

// Commit 提交事务，见 commit.go。
func (tx *Tx) Commit() error {
	return tx.store.commit(tx)
}
