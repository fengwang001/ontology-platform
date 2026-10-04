// Package credit 保存爽约记录并按滑动窗口判定禁约。
package credit

import "sync"

// Store 是按患者组织的爽约记录存储。
type Store struct {
	mu    sync.RWMutex
	rec   map[string][]int64
	touch int
}

// New 创建空记录存储。
func New() *Store { return &Store{rec: map[string][]int64{}} }

// Add 为 patient 追加一条时刻为 t 的爽约记录。
func (s *Store) Add(patient string, t int64) {
	key := string(patient)
	s.mu.Lock()
	ts := s.rec[key]
	i := len(ts)
	for i > 0 && ts[i-1] > t {
		i--
	}
	ts = append(ts, 0)
	copy(ts[i+1:], ts[i:])
	ts[i] = t
	s.rec[key] = ts
	s.mu.Unlock()
}

// Banned 判定患者在 now 是否禁约；纯读，不修改任何状态。
// 禁约条件：时刻 t 满足 now-t < w 的记录不少于 k 条。
// touched 记录本次判定实际读取的记录数：从最新向旧扫描，读满 k 条即停。
func (s *Store) Banned(now int64, patient string, k, w int64) bool {
	key := string(patient)
	s.mu.RLock()
	ts := s.rec[key]
	s.touch = 0
	n := 0
	for i := len(ts) - 1; i >= 0; i-- {
		s.touch++
		if now-ts[i] >= w {
			break
		}
		n++
		if n >= int(k) {
			break
		}
	}
	s.mu.RUnlock()
	return n >= int(k)
}

// Touched 返回最近一次 Banned 读取的爽约记录条数（≤ k）。
func (s *Store) Touched() int {
	s.mu.RLock()
	n := s.touch
	s.mu.RUnlock()
	return n
}

// Records 返回患者全部爽约时刻的有序副本（只读访问器）。
func (s *Store) Records(patient string) []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]int64(nil), s.rec[patient]...)
}

// Patients 返回有记录的全部患者标识。
func (s *Store) Patients() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.rec))
	for k := range s.rec {
		out = append(out, k)
	}
	return out
}
