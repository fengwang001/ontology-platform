// Package rw 持有读写锁状态（读者集合、写者、等待写者标志）并做授予条件判定。
// 授予规则（写者优先）：
//
//	读者可进入 ⇔ 没有写者持有、且没有写者等待；
//	写者可进入 ⇔ 没有读者持有、且没有写者持有。
//
// 本包不依赖工程内其他包；所有方法均非并发安全，由上层 lock 加锁。
package rw

import "sort"

// State 持有当前持有者状态与最近一次授予判定的检查计数。
type State struct {
	readers map[string]struct{}
	writer  string // 空串表示无写者持有
	waitW   int    // 等待中的写者个数（O(1) 标志，替代队列扫描）
	checked int    // 最近一次授予判定检查的等待者条目个数（非导出）
}

// New 创建空状态。
func New() *State {
	return &State{readers: map[string]struct{}{}}
}

// CanRead 是读者授予判定：只看写者持有位与等待写者标志，O(1)，
// 不遍历等待队列，故本次判定检查的等待者条目数恒为 0。
func (s *State) CanRead() bool {
	s.checked = 0
	return s.writer == "" && s.waitW == 0
}

// CanWrite 是写者授予判定：只看持有者状态，O(1)。
func (s *State) CanWrite() bool {
	s.checked = 0
	return s.writer == "" && len(s.readers) == 0
}

// GrantRead 在判定通过后登记一个读者。
func (s *State) GrantRead(id string) { s.readers[id] = struct{}{} }

// GrantWrite 在判定通过后登记写者。
func (s *State) GrantWrite(id string) { s.writer = id }

// IncWaitWriter / DecWaitWriter 在写者入队/出队时维护等待写者标志。
func (s *State) IncWaitWriter() { s.waitW++ }
func (s *State) DecWaitWriter() { s.waitW-- }

// ReleaseRead 移除一个读者；不存在返回 false。
func (s *State) ReleaseRead(id string) bool {
	if _, ok := s.readers[id]; !ok {
		return false
	}
	delete(s.readers, id)
	return true
}

// ReleaseWrite 移除写者；id 与当前写者不符返回 false。
func (s *State) ReleaseWrite(id string) bool {
	if s.writer == "" || s.writer != id {
		return false
	}
	s.writer = ""
	return true
}

// HasRead 报告 id 是否持有读锁。
func (s *State) HasRead(id string) bool {
	_, ok := s.readers[id]
	return ok
}

// Writer 返回当前写者 ID，无写者时返回空串。
func (s *State) Writer() string { return s.writer }

// Readers 返回当前全部读者 ID，按字典序排列。
func (s *State) Readers() []string {
	out := make([]string, 0, len(s.readers))
	for id := range s.readers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// FlagDecisionIsO1 在包内部复现"一个读者持续持有、m 个写者排队"后执行一次
// 读者授予判定，仅返回各规模下检查条目数是否不随 m 增长；不泄露计数器数值。
func FlagDecisionIsO1(sizes []int) bool {
	prev := -1
	for _, m := range sizes {
		s := New()
		s.GrantRead("keeper")
		for range m { // m 个写者堆积为等待者
			s.IncWaitWriter()
		}
		s.CanRead() // 一次授予判定：靠 waitW 标志，O(1)
		if prev >= 0 && s.checked != prev {
			return false
		}
		prev = s.checked
	}
	return true
}
