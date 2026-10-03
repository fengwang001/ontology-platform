// Package meta 存储带格式级别的对象元数据记录，并按级别维护计数。
package meta

import "sync"

// 各字段最低格式级别。
const (
	LevelSize = 1
	LevelEtag = 2
	LevelTags = 3
)

// Fields 为一次写入显式提供的字段；nil 指针表示缺省（未提供）。
type Fields struct {
	Size int64
	Etag *string
	Tags *[]string
}

// Record 是一条带格式级别的元数据记录。
type Record struct {
	Level int
	Fields
}

// View 是按节点读取能力过滤后的读结果。
type View struct {
	Level    int
	Size     int64
	Etag     *string
	Tags     *[]string
	Degraded bool
}

// Store 是线程安全的记录存储。
type Store struct {
	mu         sync.Mutex
	records    map[string]Record
	levelCount [4]int // 下标 1..3
	scanned    int    // 残留判定不应触碰记录；仅在真正扫描记录时自增（测试探针）
}

// New 创建空存储。
func New() *Store {
	return &Store{records: make(map[string]Record)}
}

// Put 以 level 写入（覆盖旧记录），并维护按级别计数。
func (s *Store) Put(key string, level int, f Fields) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.records[key]; ok {
		s.levelCount[old.Level]--
	}
	s.records[key] = Record{Level: level, Fields: f}
	s.levelCount[level]++
}

// Delete 删除记录并维护计数；返回是否存在。
func (s *Store) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.records[key]
	if !ok {
		return false
	}
	s.levelCount[old.Level]--
	delete(s.records, key)
	return true
}

// Get 取出原始记录。
func (s *Store) Get(key string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[key]
	return r, ok
}

// Read 以 maxLevel 过滤字段并判定降级。
func (s *Store) Read(key string, maxLevel int) (View, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[key]
	if !ok {
		return View{}, false
	}
	v := View{Level: r.Level, Size: r.Size, Degraded: r.Level > maxLevel}
	if r.Etag != nil && LevelEtag <= maxLevel {
		v.Etag = r.Etag
	}
	if r.Tags != nil && LevelTags <= maxLevel {
		v.Tags = r.Tags
	}
	return v, true
}

// CountByLevel 返回级别 1..3 的记录条数副本。
func (s *Store) CountByLevel() [4]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.levelCount
}

// Total 返回记录总数。
func (s *Store) Total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// Residue 返回级别严格大于 target 的残留条数与其中的最高级别；无残留时 count 为 0。
// 只使用按级别计数，不扫描记录。
func (s *Store) Residue(target int) (count, highest int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for lvl := 3; lvl > target; lvl-- {
		if s.levelCount[lvl] > 0 {
			highest = lvl
			break
		}
	}
	for lvl := target + 1; lvl <= 3; lvl++ {
		count += s.levelCount[lvl]
	}
	return count, highest
}

// Scanned 返回内部扫描探针计数（测试用）。
func (s *Store) Scanned() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanned
}
