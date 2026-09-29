package snapread

import (
	"errors"
	"fmt"
	"sync"
)

// ErrEmptyKey 在写入或读取的键为空串时返回。
var ErrEmptyKey = errors.New("snapread: key must not be empty")

// ErrEmptyValue 在写入空值（nil 或长度为 0 的字节切片）时返回。
var ErrEmptyValue = errors.New("snapread: value must not be empty")

// ErrSeqBeforeSnapshot 表示请求读取的位点早于最近一次快照点，
// 该区间的历史已被快照截断，无法在本实例上回答。
var ErrSeqBeforeSnapshot = errors.New("snapread: read sequence is before the latest snapshot point")

// ErrSeqInFuture 表示请求读取的位点大于当前已经写入的最大序号。
var ErrSeqInFuture = errors.New("snapread: read sequence is in the future")

// Record 是日志中的一条不可变写入记录。
type Record struct {
	Seq   int
	Key   string
	Value []byte
}

// Result 表示一次位点读的结果。
type Result struct {
	Seq      int
	Key      string
	Value    []byte
	Exists   bool
	Snapshot int
	Basis    string
}

// snapshot 是某个位点上逐键最新值的不可变副本。
type snapshot struct {
	seq    int
	values map[string][]byte
}

// Store 是支持快照冻结与增量合并读取的键值存储。
//
// 日志（log）自序号 1 起连续保存全部写入记录，快照只固化值、从不清空日志。
// 最近一次快照点之前的位点读会被整体拒绝；快照点及之后的位点读，
// 以快照副本为基、再按序应用 (snapSeq, seq] 区间内的增量记录。
type Store struct {
	mu     sync.RWMutex
	log    []Record
	seq    int
	latest map[string][]byte
	snap   *snapshot
}

// New 创建一个空存储。
func New() *Store {
	return &Store{latest: make(map[string][]byte)}
}

// Seq 返回当前已写入的最大序号（空存储为 0）。
func (s *Store) Seq() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq
}

// SnapshotSeq 返回最近一次快照点（无快照时为 0）。
func (s *Store) SnapshotSeq() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotSeqLocked()
}

// LogLen 返回日志中保留的记录数（快照不清空日志）。
func (s *Store) LogLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.log)
}

// Write 追加一条写入记录，返回其单调递增序号。
// 空键或空值会在任何状态变更之前被拒绝。
func (s *Store) Write(key string, value []byte) (int, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	if len(value) == 0 {
		return 0, ErrEmptyValue
	}

	stored := make([]byte, len(value))
	copy(stored, value)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	s.log = append(s.log, Record{Seq: s.seq, Key: key, Value: stored})
	s.latest[key] = stored
	return s.seq, nil
}

// Snapshot 冻结当前位点，返回快照点序号。
// 快照把当前逐键最新值深拷贝成不可变副本；日志保留不变，可反复调用。
func (s *Store) Snapshot() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	frozen := make(map[string][]byte, len(s.latest))
	for key, value := range s.latest {
		copied := make([]byte, len(value))
		copy(copied, value)
		frozen[key] = copied
	}
	s.snap = &snapshot{seq: s.seq, values: frozen}
	return s.seq
}

// ReadAt 在指定位点读取键的值。
//
// 位点区间：最近快照点 snapSeq 之前（seq < snapSeq）整体拒绝；
// snapSeq <= seq <= 当前位点时，以快照为基合并增量回答。
func (s *Store) ReadAt(seq int, key string) (*Result, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.snap != nil && seq < s.snap.seq {
		return nil, ErrSeqBeforeSnapshot
	}
	if seq > s.seq {
		return nil, ErrSeqInFuture
	}

	var (
		value  []byte
		exists bool
		basis  string
	)

	if s.snap != nil {
		if base, ok := s.snap.values[key]; ok {
			value = make([]byte, len(base))
			copy(value, base)
			exists = true
		}
		for _, rec := range s.log[s.snap.seq:seq] {
			if rec.Key != key {
				continue
			}
			value = make([]byte, len(rec.Value))
			copy(value, rec.Value)
			exists = true
		}
		basis = fmt.Sprintf("snapshot@%d + incrementals[%d,%d]",
			s.snap.seq, s.snap.seq+1, seq)
	} else {
		for _, rec := range s.log[:seq] {
			if rec.Key != key {
				continue
			}
			value = make([]byte, len(rec.Value))
			copy(value, rec.Value)
			exists = true
		}
		basis = fmt.Sprintf("incrementals[1,%d] (no snapshot)", seq)
	}

	return &Result{
		Seq:      seq,
		Key:      key,
		Value:    value,
		Exists:   exists,
		Snapshot: s.snapshotSeqLocked(),
		Basis:    basis,
	}, nil
}

// ReplayFromZero 从序号 1 开始按序重放全部日志，返回该键在位点 seq 的值。
// 它不受快照截断限制，是核对快照+增量读取结果的本地参照实现。
func (s *Store) ReplayFromZero(seq int, key string) (*Result, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if seq < 0 || seq > s.seq {
		return nil, ErrSeqInFuture
	}

	var (
		value  []byte
		exists bool
	)
	for _, rec := range s.log[:seq] {
		if rec.Key != key {
			continue
		}
		value = make([]byte, len(rec.Value))
		copy(value, rec.Value)
		exists = true
	}

	return &Result{
		Seq:      seq,
		Key:      key,
		Value:    value,
		Exists:   exists,
		Snapshot: s.snapshotSeqLocked(),
		Basis:    fmt.Sprintf("full-replay[1,%d]", seq),
	}, nil
}

func (s *Store) snapshotSeqLocked() int {
	if s.snap == nil {
		return 0
	}
	return s.snap.seq
}
