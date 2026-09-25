// Package rjoin 维护左事件登记、反向索引（Key → leftID 集合）、物化视图与右表增改删触发的重新求值。依赖 rtab。
package rjoin

import (
	"errors"
	"fmt"
	"sync"

	"ontology/rtab"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrEmptyLeftID   = errors.New("rjoin: empty leftID")
	ErrEmptyLeftKey  = errors.New("rjoin: Left empty key")
	ErrDupLeftID     = errors.New("rjoin: duplicate leftID")
	ErrEmptyRightKey = errors.New("rjoin: right empty key")
	// ErrInconsistent 是自检发现内部不变量被破坏时的哨兵错误。
	ErrInconsistent = errors.New("rjoin: inconsistent state")
)

// Join 是增量 join 的状态。右表删除某 key 时，其反向索引条目被移除，
// 这些 leftID 记为 gone：视图保持 ∅，日后同 key 再现也不再重估。
type Join struct {
	mu      sync.RWMutex
	right   *rtab.Table
	rev     map[string]map[string]struct{} // key → leftID 集合（含无匹配时登记的）
	keys    map[string]string              // leftID → join key
	view    map[string]string              // leftID → 当前视图值
	matched map[string]bool                // leftID → 当前是否有匹配
	gone    map[string]struct{}            // 被 DeleteRight 注销的 leftID
	checked int                            // 最近一次 reeval 检查过的左事件个数
}

// New 返回空 Join。
func New() *Join {
	return &Join{
		right:   rtab.New(),
		rev:     make(map[string]map[string]struct{}),
		keys:    make(map[string]string),
		view:    make(map[string]string),
		matched: make(map[string]bool),
		gone:    make(map[string]struct{}),
	}
}

// Left 登记左事件；无论有无匹配都登记进反向索引。校验先于任何状态修改。
func (j *Join) Left(leftID, key string) error {
	if leftID == "" {
		return ErrEmptyLeftID
	}
	if key == "" {
		return ErrEmptyLeftKey
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, dup := j.keys[leftID]; dup {
		return ErrDupLeftID
	}
	j.keys[leftID] = key
	set := j.rev[key]
	if set == nil {
		set = make(map[string]struct{})
		j.rev[key] = set
	}
	set[leftID] = struct{}{}
	j.view[leftID], j.matched[leftID] = j.right.Get(key)
	return nil
}

// UpsertRight 写右表并立即重估该 key 反向索引里的每个 leftID。
func (j *Join) UpsertRight(key, val string) error {
	if key == "" {
		return ErrEmptyRightKey
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.right.Upsert(key, val); err != nil {
		return err
	}
	j.reeval(key)
	return nil
}

// DeleteRight 删右表（不存在则幂等），重估受影响 leftID 为 ∅ 并注销它们。
func (j *Join) DeleteRight(key string) error {
	if key == "" {
		return ErrEmptyRightKey
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.right.Delete(key); err != nil {
		return err
	}
	for id := range j.rev[key] {
		j.gone[id] = struct{}{}
	}
	j.reeval(key)
	delete(j.rev, key)
	return nil
}

// reeval 用右表当前值重写 rev[key] 里每个 leftID 的视图；调用方须持写锁。
func (j *Join) reeval(key string) {
	set := j.rev[key]
	j.checked = len(set)
	for id := range set {
		j.view[id], j.matched[id] = j.right.Get(key)
	}
}

// GetView 返回 leftID 当前视图值及是否匹配（∅ 为 ("", false)）。
func (j *Join) GetView(leftID string) (string, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if !j.matched[leftID] {
		return "", false
	}
	return j.view[leftID], true
}

// Check 核验反向索引与视图的一致性（不变量 1、2），只读、可并发。
func (j *Join) Check() error {
	j.mu.RLock()
	defer j.mu.RUnlock()
	for key, set := range j.rev {
		for id := range set {
			if j.keys[id] != key {
				return fmt.Errorf("%w: rev[%q] 含 %q 但其 join key 是 %q", ErrInconsistent, key, id, j.keys[id])
			}
			if _, isGone := j.gone[id]; isGone {
				return fmt.Errorf("%w: gone 的 %q 仍在 rev[%q]", ErrInconsistent, id, key)
			}
			if v, ok := j.right.Get(key); j.matched[id] != ok || (ok && j.view[id] != v) {
				return fmt.Errorf("%w: %q 视图与右表不符", ErrInconsistent, id)
			}
		}
	}
	for id, key := range j.keys {
		_, isGone := j.gone[id]
		_, inRev := j.rev[key][id]
		if isGone == inRev {
			return fmt.Errorf("%w: %q 登记状态矛盾", ErrInconsistent, id)
		}
		if isGone && j.matched[id] {
			return fmt.Errorf("%w: gone 的 %q 仍有匹配", ErrInconsistent, id)
		}
	}
	return nil
}
