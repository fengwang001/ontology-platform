package ontology

import (
	"context"
	"strconv"
)

// MergeDiff 对两份已经过 Validate 校验的快照做双指针归并差分。
//
// 逐键比较：
//   - 仅旧快照存在：输出 delete；
//   - 仅新快照存在：输出 insert；
//   - 两侧都有且值不同：输出 update；值相同：不输出；
//   - 任一侧指针耗尽后，另一侧剩余条目依次输出。
//
// 输出的变更日志按键严格升序、每键至多一条。maxChanges <= 0 表示不限条数。
// 若变更条数超过上限，返回 ErrTooManyChanges（不返回部分日志）。
// log 为 nil 时不记录过程。
func MergeDiff(ctx context.Context, oldSnap, newSnap []Entry, maxChanges int, log Logger) ([]Change, error) {
	if maxChanges < 0 {
		return nil, reject(KindInvalidConfig, "maxChanges must be >= 0, got "+strconv.Itoa(maxChanges))
	}
	changes := make([]Change, 0)
	i, j := 0, 0
	emit := func(c Change, reason string) error {
		if log != nil {
			log.Logf(ctx, "step old[%d]=%q new[%d]=%q => %s key=%q reason=%s",
				i, keyAt(oldSnap, i), j, keyAt(newSnap, j), c.Op, c.Key, reason)
		}
		if maxChanges > 0 && len(changes) >= maxChanges {
			if log != nil {
				log.Logf(ctx, "reject kind=%s key=%q change-count would exceed limit %d",
					KindTooManyChanges, c.Key, maxChanges)
			}
			return reject(KindTooManyChanges,
				"more than "+strconv.Itoa(maxChanges)+" changes (next key "+c.Key+")")
		}
		changes = append(changes, c)
		return nil
	}
	for i < len(oldSnap) && j < len(newSnap) {
		oldKey, newKey := oldSnap[i].Key, newSnap[j].Key
		switch {
		case oldKey < newKey:
			if err := emit(Change{Op: OpDelete, Key: oldKey, OldValue: oldSnap[i].Value},
				"key only in old snapshot => delete"); err != nil {
				return nil, err
			}
			i++
		case oldKey > newKey:
			if err := emit(Change{Op: OpInsert, Key: newKey, NewValue: newSnap[j].Value},
				"key only in new snapshot => insert"); err != nil {
				return nil, err
			}
			j++
		default:
			if oldSnap[i].Value != newSnap[j].Value {
				if err := emit(Change{
					Op:       OpUpdate,
					Key:      oldKey,
					OldValue: oldSnap[i].Value,
					NewValue: newSnap[j].Value,
				}, "key in both, value differs => update"); err != nil {
					return nil, err
				}
			} else if log != nil {
				log.Logf(ctx, "step old[%d]=%q new[%d]=%q => none key=%q reason=identical key and value",
					i, oldKey, j, newKey, oldKey)
			}
			i++
			j++
		}
	}
	for ; i < len(oldSnap); i++ {
		if err := emit(Change{Op: OpDelete, Key: oldSnap[i].Key, OldValue: oldSnap[i].Value},
			"old snapshot tail remainder => delete"); err != nil {
			return nil, err
		}
	}
	for ; j < len(newSnap); j++ {
		if err := emit(Change{Op: OpInsert, Key: newSnap[j].Key, NewValue: newSnap[j].Value},
			"new snapshot tail remainder => insert"); err != nil {
			return nil, err
		}
	}
	if log != nil {
		log.Logf(ctx, "merge done changes=%d (limit=%d)", len(changes), maxChanges)
	}
	return changes, nil
}

func keyAt(s []Entry, i int) string {
	if i < len(s) {
		return s[i].Key
	}
	return "<end>"
}

// Replay 将变更日志按键升序按序应用到基础快照，返回得到的新快照。
//
// base 必须已经过校验，changes 必须是对 base 与某目标快照做 MergeDiff 的产物。
// 任何不匹配（删除/更新的键在当前状态中不存在、值不符，或插入的键已存在，
// 或日志未按键升序）都会整体返回错误且不产出部分结果。
func Replay(base []Entry, changes []Change) ([]Entry, error) {
	out := make([]Entry, 0, len(base)+len(changes))
	out = append(out, base...)
	lastKey := ""
	for ci, c := range changes {
		if c.Key <= lastKey {
			return nil, reject(KindUnsorted,
				"change at index "+strconv.Itoa(ci)+" key "+c.Key+" is not strictly greater than previous key "+lastKey)
		}
		lastKey = c.Key

		pos := searchEntry(out, c.Key)
		switch c.Op {
		case OpInsert:
			if pos < len(out) && out[pos].Key == c.Key {
				return nil, reject(KindDuplicateKey, "insert of existing key "+c.Key)
			}
			out = append(out, Entry{})
			copy(out[pos+1:], out[pos:])
			out[pos] = Entry{Key: c.Key, Value: c.NewValue}
		case OpDelete:
			if pos >= len(out) || out[pos].Key != c.Key || out[pos].Value != c.OldValue {
				return nil, reject(KindInvalidConfig, "delete of missing or mismatched key "+c.Key)
			}
			out = append(out[:pos], out[pos+1:]...)
		case OpUpdate:
			if pos >= len(out) || out[pos].Key != c.Key || out[pos].Value != c.OldValue {
				return nil, reject(KindInvalidConfig, "update of missing or mismatched key "+c.Key)
			}
			out[pos].Value = c.NewValue
		default:
			return nil, reject(KindInvalidConfig, "unknown op "+string(c.Op)+" at index "+strconv.Itoa(ci))
		}
	}
	return out, nil
}

func searchEntry(s []Entry, key string) int {
	lo, hi := 0, len(s)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if s[mid].Key < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}
