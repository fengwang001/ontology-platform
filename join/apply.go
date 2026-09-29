package join

import (
	"fmt"
	"sort"
)

// RejectReason 区分批被拒绝的原因。
type RejectReason string

const (
	ReasonEmptyKey    RejectReason = "empty-key"     // 连接键为空
	ReasonEmptyID     RejectReason = "empty-id"      // 行标识为空
	ReasonDuplicateID RejectReason = "duplicate-id"  // 插入已存在的标识
	ReasonMissingID   RejectReason = "missing-id"    // 删除不存在的标识
	ReasonTooManyRows RejectReason = "too-many-rows" // 总行数超限
)

// RejectError 表示一个批被拒绝；被拒绝的批不会改变两表或已产生的日志。
type RejectError struct {
	Reason RejectReason
	Change Change
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("join: batch rejected (%s): %s %s id=%q key=%q",
		e.Reason, e.Change.Side, e.Change.Op, e.Change.Row.ID, e.Change.Row.Key)
}

// Apply 校验并应用一个变更批。任一条变更非法则整个批被拒绝，
// 两表与日志保持原状。成功时按确定性顺序追加结果日志。
func (j *Joiner) Apply(batch []Change) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.validate(batch); err != nil {
		return err
	}
	for _, c := range batch {
		j.applyOne(c)
	}
	return nil
}

// validate 在当前状态上模拟整个批，发现首个非法变更即返回。
func (j *Joiner) validate(batch []Change) error {
	leftIDs := make(map[string]bool, len(j.left))
	for id := range j.left {
		leftIDs[id] = true
	}
	rightIDs := make(map[string]bool, len(j.right))
	for id := range j.right {
		rightIDs[id] = true
	}
	total := len(j.left) + len(j.right)

	for _, c := range batch {
		if c.Row.ID == "" {
			return &RejectError{Reason: ReasonEmptyID, Change: c}
		}
		if c.Row.Key == "" {
			return &RejectError{Reason: ReasonEmptyKey, Change: c}
		}
		ids := leftIDs
		if c.Side == Right {
			ids = rightIDs
		}
		switch c.Op {
		case Insert:
			if ids[c.Row.ID] {
				return &RejectError{Reason: ReasonDuplicateID, Change: c}
			}
			total++
			if j.maxRows > 0 && total > j.maxRows {
				return &RejectError{Reason: ReasonTooManyRows, Change: c}
			}
			ids[c.Row.ID] = true
		case Delete:
			if !ids[c.Row.ID] {
				return &RejectError{Reason: ReasonMissingID, Change: c}
			}
			total--
			delete(ids, c.Row.ID)
		}
	}
	return nil
}

// applyOne 应用单条变更并追加结果日志。
// 输出条目按对侧标识的字典序排列；同一左行的相邻两条
// （撤回空填充再补配对，或撤回配对再补空填充）紧邻输出。
func (j *Joiner) applyOne(c Change) {
	if c.Side == Left {
		if c.Op == Insert {
			j.insertLeft(c.Row)
		} else {
			j.deleteLeft(c.Row.ID)
		}
		return
	}
	if c.Op == Insert {
		j.insertRight(c.Row)
	} else {
		j.deleteRight(c.Row.ID)
	}
}

func (j *Joiner) insertLeft(r Row) {
	j.left[r.ID] = r
	addToIndex(j.leftByKey, r.Key, r.ID)

	rightIDs := sortedKeys(j.rightByKey[r.Key])
	if len(rightIDs) == 0 {
		j.matched[r.ID] = false
		j.emit(Insert, r.ID, "", r.Key, "pad: no matching right row")
		return
	}
	j.matched[r.ID] = true
	for _, rid := range rightIDs {
		j.emit(Insert, r.ID, rid, r.Key, "pair: matched right row")
	}
}

func (j *Joiner) deleteLeft(id string) {
	r := j.left[id]
	if j.matched[id] {
		for _, rid := range sortedKeys(j.rightByKey[r.Key]) {
			j.emit(Delete, id, rid, r.Key, "unpair: left row deleted")
		}
	} else {
		j.emit(Delete, id, "", r.Key, "unpad: left row deleted")
	}
	delete(j.left, id)
	removeFromIndex(j.leftByKey, r.Key, id)
	delete(j.matched, id)
}

func (j *Joiner) insertRight(r Row) {
	j.right[r.ID] = r
	addToIndex(j.rightByKey, r.Key, r.ID)

	for _, lid := range sortedKeys(j.leftByKey[r.Key]) {
		if !j.matched[lid] {
			j.matched[lid] = true
			j.emit(Delete, lid, "", r.Key, "unpad: first matching right row arrived")
		}
		j.emit(Insert, lid, r.ID, r.Key, "pair: matched right row")
	}
}

func (j *Joiner) deleteRight(id string) {
	r := j.right[id]
	delete(j.right, id)
	removeFromIndex(j.rightByKey, r.Key, id)

	last := len(j.rightByKey[r.Key]) == 0
	for _, lid := range sortedKeys(j.leftByKey[r.Key]) {
		j.emit(Delete, lid, id, r.Key, "unpair: right row deleted")
		if last {
			j.matched[lid] = false
			j.emit(Insert, lid, "", r.Key, "pad: last matching right row deleted")
		}
	}
}

func (j *Joiner) emit(op Op, leftID, rightID, key, reason string) {
	j.seq++
	j.log = append(j.log, Entry{
		Seq:     j.seq,
		Op:      op,
		LeftID:  leftID,
		RightID: rightID,
		Key:     key,
		Reason:  reason,
	})
}

// Log 返回已产生的结果日志副本，可并发调用。
func (j *Joiner) Log() []Entry {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make([]Entry, len(j.log))
	copy(out, j.log)
	return out
}

// View 返回当前连接结果视图，按 (LeftID, RightID) 字典序排列，可并发调用。
func (j *Joiner) View() []ViewRow {
	j.mu.RLock()
	defer j.mu.RUnlock()
	var out []ViewRow
	for _, lid := range sortedKeys(j.left) {
		r := j.left[lid]
		if !j.matched[lid] {
			out = append(out, ViewRow{LeftID: lid, Key: r.Key})
			continue
		}
		for _, rid := range sortedKeys(j.rightByKey[r.Key]) {
			out = append(out, ViewRow{LeftID: lid, RightID: rid, Key: r.Key})
		}
	}
	return out
}

// Size 返回两表当前总行数，可并发调用。
func (j *Joiner) Size() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return len(j.left) + len(j.right)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func addToIndex(idx map[string]map[string]struct{}, key, id string) {
	set, ok := idx[key]
	if !ok {
		set = make(map[string]struct{})
		idx[key] = set
	}
	set[id] = struct{}{}
}

func removeFromIndex(idx map[string]map[string]struct{}, key, id string) {
	set := idx[key]
	delete(set, id)
	if len(set) == 0 {
		delete(idx, key)
	}
}
