package snapshot

import (
	"context"
)

// Validate 校验快照按键严格升序。从前往后扫描，第一处违规决定错误类别：
// 与前一个键相等为重复键，小于前一个键为未排序。
func Validate(snap []Entry) error {
	for i := 1; i < len(snap); i++ {
		prev, cur := snap[i-1].Key, snap[i].Key
		switch {
		case cur == prev:
			return newDiffError(ReasonDuplicateKey, "key "+cur+" appears more than once at index "+itoa(i))
		case cur < prev:
			return newDiffError(ReasonUnsorted, "key "+cur+" at index "+itoa(i)+" is not greater than previous key "+prev)
		}
	}
	return nil
}

// Diff 将新快照与旧（当前）快照做有序归并，产出按键升序、每键至多一条的变更日志。
func Diff(ctx context.Context, cfg Config, oldSnap, newSnap []Entry) ([]Change, error) {
	log := loggerFromCtx(ctx)

	log.Printf("diff start: config={MaxChanges:%d} oldLen=%d newLen=%d", cfg.MaxChanges, len(oldSnap), len(newSnap))

	if cfg.MaxChanges < 0 {
		log.Printf("reject: invalid config MaxChanges=%d (must be >=0, 0 means unlimited)", cfg.MaxChanges)
		return nil, newDiffError(ReasonInvalidConfig, "MaxChanges must be >= 0; use 0 for unlimited")
	}

	// 先整体校验两侧输入，任一非法都不得产出任何变更。
	if err := Validate(newSnap); err != nil {
		log.Printf("reject: new snapshot invalid: %v", err)
		return nil, err
	}
	if err := Validate(oldSnap); err != nil {
		log.Printf("reject: old snapshot invalid: %v", err)
		return nil, err
	}

	changes := make([]Change, 0)
	i, j := 0, 0
	for i < len(oldSnap) && j < len(newSnap) {
		oldEntry, newEntry := oldSnap[i], newSnap[j]
		switch {
		case oldEntry.Key < newEntry.Key:
			log.Printf("step i=%d j=%d: old-only key=%q value=%q => delete (old key absent from new snapshot)", i, j, oldEntry.Key, oldEntry.Value)
			changes = append(changes, Change{Op: OpDelete, Key: oldEntry.Key, OldValue: oldEntry.Value})
			i++
		case oldEntry.Key > newEntry.Key:
			log.Printf("step i=%d j=%d: new-only key=%q value=%q => insert (new key absent from old snapshot)", i, j, newEntry.Key, newEntry.Value)
			changes = append(changes, Change{Op: OpInsert, Key: newEntry.Key, NewValue: newEntry.Value})
			j++
		default:
			if oldEntry.Value == newEntry.Value {
				log.Printf("step i=%d j=%d: shared key=%q value=%q => no change (values equal)", i, j, oldEntry.Key, oldEntry.Value)
			} else {
				log.Printf("step i=%d j=%d: shared key=%q old=%q new=%q => update (same key, different value)", i, j, oldEntry.Key, oldEntry.Value, newEntry.Value)
				changes = append(changes, Change{Op: OpUpdate, Key: oldEntry.Key, OldValue: oldEntry.Value, NewValue: newEntry.Value})
			}
			i++
			j++
		}
		if cfg.MaxChanges > 0 && len(changes) > cfg.MaxChanges {
			log.Printf("reject: change count %d exceeds limit %d", len(changes), cfg.MaxChanges)
			return nil, newDiffError(ReasonTooManyChanges, itoa(len(changes))+" changes exceed limit "+itoa(cfg.MaxChanges))
		}
	}

	// 任一侧耗尽后，剩余条目依次输出。
	for ; i < len(oldSnap); i++ {
		log.Printf("tail: old-only key=%q value=%q => delete (new snapshot exhausted)", oldSnap[i].Key, oldSnap[i].Value)
		changes = append(changes, Change{Op: OpDelete, Key: oldSnap[i].Key, OldValue: oldSnap[i].Value})
		if cfg.MaxChanges > 0 && len(changes) > cfg.MaxChanges {
			log.Printf("reject: change count %d exceeds limit %d", len(changes), cfg.MaxChanges)
			return nil, newDiffError(ReasonTooManyChanges, itoa(len(changes))+" changes exceed limit "+itoa(cfg.MaxChanges))
		}
	}
	for ; j < len(newSnap); j++ {
		log.Printf("tail: new-only key=%q value=%q => insert (old snapshot exhausted)", newSnap[j].Key, newSnap[j].Value)
		changes = append(changes, Change{Op: OpInsert, Key: newSnap[j].Key, NewValue: newSnap[j].Value})
		if cfg.MaxChanges > 0 && len(changes) > cfg.MaxChanges {
			log.Printf("reject: change count %d exceeds limit %d", len(changes), cfg.MaxChanges)
			return nil, newDiffError(ReasonTooManyChanges, itoa(len(changes))+" changes exceed limit "+itoa(cfg.MaxChanges))
		}
	}

	log.Printf("diff done: %d change(s) emitted", len(changes))
	return changes, nil
}

// Replay 将变更日志按序应用到旧快照，返回与新快照等价的结果。
//
// 变更日志按键升序且每键至多一条，因此按归并方式一遍扫描即可复现。
func Replay(oldSnap []Entry, changes []Change) []Entry {
	result := make([]Entry, 0, len(oldSnap)+len(changes))
	i := 0
	for _, ch := range changes {
		for i < len(oldSnap) && oldSnap[i].Key < ch.Key {
			result = append(result, oldSnap[i])
			i++
		}
		switch ch.Op {
		case OpInsert:
			result = append(result, Entry{Key: ch.Key, Value: ch.NewValue})
		case OpDelete:
			if i < len(oldSnap) && oldSnap[i].Key == ch.Key {
				i++
			}
		case OpUpdate:
			if i < len(oldSnap) && oldSnap[i].Key == ch.Key {
				result = append(result, Entry{Key: ch.Key, Value: ch.NewValue})
				i++
			}
		}
	}
	for ; i < len(oldSnap); i++ {
		result = append(result, oldSnap[i])
	}
	return result
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
