// Package api 对外提供快照隔离键值存储。依赖 snap。
package api

import (
	"errors"
	"sync"

	"ontology/snap"
	"ontology/ver"
)

// 可判定哨兵错误，四者互不相同。
var (
	ErrNonPositiveMax  = errors.New("api: maxSnapshots 必须为正")
	ErrEmptyKey        = errors.New("api: Key 不能为空串")
	ErrInvalidSnapshot = errors.New("api: 快照 id 非法或不存在")
	ErrSnapshotLimit   = errors.New("api: 存活快照数超过 maxSnapshots")
)

// Store 是并发安全的快照隔离存储。
type Store struct {
	mu  sync.Mutex
	s   *snap.Store
	max int
}

// New 创建一个存储；maxSnapshots 非正时整体失败。
func New(maxSnapshots int) (*Store, error) {
	if maxSnapshots <= 0 {
		return nil, ErrNonPositiveMax
	}
	return &Store{s: snap.New(), max: maxSnapshots}, nil
}

// Write 追加一个版本。Key 为空时拒绝且不留痕。
func (st *Store) Write(key string, v int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.s.Write(key, v)
	return nil
}

// Snapshot 登记并返回一个快照；存活数超限则拒绝且不留痕。
func (st *Store) Snapshot() (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.s.LiveCount() >= st.max {
		return 0, ErrSnapshotLimit
	}
	return st.s.Snapshot(), nil
}

// Read 按快照版本取值；快照 id 非法或 Key 为空时拒绝。
func (st *Store) Read(snapID int, key string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !ver.Valid(snapID, st.s.Cur()) {
		return 0, ErrInvalidSnapshot
	}
	return st.s.Read(snapID, key), nil
}

// ReadCurrent 返回最新值（无则 0）。
func (st *Store) ReadCurrent(key string) int64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.ReadCurrent(key)
}

// Release 释放一个存活快照；id 非法或未登记时拒绝且不留痕。
func (st *Store) Release(snapID int) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !ver.Valid(snapID, st.s.Cur()) || !st.s.Live(snapID) {
		return ErrInvalidSnapshot
	}
	st.s.Release(snapID)
	return nil
}

// SelfCheck 在独立的内部实例上核验四条不变量，不改本实例状态。
func (st *Store) SelfCheck() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return selfCheck()
}

func selfCheck() error {
	st, err := New(4)
	if err != nil {
		return err
	}
	// 不变量 1+2：八步序列，快照读须等于朴素重放结果。
	st.Write("a", 10)
	st.Write("b", 20)
	s1, _ := st.Snapshot()
	st.Write("a", 15)
	if got, _ := st.Read(s1, "a"); got != 10 {
		return errors.New("selfcheck: Read(s1,a) != 10")
	}
	st.Write("b", 25)
	if got, _ := st.Read(s1, "b"); got != 20 {
		return errors.New("selfcheck: Read(s1,b) != 20")
	}
	// 不变量 3：读与快照不改 ver。
	before := st.s.Cur()
	st.Snapshot()
	st.Read(s1, "a")
	st.ReadCurrent("a")
	if st.s.Cur() != before {
		return errors.New("selfcheck: 读/快照改变了 ver")
	}
	// 不变量 4：被拒操作不留痕。
	cur, live := st.s.Cur(), st.s.LiveCount()
	if _, err := st.Read(-1, "a"); !errors.Is(err, ErrInvalidSnapshot) {
		return errors.New("selfcheck: 负快照未拒绝")
	}
	if _, err := st.Read(cur+1, "a"); !errors.Is(err, ErrInvalidSnapshot) {
		return errors.New("selfcheck: 超界快照未拒绝")
	}
	if err := st.Write("", 1); !errors.Is(err, ErrEmptyKey) {
		return errors.New("selfcheck: 空 Key 未拒绝")
	}
	if err := st.Release(999); !errors.Is(err, ErrInvalidSnapshot) {
		return errors.New("selfcheck: 释放不存在快照未拒绝")
	}
	if _, err := New(0); !errors.Is(err, ErrNonPositiveMax) {
		return errors.New("selfcheck: 非正 maxSnapshots 未拒绝")
	}
	if st.s.Cur() != cur || st.s.LiveCount() != live {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	// 快照数超限。
	full, _ := New(1)
	if _, err := full.Snapshot(); err != nil {
		return err
	}
	if _, err := full.Snapshot(); !errors.Is(err, ErrSnapshotLimit) {
		return errors.New("selfcheck: 超限未拒绝")
	}
	return nil
}
