package store

import (
	"sort"

	"ontology/reclaim"
	"ontology/txid"
	"ontology/version"
)

// CrashStage 是提交流水线中的崩溃阶段。
type CrashStage int

const (
	StageBeforeAppend CrashStage = iota // 追加版本体之前
	StageAfterAppend                    // 版本体已追加、未记入索引
	StageAfterIndex                     // 已记入索引、未打提交标记
	StageBeforeMark                     // 全部键写完、提交标记之前
	StageAfterMark                      // 提交标记之后（已完全可见）
)

// CrashPoint 唯一标识一次提交过程中的一个中途点。
type CrashPoint struct {
	Stage CrashStage
	Key   string
	Seq   int
}

// SetCrashHook 注入崩溃钩子。钩子返回 true 表示在该点崩溃：
// 提交立即以 ErrCrashed 失败，留下部分状态，等待 Recover 清理。
func (s *Store) SetCrashHook(hook func(CrashPoint) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crashHook = hook
}

// crashed 在持有锁时调用钩子。
func (s *Store) crashed(p CrashPoint) bool {
	return s.crashHook != nil && s.crashHook(p)
}

// commit 执行提交流水线：预检查 → 逐键「追加版本 → 记入索引」→ 提交标记。
// 所有上限检查先于任何变更，拒绝不改变任何已有状态。
func (s *Store) commit(tx *Tx) error {
	if tx.closed {
		return ErrTxClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := tx.rec

	if s.cfg.MaxChainLen > 0 {
		for key := range rec.writes {
			if s.chainLen(key)+1 > s.cfg.MaxChainLen {
				return ErrChainLenExceeded
			}
		}
	}
	if s.cfg.MaxVersions > 0 && s.total+len(rec.writes) > s.cfg.MaxVersions {
		return ErrVersionLimit
	}

	keys := make([]string, 0, len(rec.writes))
	for key := range rec.writes {
		keys = append(keys, key)
	}
	sort.Strings(keys) // 固定顺序，崩溃点可枚举、可复现

	for i, key := range keys {
		if s.crashed(CrashPoint{Stage: StageBeforeAppend, Key: key, Seq: i}) {
			return ErrCrashed
		}
		w := rec.writes[key]
		v := &version.Version{Commit: tx.id, Value: w.value, Deleted: w.deleted}
		chain, ok := s.chains[key]
		if !ok {
			chain = version.NewChain()
			s.chains[key] = chain
		}
		if prev, ok := chain.Newest(); ok {
			s.rec.Add(reclaim.Candidate{Key: key, Commit: prev.Commit, Shadow: tx.id})
		}
		chain.Append(v)
		rec.appended = append(rec.appended, key)
		s.total++
		if s.crashed(CrashPoint{Stage: StageAfterAppend, Key: key, Seq: i}) {
			return ErrCrashed
		}
		s.insertIndexEntryLocked(key, v)
		rec.indexed = append(rec.indexed, key)
		if s.crashed(CrashPoint{Stage: StageAfterIndex, Key: key, Seq: i}) {
			return ErrCrashed
		}
	}

	if s.crashed(CrashPoint{Stage: StageBeforeMark, Seq: len(keys)}) {
		return ErrCrashed
	}
	rec.committed = true
	delete(s.txs, tx.id)
	s.crashed(CrashPoint{Stage: StageAfterMark, Seq: len(keys)}) // 已提交，崩溃无影响
	tx.closed = true
	s.snaps.EndWriter(tx.id)
	s.snaps.Release(tx.snap)
	return nil
}

// undoRecordLocked 撤销一个未提交事务的全部部分状态（调用方须持有锁）。
func (s *Store) undoRecordLocked(rec *txRecord) {
	for _, key := range rec.indexed {
		s.removeIndexEntryLocked(key, rec.id)
	}
	for _, key := range rec.appended {
		if chain, ok := s.chains[key]; ok {
			chain.RemoveUncommitted(rec.id)
			if chain.Len() == 0 {
				delete(s.chains, key)
			}
		}
		s.total--
	}
	rec.appended = nil
	rec.indexed = nil
}

// removeIndexEntryLocked 从索引中移除指定提交事务号的槽位。
func (s *Store) removeIndexEntryLocked(key string, commit txid.ID) {
	entries := s.index[key]
	for i, v := range entries {
		if v.Commit == commit {
			entries = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	if len(entries) == 0 {
		delete(s.index, key)
		return
	}
	s.index[key] = entries
}

// insertIndexEntryLocked 按提交事务号有序插入索引槽位，与版本链保持一致。
func (s *Store) insertIndexEntryLocked(key string, v *version.Version) {
	entries := s.index[key]
	i := len(entries)
	for i > 0 && entries[i-1].Commit.After(v.Commit) {
		i--
	}
	entries = append(entries, nil)
	copy(entries[i+1:], entries[i:])
	entries[i] = v
	s.index[key] = entries
}

// Recover 模拟崩溃后重启：清理所有未打提交标记的在途事务，
// 使存储回到「该事务完全不可见」的状态。已提交事务不受影响。
func (s *Store) Recover() {
	s.mu.Lock()
	recs := make([]*txRecord, 0, len(s.txs))
	for _, rec := range s.txs {
		recs = append(recs, rec)
	}
	for _, rec := range recs {
		s.undoRecordLocked(rec)
		delete(s.txs, rec.id)
	}
	s.mu.Unlock()
	for _, rec := range recs {
		s.snaps.EndWriter(rec.id)
		if rec.snap != nil {
			s.snaps.Release(rec.snap)
		}
	}
}
