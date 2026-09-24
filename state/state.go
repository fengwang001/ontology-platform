// Package state 持有可变的 map[string]int64 状态，维护自上次检查点以来的脏键集合。
// 本包不依赖其他任何包；并发串行化由上层 api 负责。
package state

import "errors"

// 哨兵错误：可被 errors.Is 判定。
var (
	// ErrEmptyKey 表示 Set 收到空键。
	ErrEmptyKey = errors.New("state: key must not be empty")
	// ErrTooManyKeys 表示新增键会使键数超过 maxKeys。
	ErrTooManyKeys = errors.New("state: key count exceeds maxKeys")
)

// State 是进程内可变状态。
type State struct {
	data    map[string]int64
	dirty   map[string]struct{} // 自上次 DrainDirty 以来值真正发生变化的键
	maxKeys int
}

// New 创建容量上限为 maxKeys 的空状态。
func New(maxKeys int) *State {
	return &State{
		data:    map[string]int64{},
		dirty:   map[string]struct{}{},
		maxKeys: maxKeys,
	}
}

// Set 写入或覆盖一个键；空键或新增超限均被拒绝且状态不变。
// 写入与现值相同的值是无操作：不改状态、不进脏集。
func (s *State) Set(k string, v int64) error {
	if k == "" {
		return ErrEmptyKey // 失败不留痕：触碰 map 之前判定
	}
	old, exists := s.data[k]
	if !exists && len(s.data) >= s.maxKeys {
		return ErrTooManyKeys // 仅新增键受上限约束，覆盖既有键放行
	}
	if exists && old == v {
		return nil
	}
	s.data[k] = v
	s.dirty[k] = struct{}{}
	return nil
}

// Delete 删除一个键；删除不存在的键是无操作（不报错、不进脏集）。
func (s *State) Delete(k string) {
	if _, ok := s.data[k]; !ok {
		return
	}
	delete(s.data, k)
	s.dirty[k] = struct{}{}
}

// Get 读取单个键。
func (s *State) Get(k string) (int64, bool) {
	v, ok := s.data[k]
	return v, ok
}

// Len 返回当前键数。
func (s *State) Len() int { return len(s.data) }

// Snapshot 返回活跃状态的深拷贝。
func (s *State) Snapshot() map[string]int64 {
	out := make(map[string]int64, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

// DrainDirty 取出并清空自上次调用以来值发生过变化的键（无序）。
func (s *State) DrainDirty() []string {
	out := make([]string, 0, len(s.dirty))
	for k := range s.dirty {
		out = append(out, k)
	}
	clear(s.dirty)
	return out
}
