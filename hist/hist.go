// Package hist 管理多 Key 的版本历史：容量上限与摄取时间单调性校验。
// 依赖 ver。所有校验先于任何写入，被拒绝的操作不改变任何状态。
package hist

import (
	"errors"
	"sync"

	"ontology/ver"
)

var (
	// ErrEmptyKey 表示 key 为空串。
	ErrEmptyKey = errors.New("hist: empty key")
	// ErrNonMonotonicIngest 表示 in 未严格大于该 key 已有最大 In。
	ErrNonMonotonicIngest = errors.New("hist: non-monotonic ingest time")
	// ErrCapacity 表示该 key 的版本数已达 maxVersions 上限。
	ErrCapacity = errors.New("hist: version capacity exceeded")
)

// Store 是多 Key 版本存储，max 为每 key 版本数上限（>= 1，由 api 保证）。
type Store struct {
	mu   sync.RWMutex
	max  int
	keys map[string]*ver.History
}

// New 创建一个每 key 最多 max 个版本的 Store。
func New(max int) *Store {
	return &Store{max: max, keys: make(map[string]*ver.History)}
}

// Apply 追加一个版本。空 key、In 不递增、超容量分别返回对应哨兵错误；
// 所有校验在持写锁下先于写入完成，失败不留痕。
func (s *Store) Apply(key, value string, ev, in int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.keys[key]
	if h != nil {
		if in <= h.MaxIn() {
			return ErrNonMonotonicIngest
		}
		if h.Len() >= s.max {
			return ErrCapacity
		}
	} else {
		h = &ver.History{}
		s.keys[key] = h
	}
	h.Append(ver.Version{Value: value, Ev: ev, In: in})
	return nil
}

// query 在读锁下取 key 的历史并执行 q；key 不存在时 h 为 nil，ver 方法返回 ErrNotFound。
func (s *Store) query(key string, q func(*ver.History) (ver.Version, error)) (ver.Version, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return q(s.keys[key])
}

// LatestEvent 见 ver.History.LatestEvent。它会更新 checked 计数器（写操作），故用写锁。
func (s *Store) LatestEvent(key string) (ver.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys[key].LatestEvent()
}

// LatestIngest 见 ver.History.LatestIngest。
func (s *Store) LatestIngest(key string) (ver.Version, error) {
	return s.query(key, func(h *ver.History) (ver.Version, error) { return h.LatestIngest() })
}

// AtEvent 见 ver.History.AtEvent。
func (s *Store) AtEvent(key string, t int64) (ver.Version, error) {
	return s.query(key, func(h *ver.History) (ver.Version, error) { return h.AtEvent(t) })
}

// AtIngest 见 ver.History.AtIngest。
func (s *Store) AtIngest(key string, t int64) (ver.Version, error) {
	return s.query(key, func(h *ver.History) (ver.Version, error) { return h.AtIngest(t) })
}
